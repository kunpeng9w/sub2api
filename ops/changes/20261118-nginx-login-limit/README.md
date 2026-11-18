# 登录入口 Nginx 限速

2026-11-18：每个直接连接 Nginx 的来源 IP，密码登录和 TOTP 验证共用 `3r/m` 额度，无 burst，约每 20 秒放行一次。成功与失败请求均计数，超限立即返回 HTTP 429。同一出口 IP 的多个用户共享额度。此规则不提供“连续重试三次”的突发额度，也不是自然分钟清零的计数器。

规则只匹配 `/api/v1/auth/login`、`/api/v1/auth/login/2fa`（兼容尾斜杠）；其他路径使用空限流键。保留既有转发、SSE 和 WebSocket 配置。当前架构是客户端直接访问 Nginx，按 `$binary_remote_addr` 计数；如果以后引入 CDN，需要先配置可信代理的真实来源 IP。

验证使用 `.github/workflows/nginx-security-ci.yml`，在 GitHub Actions 的 Ubuntu 24.04 中运行真实 Nginx、临时证书和模拟上游，检查配置语法、超限 429、20 秒后恢复、TOTP 共用额度、不同 IP 独立计数、伪造转发头无法绕过、其他路径及 Upgrade 头转发。模拟上游不能代替真实流式或 WebSocket 全链路验收。

部署前备份 `/etc/nginx/sites-available/sub2api`，在精确提交的远程检查通过后安装仓库的 `ops/config/nginx/sub2api.conf` 并平滑重载 Nginx。生产只做只读配置核对、健康检查和被动日志观察；不发送密码猜测请求。部署状态及 CI 记录见同目录 `DEPLOY.md`。
