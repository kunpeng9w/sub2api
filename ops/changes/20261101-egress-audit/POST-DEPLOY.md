# 请求出口审计：上线检查（2026-11-01）

用户已完成部署；基础检查和已观察到的 WebSocket 业务通过，存在一项日志标记待修正。应用于新加坡时间 **2026-11-01 12:14:06** 启动，本次检查没有替换程序、重启、回滚或修改线上配置。完整脱敏快照见 [POST-DEPLOY-EVIDENCE.json](POST-DEPLOY-EVIDENCE.json)。

| 项目 | 结果 |
| --- | --- |
| 实际版本 | `742271e18840fdde7fab5c120ee827b122da080c` 对应构建 |
| 程序校验 | 磁盘与实际进程 SHA256 均为 `8de67bdabaa7fb63ccd138cbd4aab5c1c3c362498d99dec6b34dd9d6cc4e443e` |
| 应用 | active/running，自动重启次数 0，ExecMainStatus=0 |
| 依赖服务 | Nginx、PostgreSQL、Redis、Mihomo、Claude DNS 和出口保护均 active |
| HTTP/HTTPS | 应用 `/health` 与通过本机 Nginx TLS/SNI 的 `/health` 均为 ok，嵌入前端返回 200；Nginx 配置检查通过 |
| 配置与备份 | 配置与部署前备份逐字节一致；旧程序和配置摘要通过，独立回滚脚本存在 |
| 出口保护 | `claude-only`、`confirmed`、`consistent=true` |

## 新增日志已生效

截至本次快照，同一 WebSocket 连接连续 **7 轮**请求成功，另有 4 次连接池拨号，11 个 attempt 均有完成记录，未见传输失败或未结束的 attempt。轮次中的用户 ID、API Key ID、请求 ID、账号与代理字段齐全，代理地址不含认证信息，未发现请求/响应正文或密钥字段。独立出口探测成功，状态为 `fresh`；这里的 IP 仍只代表探测请求的观测，不能证明模型请求的实际公网源 IP。

新进程日志有 5 条 WARN，未见 ERROR/FATAL/PANIC 级别记录。与旧进程启动日志核对，均为既有配置警告：URL allowlist/SSRF 检查关闭（出现两次）、本地定价兜底文件缺失、trusted_proxies 未配置、CORS allowed_origins 未配置。本次没有把这些既有差异视为已修复。

## 已确认的日志标记问题

同一 `conn_id` 的 `turn_id=1..7` 均记录 `connection_reused=false`。`backend/internal/service/openai_ws_pool.go` 的 `beginEgressAudit` 直接读取 `lease.Reused()`，它表示租约借出时连接是否已经复用，不随同一租约内的后续轮次更新。因此，该字段不能表示当前轮次是否使用先前轮次的连接。

`conn_id`、递增的 `turn_id` 和独立的 `attempt_id` 仍能正确关联各轮，请求正常完成。此问题不构成本次运行失败或回滚理由，但应另行修正每轮审计标记或明确拆分租约与轮次的复用语义，并补充同租约多轮的连接池审计测试。本次检查只记录问题，没有修改已部署程序。

源码修正已完成并通过 CI，待单独手动部署；操作单见 [WebSocket 复用日志修正](../20261101-ws-reuse-audit/DEPLOY.md)。本文保留旧构建的现场记录。

## 验证边界

本次利用部署后的自然流量观察 WebSocket，没有主动发送付费模型探测。HTTP/SSE、Claude/Mihomo 请求路径和插件路径尚未在该审计快照中出现，不宣称这些路径的真实生产业务均已验收。未执行故障注入、实际回滚、整机重启或外部网络视角的公网连通性测试；HTTPS 检查通过本机 Nginx 监听并验证证书。

[validation.json](validation.json) 保留部署前构建和代码验收快照，其中 `deployed=false` 是当时状态；当前上线状态以本文和 POST-DEPLOY-EVIDENCE 为准。无需重复执行部署。
