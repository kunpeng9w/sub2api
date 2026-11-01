# WebSocket 复用日志修正：上线验收（2026-11-01）

**用户已完成部署，基础检查和真实多轮复用标记验证通过。** 新加坡时间 2026-11-01 12:44:09 启动的新程序对应提交 `5957d502d942d4cfebd9595e269beae5e397e45c`。本次检查只读，没有替换程序、重启、回滚或主动发送付费模型探测。

| 项目 | 结果 |
| --- | --- |
| 程序身份 | 磁盘与实际进程 SHA256 均为 `deb10cee0365d57554a33b6ac71303f18efb41f1a23e5e12cc9c396de819b3f2` |
| 应用与依赖 | 应用 active/running、自动重启 0；Nginx、PostgreSQL、Redis、Mihomo、Claude DNS 和出口保护均 active |
| HTTP/HTTPS | 应用及本机 Nginx TLS/SNI 健康接口均 ok；嵌入前端 200；Nginx 配置检查通过 |
| 配置与备份 | 配置与本次备份逐字节相同；备份摘要通过、独立回滚脚本存在 |
| 出口保护 | claude-only、confirmed、consistent=true |
| 修正生效 | 5 组多轮 WS 连接的后续轮次均为 true；已观察到新连接首轮 false 和已有连接首轮 true |

截至 2026-11-01T12:57:28.071962+08:00 快照，46 次上游尝试均有完成记录，其中 12 次拨号、32 个 WS 轮次、2 次 HTTP 调用。上游审计中的 HTTP 响应均为 200；出口探测有 fresh 结果。WS 轮次的用户/API Key/请求标识齐全，代理地址未含认证信息，审计字段中未发现密钥或请求/响应正文。

典型同连接序列为 `false, true, true, true, true`；也观察到从池中借到已有连接后各轮均为 true。最初的复用标记问题已修正。完整脱敏证据见 [POST-DEPLOY-EVIDENCE.json](POST-DEPLOY-EVIDENCE.json)。

日志未见 ERROR/FATAL/PANIC 级别记录。5 条既有启动 WARN 与之前相同（allowlist/SSRF 关闭两次、本地定价兜底文件缺失、trusted_proxies 未配置、CORS allowed_origins 未配置）；另有一条 `openai_token_cache_get_failed`，其错误为 `redis: nil`。结合现有 token provider 代码，这是缓存未命中后继续走凭据/刷新分支，未观察到对应转发失败，本次没有修改缓存处理逻辑。

HTTP 传输和 WebSocket 已有真实自然流量；本次没有单独确认 SSE、Claude/Mihomo 或插件业务的完整生产链路，没有执行故障注入、实际回滚或整机重启。HTTPS 检查通过本机 Nginx 并验证证书，不等同于外部网络视角的公网验收。

[validation.json](validation.json) 是部署前代码/构建验收快照，其中尚未部署的标记保留历史含义；当前上线状态以本文为准。无需重复部署。日常使用方法见 [USAGE.md](USAGE.md)。
