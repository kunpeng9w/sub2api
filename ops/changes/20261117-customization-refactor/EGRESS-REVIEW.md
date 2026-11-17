# 固定出口实施决定

本轮按实际出口逐类评估后保留现有实现，不引入新的 HTTPUpstream 外层包装或 `custom/egresspolicy` 包，不删除 120 个已登记的代理解析调用。依据：规则已在 `account_egress.go` 集中；不同客户端以及选路、等待后使用、实际建连/重定向、每轮 WS 审计、仓储生命周期承担不同保护。当前无法证明删除这些层能减少维护成本且覆盖等价，满足计划的停止条件。

| 路径 | 结构决定与保留证据 |
| --- | --- |
| HTTP / SSE / TLS | `repository/http_upstream.go` 完整保持基线；最终读取账号/代理仓储、规范化路由比较、Do/DoWithTLS 与监控上下文校验仍在原位置；响应体/池化/TLS 行为不改 |
| 连接池及监控 | route/revision、重定向禁止、目标校验和旧连接回收不改；`channel_monitor_access.go`、`account_monitor_usage.go` 原样保留；状态适配只移动既有函数 |
| WS | acquire 前、排队拿到 lease 后、dial 前校验及兼容键原样保留；不把每轮审计并到握手审计 |
| OAuth / req | 绑定解析、sessionID/inputID/URL 快照比较、仓储重读及尚无账号时的选择逻辑不改；独立 req 工厂不能假设已被 HTTPUpstream 覆盖 |
| 插件 | 宿主的当前绑定检查、请求发送后禁止重播及审计上下文不改 |
| 后台图片/媒体 | WithAccountEgress、RecordAccountEgress、DetachedAccountEgress 与批任务 transport 不改；缺少最终路由仍失败 |
| 过期/更新/续期/删除 | 到期保留 ProxyID；事务内快照失效与 outbox、扫描快照重读、拒删绑定代理都不改；专项 CI 增加真实数据库到期/续期用例执行证据 |

Actions 的严格出口专项明确要求：绑定异常、最终 HTTP 校验、HTTP/SSE 不回退、WS lease 复核、OAuth 会话、插件、脱离请求媒体、批任务、快照/outbox 原子性，以及数据库 legacy direct/backup 和续期扫描用例必须真实 pass。其他已有回归由 ops/monitor/backend/upgrade 工作流继续覆盖。

Mihomo DNS 生命周期的远程失败在原基线 `0161258af6ca167f5885c145b454d3a982839939` 上也能重现：运行 `43417474087` 的重新执行，job `133843996010`，`test_real_lifecycle_start_stop_restart_crash_and_failed_start` 在测试 fixture 的 SIGKILL 后未恢复普通 DNS。该问题没有通过修改生产出口、跳过断言或回退逻辑来处理。最终远程结果另见 VERIFICATION.md；不能把这个失败写成通过。
