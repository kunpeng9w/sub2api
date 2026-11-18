# v0.2.22 文本演练与语义审查边界

固定上游基线、起点、目标与完整统计由同目录 report.py 及 Actions 的 customization-report artifact 给出。两次演练使用同一逐文件 git merge-file -p 方法，不修改索引、工作区或提交祖先。当前结果为 65 个共同修改路径、8 个冲突文件；未声称减少冲突。

对自动合并后包含出口调用的 27 个路径做静态调用比对。25 个调用行完全相同；`gateway_forward_as_chat_completions.go` 和 `gateway_forward_as_responses.go` 中仅 `resp, err :=` 变为 `resp, err =`，绑定解析及 DoWithTLS 参数仍保留。这个证据只能证明已登记的调用文本未被删除，不能证明所有请求路径/参数和运行时行为兼容。

本轮移动到适配器的 queryOpenAIUsageSnapshot、缓存协调和 monitorDisplayStatus/monitorAccountStatus/monitorQuotaWindowStatus 仍在同一个 service package。没有新包装层遮挡出口错误，没有向最终分支合入 v0.2.22。

以下逐路径登记目标差异的审查结论/后续升级检查；“自动”只表示文本结果。测试、生成、接口签名及可观察行为的升级验证仍需要独立完成。

| 共同路径 | 文本结果 | 静态语义审查/升级待办 |
| --- | --- | --- |
| `.github/workflows/backend-ci.yml` | 自动 | 构建/依赖/配置；Makefile 需人工解冲突；本轮专项 package/筛选/生成列表必须保留，不能拿目标 CI 替换 |
| `.github/workflows/release.yml` | 自动 | 构建/依赖/配置；Makefile 需人工解冲突；本轮专项 package/筛选/生成列表必须保留，不能拿目标 CI 替换 |
| `.github/workflows/security-scan.yml` | 自动 | 构建/依赖/配置；Makefile 需人工解冲突；本轮专项 package/筛选/生成列表必须保留，不能拿目标 CI 替换 |
| `Dockerfile` | 自动 | 构建/依赖/配置；Makefile 需人工解冲突；本轮专项 package/筛选/生成列表必须保留，不能拿目标 CI 替换 |
| `Makefile` | 冲突 | 构建/依赖/配置；Makefile 需人工解冲突；本轮专项 package/筛选/生成列表必须保留，不能拿目标 CI 替换 |
| `README.md` | 自动 | 文档或版本元数据；目标版本只作为演练输入，本轮不更新发布版本 |
| `README_CN.md` | 自动 | 文档或版本元数据；目标版本只作为演练输入，本轮不更新发布版本 |
| `README_JA.md` | 自动 | 文档或版本元数据；目标版本只作为演练输入，本轮不更新发布版本 |
| `backend/cmd/server/VERSION` | 自动 | 文档或版本元数据；目标版本只作为演练输入，本轮不更新发布版本 |
| `backend/cmd/server/wire_gen.go` | 自动 | 生成/装配；升级新增 billing lane 依赖，必须由目标源码重新生成并审查，不能手工接受生成文本 |
| `backend/ent/migrate/schema.go` | 自动 | 生成/装配；升级新增 billing lane 依赖，必须由目标源码重新生成并审查，不能手工接受生成文本 |
| `backend/ent/mutation.go` | 自动 | 生成/装配；升级新增 billing lane 依赖，必须由目标源码重新生成并审查，不能手工接受生成文本 |
| `backend/ent/runtime/runtime.go` | 自动 | 生成/装配；升级新增 billing lane 依赖，必须由目标源码重新生成并审查，不能手工接受生成文本 |
| `backend/go.mod` | 自动 | 构建/依赖/配置；Makefile 需人工解冲突；本轮专项 package/筛选/生成列表必须保留，不能拿目标 CI 替换 |
| `backend/internal/config/config.go` | 自动 | 配置校验允许 Concurrency.PingInterval=0；核对禁用等待 SSE ping 的消费端行为及负值拒绝。 |
| `backend/internal/repository/account_repo.go` | 自动 | 上游改变 last-used 锁顺序、新增单 scope 解封及 session start；仓储事务/outbox/快照语义须独立核验 |
| `backend/internal/repository/http_upstream.go` | 自动 | 上游更改 dialer keepalive/control；本地最终绑定检查、重定向、TLS/监控派生与审计均须保留；本轮不合入 |
| `backend/internal/repository/usage_log_repo_integration_test.go` | 自动 | 测试/fixture；升级时保留原定制行为断言，检查新增方法签名和 mock，不以自动合并等价通过 |
| `backend/internal/service/account_test_service.go` | 自动 | Vertex URL 构造增加参数；核对新签名、区域选择及账号测试的严格出口。 |
| `backend/internal/service/account_usage_service.go` | 冲突 | 冲突；保留本地额度优先/受限隔离/模型兜底/采样时间；limits[] 与刷新失败的新语义留待独立升级决策 |
| `backend/internal/service/account_usage_service_test.go` | 冲突 | 冲突；保留本地额度优先/受限隔离/模型兜底/采样时间；limits[] 与刷新失败的新语义留待独立升级决策 |
| `backend/internal/service/antigravity_gateway_compat.go` | 自动 | 已读目标差异及出口调用；请求/流式/模型元数据/错误或计费有变化，保留账号出口和请求后重播边界，升级另行回归 |
| `backend/internal/service/batch_image_provider_vertex.go` | 自动 | 目标改变 Vertex 区域/URL/参数或兼容输出；路由钉住不随目标域名切换，新增签名调用一并核验 |
| `backend/internal/service/channel_monitor_quota_fetcher.go` | 自动 | 核对移出的 monitorQuotaWindowStatus 仍由同 package 提供；utilization_unknown、刷新失败豁免不能自动采用 |
| `backend/internal/service/channel_monitor_quota_fetcher_test.go` | 冲突 | 核对移出的 monitorQuotaWindowStatus 仍由同 package 提供；utilization_unknown、刷新失败豁免不能自动采用 |
| `backend/internal/service/cn_provider_quota_service.go` | 自动 | 新增 Kimi 月度和缺席清理/数值校验；出口调用保留，窗口/缺失与显式零语义需独立验收 |
| `backend/internal/service/gateway_anthropic_passthrough.go` | 自动 | 已读目标差异及出口调用；请求/流式/模型元数据/错误或计费有变化，保留账号出口和请求后重播边界，升级另行回归 |
| `backend/internal/service/gateway_bedrock.go` | 自动 | 已读目标差异及出口调用；请求/流式/模型元数据/错误或计费有变化，保留账号出口和请求后重播边界，升级另行回归 |
| `backend/internal/service/gateway_count_tokens.go` | 自动 | 已读目标差异及出口调用；请求/流式/模型元数据/错误或计费有变化，保留账号出口和请求后重播边界，升级另行回归 |
| `backend/internal/service/gateway_forward.go` | 自动 | 已读目标差异及出口调用；请求/流式/模型元数据/错误或计费有变化，保留账号出口和请求后重播边界，升级另行回归 |
| `backend/internal/service/gateway_forward_as_chat_completions.go` | 自动 | 已读目标差异及出口调用；请求/流式/模型元数据/错误或计费有变化，保留账号出口和请求后重播边界，升级另行回归 |
| `backend/internal/service/gateway_forward_as_responses.go` | 自动 | 已读目标差异及出口调用；请求/流式/模型元数据/错误或计费有变化，保留账号出口和请求后重播边界，升级另行回归 |
| `backend/internal/service/gateway_service.go` | 冲突 | 已读目标差异及出口调用；请求/流式/模型元数据/错误或计费有变化，保留账号出口和请求后重播边界，升级另行回归 |
| `backend/internal/service/gateway_upstream_request.go` | 自动 | 已读目标差异及出口调用；请求/流式/模型元数据/错误或计费有变化，保留账号出口和请求后重播边界，升级另行回归 |
| `backend/internal/service/gateway_websearch_emulation.go` | 自动 | 已读目标差异及出口调用；请求/流式/模型元数据/错误或计费有变化，保留账号出口和请求后重播边界，升级另行回归 |
| `backend/internal/service/gemini_cached_content.go` | 自动 | 目标改变 Vertex 区域/URL/参数或兼容输出；路由钉住不随目标域名切换，新增签名调用一并核验 |
| `backend/internal/service/gemini_chat_completions_compat_service.go` | 自动 | 目标改变 Vertex 区域/URL/参数或兼容输出；路由钉住不随目标域名切换，新增签名调用一并核验 |
| `backend/internal/service/gemini_messages_compat_service.go` | 自动 | 目标改变 Vertex 区域/URL/参数或兼容输出；路由钉住不随目标域名切换，新增签名调用一并核验 |
| `backend/internal/service/gemini_models_endpoints.go` | 自动 | 目标改变 Vertex 区域/URL/参数或兼容输出；路由钉住不随目标域名切换，新增签名调用一并核验 |
| `backend/internal/service/grok_media.go` | 自动 | 已读目标差异及出口调用；请求/流式/模型元数据/错误或计费有变化，保留账号出口和请求后重播边界，升级另行回归 |
| `backend/internal/service/openai_gateway_chat_completions.go` | 自动 | 已读目标差异及出口调用；请求/流式/模型元数据/错误或计费有变化，保留账号出口和请求后重播边界，升级另行回归 |
| `backend/internal/service/openai_gateway_chat_completions_anthropic_native.go` | 自动 | 已读目标差异及出口调用；请求/流式/模型元数据/错误或计费有变化，保留账号出口和请求后重播边界，升级另行回归 |
| `backend/internal/service/openai_gateway_forward.go` | 自动 | 已读目标差异及出口调用；请求/流式/模型元数据/错误或计费有变化，保留账号出口和请求后重播边界，升级另行回归 |
| `backend/internal/service/openai_gateway_messages.go` | 自动 | 已读目标差异及出口调用；请求/流式/模型元数据/错误或计费有变化，保留账号出口和请求后重播边界，升级另行回归 |
| `backend/internal/service/openai_gateway_messages_anthropic_native.go` | 自动 | 已读目标差异及出口调用；请求/流式/模型元数据/错误或计费有变化，保留账号出口和请求后重播边界，升级另行回归 |
| `backend/internal/service/openai_gateway_passthrough.go` | 自动 | 已读目标差异及出口调用；请求/流式/模型元数据/错误或计费有变化，保留账号出口和请求后重播边界，升级另行回归 |
| `backend/internal/service/openai_gateway_responses_anthropic_native.go` | 自动 | 已读目标差异及出口调用；请求/流式/模型元数据/错误或计费有变化，保留账号出口和请求后重播边界，升级另行回归 |
| `backend/internal/service/openai_gateway_usage.go` | 自动 | 已读目标差异及出口调用；请求/流式/模型元数据/错误或计费有变化，保留账号出口和请求后重播边界，升级另行回归 |
| `backend/internal/service/openai_models_list.go` | 自动 | passthrough 模型过滤参与列表投影；核对列表可见性与实际请求模型准入一致。 |
| `backend/internal/service/openai_ws_forwarder_ingress.go` | 自动 | 注入 codexClientSessionID(c)；核对 WS 客户端会话身份、账号隔离及出口绑定。 |
| `backend/internal/service/openai_ws_v2_passthrough_adapter.go` | 自动 | 首帧和后续帧注入客户端 session ID；核对 response.create/session.update 身份一致性及账号隔离。 |
| `backend/internal/service/ratelimit_service.go` | 自动 | 上游新增 Fable 恢复/周采样保留和 Bedrock 处理；不得改变本轮过期、显式零与监控判定 |
| `backend/internal/service/upstream_models.go` | 自动 | 目标改变 Vertex 区域/URL/参数或兼容输出；路由钉住不随目标域名切换，新增签名调用一并核验 |
| `backend/internal/service/vertex_service_account.go` | 自动 | 目标改变 Vertex 区域/URL/参数或兼容输出；路由钉住不随目标域名切换，新增签名调用一并核验 |
| `backend/internal/service/vertex_service_account_test.go` | 自动 | 目标改变 Vertex 区域/URL/参数或兼容输出；路由钉住不随目标域名切换，新增签名调用一并核验 |
| `backend/internal/service/wire.go` | 自动 | 生成/装配；升级新增 billing lane 依赖，必须由目标源码重新生成并审查，不能手工接受生成文本 |
| `deploy/config.example.yaml` | 自动 | 构建/依赖/配置；Makefile 需人工解冲突；本轮专项 package/筛选/生成列表必须保留，不能拿目标 CI 替换 |
| `frontend/src/components/account/AccountUsageCell.vue` | 冲突 | 账号用量展示/未知窗口存在冲突；本轮保留既有原生 7d 与增强估算边界，不用前端重算掩盖缺失 |
| `frontend/src/components/account/CreateAccountModal.vue` | 自动 | 目标增加组/用量字段及编辑展示；保留本地 sampled_at/estimate/window_inactive 和管理边界，升级类型/组件完整回归 |
| `frontend/src/components/account/EditAccountModal.vue` | 自动 | 目标增加组/用量字段及编辑展示；保留本地 sampled_at/estimate/window_inactive 和管理边界，升级类型/组件完整回归 |
| `frontend/src/components/account/UsageProgressBar.vue` | 冲突 | 账号用量展示/未知窗口存在冲突；本轮保留既有原生 7d 与增强估算边界，不用前端重算掩盖缺失 |
| `frontend/src/components/account/__tests__/AccountUsageCell.spec.ts` | 冲突 | 账号用量展示/未知窗口存在冲突；本轮保留既有原生 7d 与增强估算边界，不用前端重算掩盖缺失 |
| `frontend/src/i18n/locales/en/admin/accounts.ts` | 自动 | 目标增加组/用量字段及编辑展示；保留本地 sampled_at/estimate/window_inactive 和管理边界，升级类型/组件完整回归 |
| `frontend/src/i18n/locales/zh/admin/accounts.ts` | 自动 | 目标增加组/用量字段及编辑展示；保留本地 sampled_at/estimate/window_inactive 和管理边界，升级类型/组件完整回归 |
| `frontend/src/types/index.ts` | 自动 | 目标增加组/用量字段及编辑展示；保留本地 sampled_at/estimate/window_inactive 和管理边界，升级类型/组件完整回归 |

2026-11-17 升级准备补正：依 IMPLEMENTATION-REVIEW.md 修正上述五处生产代码分类。原重构验收结果保持历史记录；这些行为纳入 v0.2.22 升级验收。
