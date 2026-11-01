# v0.2.19 上线检查（2026-11-01）

用户已完成部署。新加坡时间 **2026-11-01 15:31** 检查：程序身份、数据库迁移、基础服务和已观察到的 WebSocket 业务通过；有一项失效的监控账号关联需要处理，管理端登录尚未验证通过。

本次检查未替换程序、重启服务、修改线上配置或数据库业务数据，未主动发送付费模型请求。脱敏快照见 [POST-DEPLOY-EVIDENCE.json](POST-DEPLOY-EVIDENCE.json)。

| 项目 | 结果 |
| --- | --- |
| 运行程序 | `0.2.19`，构建提交 `6690819d4819b43f4d3d4f670594d3a4b3cec3c0` |
| 程序校验 | 磁盘和 `/proc/450540/exe` SHA256 均为 `012b45620c714bc187405a49a14ad3810f583f6fa8ecdb5d3c6ebe0234961d72`，与验收构建一致 |
| 应用 | 15:28:42 启动，active/running，自动重启 0 |
| 依赖 | Nginx、PostgreSQL、Redis、Mihomo、Claude DNS、出口保护均 active |
| HTTPS 与前端 | 本机 Nginx TLS/SNI `/health` 返回 200/ok，证书校验通过；嵌入前端 200；`nginx -t` 通过 |
| 认证边界 | 无效下游 API Key 返回 401；数据库设置确认公开注册关闭 |
| 数据库 | 迁移 247、248 于 15:28:42 完成；按迁移器的 TrimSpace 规则计算的 SHA256 与数据库一致；新增表/列存在 |
| 出口保护 | `claude-only`、`confirmed`、`consistent=true` |
| 备份 | `/root/sub2api-before-v0.2.19.VPC5OI` 有旧程序、配置和部署前数据库 dump；dump 目录可由 `pg_restore --list` 读取，未做恢复演练 |
| 配置 | 当前 `data/config.yaml` 与部署前备份逐字节相同 |
| 资源 | 根分区使用率 48%，可用约 15 GiB；系统 available 内存约 453 MiB；采样时应用内存约 128 MiB |

自然流量快照中，19 次上游尝试均有 `outcome=completed` 的完成记录，其中 8 次 WS 拨号、11 个 WS 轮次；全部记录为 `account_proxy`，未见未完成的 attempt。两个连接分别完成 9 轮和 2 轮，每个连接首轮 `connection_reused=false`，后续均为 true。两个独立出口探测均为 fresh；探测 IP 不等同于模型请求实际公网源 IP 的直接证明。

新进程日志快照无 ERROR/FATAL/PANIC，有 6 条 WARN：5 条为此前已记录的启动警告（allowlist/SSRF 关闭两次、本地定价兜底文件缺失、trusted_proxies 未配置、CORS allowed_origins 未配置）；另有 `channel_monitor: load linked account failed`。数据库确认：**监控 #6 仍启用，绑定账号 #8；账号 #8 已于 14:56:54 软删除**。这会令该监控的额度查询降级，应在管理界面重新关联有效账号，或停用不再使用的监控。本次没有修改该配置。

使用 `.operator/admin.json` 中既有凭据进行一次管理员登录检查返回 401。因此不能宣称管理端登录已验收，也不能据此认定所有管理员均无法登录；本次未重试密码或重置凭据。

系统还保留两个历史 failed 单元：`aegis.service`（10 月 30 日）和 `sub2api-phase2-switch-20261029-480btl.service`（10 月 29 日），均早于此次部署。历史记录中的 101/102 回程规则目前仍缺失；本机 HTTPS 通过不代表外部网络视角的公网验收。

本次没有覆盖真实 HTTP/SSE、Claude/Mihomo、Grok 或插件业务，没有执行隔离迁移/旧程序兼容性验证、实际回滚或整机重启。数据库备份存在不代表恢复与回滚兼容性已验证。此前 [validation.json](validation.json) 与 README 中“尚未部署”是部署前快照，当前线上状态以本文为准。
