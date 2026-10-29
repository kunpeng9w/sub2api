# 账号代理优先：手动部署操作单

准备状态：预检及脚本验证通过，**尚未执行部署**。本次只替换嵌入前端的应用二进制，并停止、启动 Sub2API；已有请求和流式长连接可能中断。Mihomo、DNS、防火墙和账号绑定均不作调整。

已核对的线上版本为 `b1736319920d76c30a87061dad3205a0a4a6322d`，程序 SHA256 为 `1966de62f70ac92bb8840e6c886b7d4885cd88765cfdf13af2ed4e795e6300a8`。目标为通过完整 CI 的 `5ed77ee58816d638350e26ff22f10918f8dff82a`；两者的 `backend/migrations` 无差异。本次不执行数据库恢复、账号改绑或配置重建。

## 1. 预检（不会停服务）

在当前服务器终端执行，可从任意目录运行：

```bash
sudo bash /home/pk/workspace/sub2api/ops/changes/20261029-account-proxy-priority/deploy.sh --check
```

预期输出：`Preflight passed. No files installed and no services stopped.`

脚本重新核对构建包和二进制的固定摘要、磁盘与运行中程序是否相同、已审查的旧版本、systemd 启动路径、健康状态及出口保护。还会检查现有 `gateway.claude_egress.enabled=true`。部署时保持 Mihomo 及现有 DNS/保护服务运行。

如果这里失败，先看输出，不执行下一步；脚本不会用陌生版本覆盖当前程序。若目标版本已经运行，脚本会提示并直接结束。

## 2. 正式部署（会重启 Sub2API）

准备好短时中断窗口后执行：

```bash
sudo bash /home/pk/workspace/sub2api/ops/changes/20261029-account-proxy-priority/deploy.sh --apply
```

脚本会：

1. 再次预检，并在 root 私有目录备份旧程序和当前配置，生成摘要与独立的 `rollback.sh`。
2. 在程序所在文件系统暂存并校验新程序，然后停止 Sub2API、原子替换程序并启动。
3. 等待健康接口恢复，核对实际运行程序的 SHA256，确认配置未改变及出口保护仍一致。
4. 在切换过程中失败时尝试自动恢复旧程序并检查健康；原部署命令仍返回失败状态。如自动恢复也失败，会明确输出手动恢复命令。

成功时会输出：

```text
Deployment passed: commit 5ed77ee58816d638350e26ff22f10918f8dff82a, running binary verified, health=ok.
```

同时输出 `Backup:`、`Manual rollback:` 和备份目录。请保留该目录及终端输出。备份目录采用 `/root/sub2api-proxy-priority-日期时间.随机字符`，时间为 Asia/Singapore；不依赖跨命令保存 shell 变量。

## 3. 上线后检查

```bash
systemctl is-active sub2api mihomo dnsmasq-claude sub2api-egress
curl -fsS --max-time 5 http://127.0.0.1:8080/health
sudo /usr/local/sbin/mihomo-ctl status
```

预期服务均 active，健康为 `{"status":"ok"}`，保护为 `claude-only`、`confirmed`、`consistent=true`。

强制刷新后台页面，打开官方 Claude 账号的新增或编辑窗口，确认可以选择账号代理，并显示“账号代理优先；未设置使用 Mihomo”的说明。部署过程不会自动添加、清空或更换账号代理。

基础健康检查不代替真实 Claude 账号验收。使用自己的账号做后续业务验收时，检查转发、状态/用量查询及授权刷新是否正常。无需为了部署而停止线上 Mihomo；进程启停和代理故障演练可单独安排。

## 4. 手动回滚

使用步骤 2 输出的 **实际完整命令**，例如将下方路径换成终端输出的备份目录：

```bash
sudo bash /root/实际备份目录/rollback.sh
```

回滚脚本先验证备份程序摘要，再仅恢复旧程序并启动、检查健康，不恢复或清空账号绑定，也不覆盖后来修改的应用配置。

旧版本不支持官方 Claude 独立代理；如果上线后已经添加此类绑定，回滚后这些账号会被旧策略拒绝，不能为了回滚而自动清空它们。

## 准备阶段验证

部署脚本已通过 Bash 语法检查和真实服务器的只读 `--check`，没有执行 `--apply`。9 项临时目录隔离模拟通过，覆盖预检不变更、正常部署及手动回滚、启动失败自动恢复、健康失败自动恢复、构建损坏、陌生旧版本、磁盘与进程版本不一致、默认出口开关关闭及备份损坏。模拟中的 systemctl、网络健康、安装和进程文件均为替身，不操作真实服务。

如需重复该隔离验证：

```bash
python3 -m unittest discover -s /home/pk/workspace/sub2api/ops/changes/20261029-account-proxy-priority -p test_deploy.py -v
```
