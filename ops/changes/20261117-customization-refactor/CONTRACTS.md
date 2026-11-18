# 定制契约与接入登记

日期：2026-11-17（Asia/Singapore）。本轮基线固定为 `0161258af6ca167f5885c145b454d3a982839939`；上游 v0.2.21 为 `10ba8f9b4dc2e6023d4a9879ab77b21841babd10`；仅演练目标 v0.2.22 为 `b0fa9f3e6eeab1968a53e1289b69daa4bb40ca2b`。不合入升级提交。

## 出口固定契约

绑定不可用时当前账号阻断并保留绑定；允许网关按原规则选择其他账号，不允许为同一账号回退直连、备用代理或托管 Claude 默认。未绑定普通账号仍按原规则直连，未绑定官方 Claude 仍按配置使用托管出口。显式绑定优先于托管默认，不能用空字符串掩盖仓储错误。

| 实际请求路径与接入 | 保留原因/依赖 | 既有回归（精确顶层名称或前缀） | Actions |
| --- | --- | --- | --- |
| `service/account_egress.go` 的账号解析、仓储重读、OAuth session；`repository/claude_oauth_service.go`、各平台 OAuth/req 工厂 | 尚无账号时显式代理选择；账号绑定优先；会话创建后线路固定；仓储失败必须阻断 | `TestStrictEgressBrokenBindingsNeverBecomeDirect`、`TestClaudeEgressAccountProxyTakesPriorityOverManagedDefault`、`TestStrictEgressOAuthSessionCannotChangeProxy`、`TestStrictEgressRefreshLookupFailureSendsNoRequest` | egress-policy simulate；ops egress-audit |
| `repository/http_upstream.go` 的 `Do` / `DoWithTLS`、`resolveEgressForRequest`、重定向、客户端池 | 最后一次绑定读取；监控 route/revision 隔离、禁止重定向；TLS、SSE body 生命周期、审计和旧连接回收 | `TestStrictEgressHTTPAndSSEFailureNeverUsesDirectOrBackup`、`TestStrictEgressTransportChecksLatestBindingBeforeAnyDial`、`TestStrictEgress*`、`TestHTTPUpstream*`、`TestMonitor*` | egress-policy simulate；ops egress-audit；monitor backend |
| `service/openai_ws_pool.go` 的 acquire / dial / lease；passthrough adapter 与 WS client | 入队前和获得 lease 前分别校验；兼容键包含规范化线路；不可复用另一出口；每轮独立审计 | `TestStrictEgressWebSocketNeverDialsOnMissingBinding`、`TestStrictEgressWebSocketHandshakeFailureDoesNotRetryDirect`、`TestStrictEgressWebSocketPoolNeverReusesAnotherExit`、`TestStrictEgressWebSocketRechecksBindingBeforeReturningLease`、`TestEgressAuditWebSocketEveryTurnOnSameConnection` | egress-policy simulate；ops egress-audit |
| `service/openai_plugin_transport.go` | 插件绕过 HTTPUpstream；宿主仍要验证当前绑定并传递审计上下文 | `TestStrictEgressPluginChecksCurrentBindingBeforeDispatch`、`TestPluginRequestSentErrorDoesNotFailOver` | egress-policy simulate；backend unit |
| `service/account_egress_context.go`、`openai_images*`、`grok_media.go`、`seedance.go` 的后台任务 | 取消/脱离请求后仍沿最终选定账号线路；缺少捕获不能默认为直连 | `TestStrictEgressDetachedMediaRetainsFinalAccountRoute`、`TestStrictEgressBatchContextRejectsBrokenBinding` | egress-policy simulate；ops egress-audit |
| `repository/proxy_repo.go`、`proxy_expiry*`、`service/proxy_expiry_service.go` | 到期标记、账号快照失效、outbox 同一事务；续期刷新全部绑定；删除已绑定代理拒绝；旧 fallback 配置不能重绑 | `TestStrictEgressExpiryInvalidatesButNeverReassigns`、`TestStrictEgressDeleteSerializesAndRejectsBoundProxy`、`TestStrictEgressProxyExpiryChangesRefreshAllBindingsAtomically`、`TestProxyExpirySuite`、续期 integration suite | egress-policy simulate；monitor integration；backend integration |

逐调用位置见 [EGRESS-CALLS.md](EGRESS-CALLS.md)。该清单不以名字判断归属、不把同一策略的多阶段校验视作冗余。本轮优先保留这些现有独立实现；只有共同出口覆盖得到证据、且确实降低维护成本时才考虑删除调用。HTTPUpstream 的外层包装不能替代每跳、TLS、WS 或仓储事务。

## 其他定制

