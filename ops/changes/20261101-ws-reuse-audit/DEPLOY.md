# WebSocket 连接复用日志修正：手动部署

状态：**验收通过，产物校验、15 项部署模拟和服务器只读预检通过；等待用户手动部署，尚未部署。**源码提交为 `5957d502d942d4cfebd9595e269beae5e397e45c`，分支 `codex/egress-audit`。[构建和专项回归](https://github.com/kunpeng9w/sub2api/actions/runs/41272880391) 已通过。[完整 CI](https://github.com/kunpeng9w/sub2api/actions/runs/41272880393) 和 [安全扫描](https://github.com/kunpeng9w/sub2api/actions/runs/41272880406) 均通过，最终记录见 [validation.json](validation.json)。

产物已下载到 `/home/pk/workspace/sub2api-egress-delivery/5957d502d942d4cfebd9595e269beae5e397e45c/`。程序 SHA256 为 `deb10cee0365d57554a33b6ac71303f18efb41f1a23e5e12cc9c396de819b3f2`，构建包 SHA256 为 `25a4e28ed462b9c826078dc156cd07ed8a6695052d9048622b0ac358519f0cc1`。

本次只修正连接池每轮审计的 `connection_reused` 标记，没有数据库迁移。新连接首轮为 false，同一租约后续轮次为 true；借到已有连接时首轮即为 true；会话中途换新连接重新从 false 开始。请求转发和原有连接池获取指标保持不变。

## 1. 预检

```bash
sudo bash /home/pk/workspace/sub2api/ops/changes/20261101-ws-reuse-audit/deploy.sh --check
```

预期：`Preflight passed. No files installed and no services stopped.`

脚本固定构建包、目标程序及旧程序摘要，核对实际进程、systemd 启动路径、健康接口、配置和出口保护。允许替换的旧程序 SHA256 为 `8de67bdabaa7fb63ccd138cbd4aab5c1c3c362498d99dec6b34dd9d6cc4e443e`（已部署的 `742271e18` 构建）。失败时先处理原因，再进行部署。

## 2. 手动部署

准备好短暂重启窗口后执行；现有请求、SSE 和 WebSocket 连接可能中断：

```bash
sudo bash /home/pk/workspace/sub2api/ops/changes/20261101-ws-reuse-audit/deploy.sh --apply
```

脚本再次预检，在 root 私有目录保存旧程序及配置并生成 `rollback.sh`，暂存并校验新程序，然后停止 Sub2API、原子替换和启动。健康恢复后校验实际进程摘要、配置与出口保护；切换失败时尝试自动恢复旧程序，原命令仍返回失败状态。

保留输出的 `Backup:` 和 `Manual rollback:` 完整路径。备份目录形如 `/root/sub2api-ws-reuse-audit-日期时间.随机字符`，使用新加坡时间。Mihomo、DNS、防火墙、账号绑定和数据库配置沿用现状。

## 3. 上线后验证

```bash
systemctl is-active sub2api mihomo dnsmasq-claude sub2api-egress
curl -fsS --max-time 5 http://127.0.0.1:8080/health
sudo /usr/local/sbin/mihomo-ctl status
sudo journalctl -u sub2api --since '10 minutes ago' -o cat | rg 'upstream_egress_'
```

预期服务 active、健康 ok，出口保护 `claude-only`、`confirmed`、`consistent=true`。使用现有客户端在同一 WebSocket 连接中连续发送至少两轮请求，检查审计日志：

- 相同租约的新连接首轮 `connection_reused=false`，后续为 true；从池中借到已有连接的首轮可以为 true。
- `conn_id` 保持一致，每轮有独立 `attempt_id`，started/completed 的复用标记一致。
- `turn_id` 继续递增；实际换到新连接时，虽然轮次编号大于 1，新连接首轮仍应是 false。

拨号阶段 `phase=dial` 与请求轮次应分开判断。基础健康检查不能替代真实多轮请求验证。

## 4. 回滚

如需回滚，执行本次部署输出的真实完整命令：

```bash
sudo bash /root/实际备份目录/rollback.sh
```

回滚仅恢复旧程序并验证健康，不覆盖后来修改的配置、不恢复数据库或清空账号绑定；旧版本的日志标记问题也会恢复。

部署脚本的 15 项临时目录模拟已通过，不调用真实服务。真实服务器只读 `--check` 也已通过，尚未执行 `--apply`。模拟可复现：

```bash
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s /home/pk/workspace/sub2api/ops/changes/20261101-ws-reuse-audit -p test_deploy.py -v
```
