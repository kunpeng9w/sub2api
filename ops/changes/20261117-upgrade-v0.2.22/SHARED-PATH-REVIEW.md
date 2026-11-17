# 共同修改路径审查

按固定共同祖先分别计算起点/目标 changed paths 交集，实际仍为 65 个；普通 merge 8 个文件冲突。自动合并只是文本结果，以下登记静态行为核对；运行时结论只来自最终 SHA 的 Actions。额外新增的监控/权限/出口文件也保留在升级源码及完整回归中。

| 路径 | 文本结果 | 复核与保留接入 |
| --- | --- | --- |
| `.github/workflows/backend-ci.yml` | 自动合并 | 采用上游依赖/构建调整；保留本地专项测试和构建来源；七工作流触发及必选筛选单独复核。 |
| `.github/workflows/release.yml` | 自动合并 | 采用上游依赖/构建调整；保留本地专项测试和构建来源；七工作流触发及必选筛选单独复核。 |
| `.github/workflows/security-scan.yml` | 自动合并 | 采用上游依赖/构建调整；保留本地专项测试和构建来源；七工作流触发及必选筛选单独复核。 |
| `Dockerfile` | 自动合并 | 采用上游依赖/构建调整；保留本地专项测试和构建来源；七工作流触发及必选筛选单独复核。 |
| `Makefile` | 人工冲突 | 采用上游依赖/构建调整；保留本地专项测试和构建来源；七工作流触发及必选筛选单独复核。 |
| `README.md` | 自动合并 | 采用上游功能文档，本轮发布版本由 VERSION 显式设为 0.2.22。 |
| `README_CN.md` | 自动合并 | 采用上游功能文档，本轮发布版本由 VERSION 显式设为 0.2.22。 |
| `README_JA.md` | 自动合并 | 采用上游功能文档，本轮发布版本由 VERSION 显式设为 0.2.22。 |
| `backend/cmd/server/VERSION` | 自动合并 | 目标标签中值仍为 0.2.21，按交付要求显式设为 0.2.22。 |
| `backend/cmd/server/wire_gen.go` | 自动合并 | 生成文本逐项对照；必须由 Actions Ent/Wire 重生成验收，最终补丁为空。 |
| `backend/ent/migrate/schema.go` | 自动合并 | 生成文本逐项对照；必须由 Actions Ent/Wire 重生成验收，最终补丁为空。 |
| `backend/ent/mutation.go` | 自动合并 | 生成文本逐项对照；必须由 Actions Ent/Wire 重生成验收，最终补丁为空。 |
| `backend/ent/runtime/runtime.go` | 自动合并 | 生成文本逐项对照；必须由 Actions Ent/Wire 重生成验收，最终补丁为空。 |
| `backend/go.mod` | 自动合并 | 采用上游依赖/构建调整；保留本地专项测试和构建来源；七工作流触发及必选筛选单独复核。 |
| `backend/internal/config/config.go` | 自动合并 | 0 禁用等待 ping；5–30 有效、负值拒绝；配置用例沿用上游。 |
| `backend/internal/repository/account_repo.go` | 自动合并 | last-used 升序 NO KEY UPDATE 锁；ClearModelRateLimit 保留本地事务 scope、outbox 与调度快照；session start/end 同步。 |
| `backend/internal/repository/http_upstream.go` | 自动合并 | 采用 TCP keepalive/control；原最终绑定检查、请求后禁止重播、重定向与 TLS 审计保留。 |
| `backend/internal/repository/usage_log_repo_integration_test.go` | 自动合并 | 新增官方窗口/汇总行为；原 monitor WindowStats 和采样区间用例保留。 |
| `backend/internal/service/account_test_service.go` | 自动合并 | Vertex URL 新签名传空 cachedContentLocation；原账号测试出口不变。 |
| `backend/internal/service/account_usage_service.go` | 人工冲突 | 普通模型探测/受限与 Spark API 分支；未知字段、source、页面过期归零；独立监控与采样接入见 IMPLEMENTATION。Fable limits[]/原窗口时间保留。 |
| `backend/internal/service/account_usage_service_test.go` | 人工冲突 | 保留上游快照时间/失败/未知/过期归零；旧不依赖 WSv2 的普通刷新断言改为上游门控，独立监控另测。 |
| `backend/internal/service/antigravity_gateway_compat.go` | 自动合并 | 兼容转换增加协议内容处理；原账号出口与失败请求边界不变。 |
| `backend/internal/service/batch_image_provider_vertex.go` | 自动合并 | 跟进 Vertex 构造签名与模型区域；原账号出口/批量上下文接入保留。 |
| `backend/internal/service/channel_monitor_quota_fetcher.go` | 自动合并 | 真实独立监控入口；跳过 utilization_unknown；鉴权优先，一般刷新失败不豁免。 |
| `backend/internal/service/channel_monitor_quota_fetcher_test.go` | 人工冲突 | 保留耗尽 failed/全局窗口；上游 DoesNotFlap 豁免改由已确认的未知/失败矩阵覆盖。 |
| `backend/internal/service/cn_provider_quota_service.go` | 自动合并 | Kimi 月度窗口、缺席清理、非数字拒绝；原账号出口保留。 |
| `backend/internal/service/gateway_anthropic_passthrough.go` | 自动合并 | 恢复复合模型名；SSE 转换优化；账号出口/已断开后 drain 计费保留。 |
| `backend/internal/service/gateway_bedrock.go` | 自动合并 | 内容过滤不换号、流中断保留 usage、调度模型键；原严格请求出口保留。 |
| `backend/internal/service/gateway_count_tokens.go` | 自动合并 | 原始 header 大小写处理、延迟诊断；DoWithTLS 与绑定上下文保留。 |
| `backend/internal/service/gateway_forward.go` | 自动合并 | 仅重载确已轮换的 OAuth token 重试一次；每次仍经过同一严格出口。 |
| `backend/internal/service/gateway_forward_as_chat_completions.go` | 自动合并 | 轮换 token 重试、用户元数据与响应模型/usage 观测；请求重建仍用绑定账号出口。 |
| `backend/internal/service/gateway_forward_as_responses.go` | 自动合并 | 轮换 token 重试及响应模型/usage 观测；请求重建仍用绑定账号出口。 |
| `backend/internal/service/gateway_service.go` | 人工冲突 | 同时保留本地 Cause/Unwrap 和上游 StreamFailedFrame；模型目录支持 passthrough filter；延迟诊断。 |
| `backend/internal/service/gateway_upstream_request.go` | 自动合并 | 延迟诊断与 toolset beta 处理；账号 header 覆盖和出口接入保留。 |
| `backend/internal/service/gateway_websearch_emulation.go` | 自动合并 | JSON 遍历优化；搜索请求原账号出口保留。 |
| `backend/internal/service/gemini_cached_content.go` | 自动合并 | 从显式缓存引用提取区域，统一 vertexAPIHost；账号绑定不随区域改变。 |
| `backend/internal/service/gemini_chat_completions_compat_service.go` | 自动合并 | Vertex 新签名、模型成本倍率、同名工具多调用边界；严格账号出口不变。 |
| `backend/internal/service/gemini_messages_compat_service.go` | 自动合并 | Vertex 缓存区域与成本倍率贯穿 native；工具调用分段；所有原 DoWithTLS 调用保留。 |
| `backend/internal/service/gemini_models_endpoints.go` | 自动合并 | 多区域模型查询用 global 清单；生成区域策略仍独立；原出口不变。 |
| `backend/internal/service/grok_media.go` | 自动合并 | Grok 错误消息/code 和透传规则变化；原媒体失败与审计/账号就绪状态保留。 |
| `backend/internal/service/openai_gateway_chat_completions.go` | 自动合并 | 注入客户端 session ID、Grok 搜索 usage、流终止副作用；原出口保留。 |
| `backend/internal/service/openai_gateway_chat_completions_anthropic_native.go` | 自动合并 | 记录真实 /v1/messages endpoint、用户元数据；原严格出口保留。 |
| `backend/internal/service/openai_gateway_forward.go` | 自动合并 | 客户端 session ID 与强制 Ultrafast tier；原请求准入、身份命名空间、账号出口保留。 |
| `backend/internal/service/openai_gateway_messages.go` | 自动合并 | 客户端 session ID、复合模型名、Grok 搜索 usage；首内容前暂存 prelude，保留 ping/失败转移与 usage drain。 |
| `backend/internal/service/openai_gateway_messages_anthropic_native.go` | 自动合并 | 补实际端点与响应模型计费元数据；原流 drain 和出口保留。 |
| `backend/internal/service/openai_gateway_passthrough.go` | 自动合并 | 客户端 session ID，失败帧在耗尽 failover 后仍可透传；本地 Cause 不丢失。 |
| `backend/internal/service/openai_gateway_responses_anthropic_native.go` | 自动合并 | 记录真实 /v1/messages endpoint；本地绑定/转换工具调用接入保留。 |
| `backend/internal/service/openai_gateway_usage.go` | 自动合并 | 保留窗口级 sample；新使用率缺 reset 时写 nil；不让单窗口更新刷新另一窗口时间。 |
| `backend/internal/service/openai_models_list.go` | 自动合并 | 默认 passthrough 不收窄清单；启用过滤时与 IsModelSupported 一致，不改公共模型名。 |
| `backend/internal/service/openai_ws_forwarder_ingress.go` | 自动合并 | 客户端 session ID 传入账号命名空间；出口与账号隔离不变。 |
| `backend/internal/service/openai_ws_v2_passthrough_adapter.go` | 自动合并 | 首帧、response.create、session.update 均传 session ID；原租约/绑定复查不变。 |
| `backend/internal/service/ratelimit_service.go` | 自动合并 | Fable 仅真实观测恢复，单 scope 清除；周窗口按自己 reset 清理；保留窗口采样与 inactive。 |
| `backend/internal/service/upstream_models.go` | 自动合并 | 跟进模型集合与上游映射；不绕过本地目录/权限及账号出口。 |
| `backend/internal/service/vertex_service_account.go` | 自动合并 | 多区域回退、显式缓存区域及统一 host；不把目标域名当代理选择条件。 |
| `backend/internal/service/vertex_service_account_test.go` | 自动合并 | 保留原严格出站 fixture，加入上游多区域/显式缓存签名与 URL 用例。 |
| `backend/internal/service/wire.go` | 自动合并 | 装配 UsageBillingLaneCombiner；底层事务仍由仓储实现，Shutdown 等待 lanes；本地服务装配保留。 |
| `deploy/config.example.yaml` | 自动合并 | 0 禁用 ping 文案及 DeepSeek 显式价格规则；不改生产配置。 |
| `frontend/src/components/account/AccountUsageCell.vue` | 人工冲突 | 上游未知/错误 UI、独立窗口时间；普通/主动/身份切换代次保护；保留定制估算/inactive。 |
| `frontend/src/components/account/CreateAccountModal.vue` | 自动合并 | 上游 passthrough filter 开关、准入限制与存储一致；保留代理选择。 |
| `frontend/src/components/account/EditAccountModal.vue` | 自动合并 | filter 回填、model_mapping 开关与保存一致；保留代理选择。 |
| `frontend/src/components/account/UsageProgressBar.vue` | 人工冲突 | utilization_unknown 优先；保留 inactive、remainingCapacity、定制宽度与原计费统计。 |
| `frontend/src/components/account/__tests__/AccountUsageCell.spec.ts` | 人工冲突 | 保留多平台及估算、未知/失败提示；时间断言迁移到窗口 sampled_at，异步新增独立 spec。 |
| `frontend/src/i18n/locales/en/admin/accounts.ts` | 自动合并 | 采用上游未知/刷新失败/过滤文案，保留原定制 key 兼容；i18n 完整性远程检查。 |
| `frontend/src/i18n/locales/zh/admin/accounts.ts` | 自动合并 | 采用上游未知/刷新失败/过滤文案，保留原定制 key 兼容；i18n 完整性远程检查。 |
| `frontend/src/types/index.ts` | 自动合并 | UtilizationUnknown 与既有 SampledAt/Estimate/inactive 同时保留；API 字段不删除。 |