| 能力/现有行为 | 独立来源与必要共享接入 | 回归映射与 Actions | 可替换/删除条件 |
| --- | --- | --- | --- |
| 额度公式、有效采样、非负剩余值、不可用原因 | `service/quota_estimate.go`；`domain.QuotaEstimate`；GetUsage/GetUsageForAccount 增强调用 | `TestQuotaEstimateFormulaAndInvalidInputs`、`TestQuotaEstimateAllWindowsAndSampleCutoff`、`TestQuotaEstimateKeepsIndependentWindowSampleTimes`；usage + monitor + backend | 试点需证明减少依赖/共享逻辑；仅搬目录不扩大 |
| 每窗口采样截止、费用口径与历史容量；显式零才可恢复全部剩余 | quota_estimate 的本地统计/快照协调留在 service；repository 原子写入 | `TestQuotaEstimateHistoryDoesNotInventFullRemaining`、`TestQuotaEstimateOpenAIPartialObservationKeepsOtherWindowCutoff`、`TestQuotaEstimateSnapshotRoundTrip`；usage + monitor integration | 上游完全覆盖采样和历史语义才可删除，不能靠前端重算 |
| 普通 OpenAI 额度 API 优先、失败模型兜底；Spark 独立窗口；force/passive/受限读取不同策略 | `account_usage_service.go` 的 getOpenAIUsage 与 queryOpenAIUsageSnapshot；复用 OpenAIQuotaService 网络能力 | `TestOpenAIUsageRefresh_AllEntryPoints`、`TestOpenAIUsageRefresh_ModelFallbackSuccess`、`TestOpenAIUsageRefresh_SparkFallbackRejectsGlobalHeaders`、`TestOpenAIUsageRefresh_MissingPercentIsNotZero`、`TestScopedAdmin*`；usage + backend + upgrade cross | v0.2.22 仅后续候选；本轮不改变请求或额度消耗 |
| UI 等待可取消；共享刷新真实任务有期限、权限隔离；监控有独立路由缓存和执行槽 | account_usage 的缓存协调；`account_monitor_usage.go`；`channel_monitor_access.go`；`channel_monitor_quota_fetcher.go` | `TestOpenAIUsageRefresh_CallerCancellationDoesNotCancelSharedRefresh`、`TestOpenAIUsageRefresh_AlreadyCanceledCallerDoesNotStartRefresh`、`TestMonitorCanceledRunHoldsLockAndSlotUntilQuotaCompletes`、`TestMonitorQuotaFetchRetainsDeadlineAfterCallerCancellation`；usage race + monitor |
| 手动执行、用户可见范围、冷却、锁、权限撤销；V1/V2 门控 | `channel_monitor_access.go`、既有 handler/routes；共享 runChecksConcurrent / persistCheckResults | `TestMonitorVisibilityAndRevocation`、`TestMonitorExecutionLocksAndCooldown`、`TestMonitorMissingAccountAndUnauthorizedNeverReachQuota`、`TestChannelMonitor*`；monitor + backend | 不复制 provider 查询；执行和保存共用原链路 |
| 仅全局无 label 的 5h/7d 判断；耗尽 failed；暂停/关闭自动检测区分；手动结果有效 12h | `channel_monitor_quota_fetcher.go`、`channel_monitor_aggregator.go`、`channel_monitor_service.go` 状态适配 | `TestDeriveQuotaCheckResult_StatusMatrix`、`TestMonitorQuotaExhaustion`、`TestMonitorCurrentStatusAcrossViews`、`TestMonitorStatusReusesAccountAndHonorsJitter`；monitor + usage |
| 客户端配置字节内容、shell 风格、路径与 hint 保持 | `frontend/.../UseKeyModal.vue` 中的 Anthropic 模板；已有 UI 选择流程 | `UseKeyModal.spec.ts`；ops embedded-build；frontend-full | 提取纯模板可减少重复，组件继续控制展示 |
| 独立网络状态监控/认证/管理边界 | `ops/network-status`、network gateway、既有路由菜单 locale | network-status Python suite、`NetworkStatusView.spec.ts`；ops | 已有边界清晰，本轮不建立通用注册框架 |

## 数据及实现约束

API、JSON、数据库/迁移、Ent/Wire 公开结构保持兼容。规则仅依赖标准库/domain；账号读取、鉴权、费用、缓存、执行锁和持久化留在原 package 适配，不复制 Account/Proxy 结构。保留已有未提交升级计划。本轮只在 Actions 运行测试、生成和构建，逐批保存 source SHA 和真实执行用例；未完成检查不能视作通过。

## 实际接入归属（第 3 批）

- `custom_openai_usage_adapter.go` 收拢本地额度优先/模型兜底、权限范围缓存、singleflight 等待及错误/快照处理；`account_usage_service.go` 保留 `getOpenAIUsage`、上游窗口构建和费用查询，通过 `refreshOpenAIUsageSnapshot` 接入。原 helper 符号不改名，其他已存在调用无需改写。
- `custom_monitor_status_adapter.go` 收拢全局额度状态、关联账号调度状态、跨视图展示有效期；原 service/aggregator/fetcher 继续以同名函数调用。`loadMonitorAccount` 和状态覆盖仍在 service package，不导出内部模型，不创建 Account 副本类型。时间调用位置和优先级保持原样。
- 监控权限/锁/冷却/实际执行期限继续使用 `channel_monitor_access.go` 的现有独立实现；`runCheck` 的执行、保存及释放顺序不调整。出口 context、HTTP/TLS、WS、插件和仓储生命周期全部保留。
- 没有引入 `custom/quotaestimate` 常驻包，额度试点及停止依据见 PILOT.md；不改变前端原生 7d 公式或让前端补算缺失估算。
