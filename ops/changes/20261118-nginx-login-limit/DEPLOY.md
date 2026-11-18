# 登录限速部署记录

部署完成：2026-11-18 11:37:30，Asia/Singapore（UTC+8）。用户授权将登录入口设为每个来源 IP 每分钟最多三次。

- 已安装 `ops/config/nginx/sub2api.conf` 到 `/etc/nginx/sites-available/sub2api`，密码登录与 TOTP 验证共用 `3r/m` 额度，无突发额度，约每 20 秒放行一次，超限 HTTP 429。计数包含成功、失败以及匹配路径上的其他方法请求。其他路径使用空键，不参与限流。
- 配置与 GitHub Actions 通过的精确提交 `0d7d2f0adea61343e7a6f7d2908a0771b9b5e4e1` 内容一致；线上配置 SHA256：`0eb8883ff3320c134ed879b7674b3988b7f4b290deeaf7d69dbd8ede1e7f87cf`。
- 远程 Nginx 检查：[Actions 44231560263](https://github.com/kunpeng9w/sub2api/actions/runs/44231560263)，结论 success。真实 Nginx 配置语法、首次放行/重复拒绝、TOTP 共用额度、不同来源独立计数、伪造转发头不能绕过、20.5 秒后恢复、其他 API 路径不受限、Upgrade 头转发均通过。此前两轮仅测试夹具失败：默认日志目录权限、监听器就绪等待；已修正夹具并重跑，生产候选配置未因此变更。
- 生产 Nginx 1.24.0；平滑重载成功，master PID 保持 1015313，新 worker PID 为 1167414、1167415。以站点域名验证本机 HTTPS `/health`，返回 `{"status":"ok"}`。未重启应用。
- 本机未运行编译、测试、Lint 或 `nginx -t`；仅执行配置内容/哈希检查、部署、服务状态与 HTTPS 健康检查。未在生产发送登录猜测请求，未执行完整 SSE/WebSocket 业务请求验收；远程模拟验证不代表完整上游业务验收。
- 此提交的通用应用 CI 不作为本次 Nginx 配置部署的验收条件，前后端源码及二进制未改变；本次通过结论仅指上述 Nginx 专项工作流。

回滚点：`/home/pk/workspace/sub2api/.operator/nginx-login-limit-20261118T113341`（root 私有目录），包含 `sub2api.conf.before`、候选配置和 `metadata.json`。旧配置 SHA256：`4e76233bb903ae317a84ac60133c594639790f5e88fa86f8fb1ba112f0437a6b`。如需回滚，先确认线上没有新的配置变更，再恢复该备份并平滑重载 Nginx；本次未执行回滚。

远程检查日志及状态保存在 Git 忽略目录 `ops/audits/20261118-nginx-login-limit/`。源码与验证脚本已推送至 `codex/nginx-login-limit-3`；本记录在本地工作目录维护。原有未提交修改保持原样。
