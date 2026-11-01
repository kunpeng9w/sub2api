# 请求出口审计：手动部署操作单

状态：**用户已于新加坡时间 2026-11-01 12:14:06 完成部署，无需重复部署。**基础检查、已观察到的 WebSocket 业务及日志待修正项见 [上线检查](POST-DEPLOY.md)。以下操作保留供恢复参考。最终验收记录见 [validation.json](validation.json)。[完整 CI](https://github.com/kunpeng9w/sub2api/actions/runs/41270389665)、[安全扫描](https://github.com/kunpeng9w/sub2api/actions/runs/41270389709) 均通过。

候选源码提交：`742271e18840fdde7fab5c120ee827b122da080c`，分支 `codex/egress-audit`。前后端均由 GitHub Actions 构建，本机只下载、校验和暂存产物。[构建与审计回归记录](https://github.com/kunpeng9w/sub2api/actions/runs/41270389720)。本次补充请求出口日志，并修复两个前端间接依赖；没有数据库迁移。

产物已暂存到 `/home/pk/workspace/sub2api-egress-delivery/742271e18840fdde7fab5c120ee827b122da080c/`。程序 SHA256 为 `8de67bdabaa7fb63ccd138cbd4aab5c1c3c362498d99dec6b34dd9d6cc4e443e`，构建包 SHA256 为 `ffcec58454c62a81361776a3665c85ecdb164205eedb2a8510fbb77dec623e06`，脚本已固定这些值。

## 1. 预检

在服务器终端执行：

```bash
sudo bash /home/pk/workspace/sub2api/ops/changes/20261101-egress-audit/deploy.sh --check
```

预期输出：`Preflight passed. No files installed and no services stopped.`

脚本核对构建包与程序的固定 SHA256、当前磁盘及进程程序、systemd 启动路径、健康接口、现有 Claude 出口配置和出口保护。已审查的旧程序 SHA256 为 `89d3c99845988494d44ede994911b288263b7b7b10e302ca78bfbc0ee2943a32`。遇到陌生版本或校验失败会退出，应先处理失败原因。

## 2. 手动部署

安排短暂中断窗口后执行；重启会中断进行中的请求、SSE 和 WebSocket 连接：

```bash
sudo bash /home/pk/workspace/sub2api/ops/changes/20261101-egress-audit/deploy.sh --apply
```

脚本先再次预检，将旧程序和配置备份到 root 私有目录，生成独立 `rollback.sh`，暂存并校验新程序，然后停止 Sub2API、原子替换并启动。等待健康恢复后检查实际进程程序摘要、配置完整性及出口保护。切换失败时尝试自动恢复旧程序，并以失败状态退出。

成功输出以 `Deployment passed:` 开头。请保留终端输出中的 `Backup:` 和 `Manual rollback:` 完整路径。备份目录形如 `/root/sub2api-egress-audit-日期时间.随机字符`，使用新加坡时间。

这一步只替换应用二进制。Mihomo、DNS、防火墙、数据库、Redis 和账号代理绑定沿用现状。

## 3. 上线后验收

```bash
systemctl is-active sub2api mihomo dnsmasq-claude sub2api-egress
curl -fsS --max-time 5 http://127.0.0.1:8080/health
sudo /usr/local/sbin/mihomo-ctl status
sudo journalctl -u sub2api --since '10 minutes ago' -o cat | rg 'upstream_egress_'
```

预期服务均 active，健康为 `{"status":"ok"}`，出口保护为 `claude-only`、`confirmed`、`consistent=true`。

使用现有客户端分别发送一次普通请求、SSE 流式请求和同一 WebSocket 连接上的两轮请求，再检查日志：

- 用户 ID、API Key ID、账号 ID/名称、代理名称及去认证的代理地址正确；能用 `request_id` 和 `attempt_id` 关联。
- SSE 收到响应头只记录 response；EOF、读取错误或关闭响应体才产生 completed。
- WebSocket 拨号带 `phase=dial`；同连接多轮有各自的 attempt，连接标识可关联。
- 账号独立代理优先于 Mihomo 默认；未绑定的官方 Claude 路径显示 `Mihomo` 和 `exit_ip_status=unavailable`。
- 普通代理的独立出口探测可能先 pending、后 fresh，也可能 failed；失败不回退直连。探测 IP 仅代表该次独立查询，不能证明模型请求的实际公网源 IP。

基础健康通过不代表真实模型、流式和 WebSocket 业务已经验收。日志字段和证据边界详见 [README.md](README.md)。

## 4. 回滚

执行步骤 2 输出的实际命令：

```bash
sudo bash /root/实际备份目录/rollback.sh
```

脚本先验证备份程序摘要，再恢复旧程序、启动应用并等待健康。它不会覆盖后来修改的配置、恢复数据库或清空账号绑定。回滚后新增审计日志停止产生。

## 部署脚本验证

15 项模拟测试均通过。在临时目录中替换 systemctl、健康请求、出口保护和进程文件，不调用真实服务：

```bash
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s /home/pk/workspace/sub2api/ops/changes/20261101-egress-audit -p test_deploy.py -v
```

覆盖只读预检、正常部署及手动回滚、启动/健康失败自动恢复、瞬时未就绪重试、无效健康返回、程序/构建包损坏、重复执行幂等、陌生旧版本、进程与磁盘版本不一致、默认出口关闭和备份损坏。
