# 双方共同修改文件复核

共同祖先 `dece26153621959ccd7635814f4b2d08304e681a`，定制基线 `56ef37468`，固定上游 `dab829a4e`。交集为 70 个文件。下表是源码复核记录，不能代替 Actions 编译、测试及真实生产验收。

| 文件 | 合并决定与核对点 |
| --- | --- |
| `.github/workflows/backend-ci.yml` | 冲突逐项保留 Node.js 22/pnpm 9 与既有隔离，保留上游新增检查。 |
| `.github/workflows/release.yml` | 冲突逐项保留 Node.js 22/pnpm 9 与既有隔离，保留上游新增检查。 |
| `.github/workflows/security-scan.yml` | 冲突逐项保留 Node.js 22/pnpm 9 与既有隔离，保留上游新增检查。 |
| `.gitignore` | 保留运维源码例外、生产采样忽略规则；不将本机审计目录带入交付。 |
| `README.md` | 保留 Node.js 22 环境要求和上游说明；历史部署时间不改。 |
| `README_CN.md` | 保留 Node.js 22 环境要求和上游说明；历史部署时间不改。 |
| `README_JA.md` | 保留 Node.js 22 环境要求和上游说明；历史部署时间不改。 |
| `backend/cmd/server/VERSION` | 改为 0.2.20；构建真实 SHA、时间、source 类型由 ops-ci 注入。 |
| `backend/cmd/server/wire_gen.go` | 核对新增 MCP 与定制代理/审计/监控 provider 参数；Ent/Wire 由 Actions 重生成并检查无差异。 |
| `backend/go.mod` | 合并后与上游安全修复兼容，x/net v0.62.0 保留；Wire 的额外 go.sum 项从 Actions 补丁提交。 |
| `backend/go.sum` | 合并后与上游安全修复兼容，x/net v0.62.0 保留；Wire 的额外 go.sum 项从 Actions 补丁提交。 |
| `backend/internal/config/config.go` | 保留 Claude 定向分流配置，加入上游资源限制、MCP 等配置；不降低全局默认预算。 |
| `backend/internal/handler/admin/account_handler.go` | 保留出口策略接口；受限密钥走上游默认拒绝目录。 |
| `backend/internal/handler/admin/openai_oauth_handler.go` | 逐块合并代理权限/有效性与严格解析；查询失败不退回直连。 |
| `backend/internal/handler/image_task_handler.go` | 保留异步任务出口上下文捕获和完成存储；任务上下文清理沿用上游。 |
| `backend/internal/repository/account_repo.go` | 保留估算快照为调度中性键；其余账号更新与上游受限操作保留。 |
| `backend/internal/repository/http_upstream.go` | 逐块合并当前绑定校验、监控禁止重定向/回退、Claude 跳转校验、审计、每次尝试限时读取和一次性 Close/inFlight 释放。 |
| `backend/internal/repository/wire.go` | 核对新增 MCP 与定制代理/审计/监控 provider 参数；Ent/Wire 由 Actions 重生成并检查无差异。 |
| `backend/internal/server/middleware/api_key_auth.go` | 逐处保留 ResolveAccountProxyURL 错误传播，核对返回签名；上游请求格式、资源限制、取消/限时读取和 defer 释放保持接通。 |
| `backend/internal/server/middleware/api_key_auth_google.go` | 逐处保留 ResolveAccountProxyURL 错误传播，核对返回签名；上游请求格式、资源限制、取消/限时读取和 defer 释放保持接通。 |
| `backend/internal/server/routes/admin.go` | 保留出口策略与监控接口，新增上游权限/MCP/黑名单；定制接口不加入受限权限允许目录。 |
| `backend/internal/server/routes/custom_admin_key_access_test.go` | 保留默认拒绝及资源范围测试；上游明确排除的接口与定制出口接口核对注册路径。 |
| `backend/internal/server/routes/user.go` | 保留监控手动检测及权限路径，上游 MCP 用户功能不删减。 |
| `backend/internal/service/account_test_service.go` | 逐处核对普通请求、WS、图像与 Grok 多步骤严格代理解析的返回签名和释放；显式模型测试继续读取响应头。 |
| `backend/internal/service/account_usage_service.go` | 保留 usage API 优先/独立采样/估算/取消隔离；权限前置、受限 flight 分离且禁止模型回退；保留上游 SetupToken 周额度与其他平台额度分组。 |
| `backend/internal/service/antigravity_gateway_compat.go` | 严格代理 helper 返回 (string,error)，prepare 阶段先拒绝；兼容调用方签名与上游取消/重试。 |
| `backend/internal/service/antigravity_gateway_gemini.go` | 逐处保留 ResolveAccountProxyURL 错误传播，核对返回签名；上游请求格式、资源限制、取消/限时读取和 defer 释放保持接通。 |
| `backend/internal/service/antigravity_gateway_service.go` | 逐处保留 ResolveAccountProxyURL 错误传播，核对返回签名；上游请求格式、资源限制、取消/限时读取和 defer 释放保持接通。 |
| `backend/internal/service/antigravity_gateway_upstream.go` | 逐处保留 ResolveAccountProxyURL 错误传播，核对返回签名；上游请求格式、资源限制、取消/限时读取和 defer 释放保持接通。 |
| `backend/internal/service/antigravity_quota_fetcher.go` | 保留监控绑定与禁止多余查询；GetProxyURL 的 (string,error) 调用同步，保留新增额度分组。 |
| `backend/internal/service/gateway_anthropic_passthrough.go` | 逐处保留 ResolveAccountProxyURL 错误传播，核对返回签名；上游请求格式、资源限制、取消/限时读取和 defer 释放保持接通。 |
| `backend/internal/service/gateway_forward_as_chat_completions.go` | 逐处保留 ResolveAccountProxyURL 错误传播，核对返回签名；上游请求格式、资源限制、取消/限时读取和 defer 释放保持接通。 |
| `backend/internal/service/gateway_forward_as_responses.go` | 逐处保留 ResolveAccountProxyURL 错误传播，核对返回签名；上游请求格式、资源限制、取消/限时读取和 defer 释放保持接通。 |
| `backend/internal/service/gateway_upstream_request.go` | 自定义 relay URL 不泄漏代理凭据，保留上游请求上下文与限时读取机制。 |
| `backend/internal/service/gemini_chat_completions_compat_service.go` | 逐处保留 ResolveAccountProxyURL 错误传播，核对返回签名；上游请求格式、资源限制、取消/限时读取和 defer 释放保持接通。 |
| `backend/internal/service/gemini_messages_compat_service.go` | 逐处保留 ResolveAccountProxyURL 错误传播，核对返回签名；上游请求格式、资源限制、取消/限时读取和 defer 释放保持接通。 |
| `backend/internal/service/grok_quota_service.go` | 复制账号后加载当前代理，失败即拒绝；保留上游 scoped 权限和 token 获取。 |
| `backend/internal/service/openai_apikey_responses_probe.go` | 逐处保留 ResolveAccountProxyURL 错误传播，核对返回签名；上游请求格式、资源限制、取消/限时读取和 defer 释放保持接通。 |
| `backend/internal/service/openai_codex_models_service.go` | 模型目录请求保留严格解析和插件审计上下文；新增模型与目录行为沿用上游。 |
| `backend/internal/service/openai_gateway_cc_pipeline.go` | 逐处保留 ResolveAccountProxyURL 错误传播，核对返回签名；上游请求格式、资源限制、取消/限时读取和 defer 释放保持接通。 |
| `backend/internal/service/openai_gateway_chat_completions.go` | 逐处保留 ResolveAccountProxyURL 错误传播，核对返回签名；上游请求格式、资源限制、取消/限时读取和 defer 释放保持接通。 |
| `backend/internal/service/openai_gateway_chat_completions_anthropic_native.go` | 逐处保留 ResolveAccountProxyURL 错误传播，核对返回签名；上游请求格式、资源限制、取消/限时读取和 defer 释放保持接通。 |
| `backend/internal/service/openai_gateway_forward.go` | 严格代理失败时仍关闭 headerGuard；重试沿用上游 attempt 生命周期。 |
| `backend/internal/service/openai_gateway_grok.go` | 逐处保留 ResolveAccountProxyURL 错误传播，核对返回签名；上游请求格式、资源限制、取消/限时读取和 defer 释放保持接通。 |
| `backend/internal/service/openai_gateway_messages.go` | 逐处保留 ResolveAccountProxyURL 错误传播，核对返回签名；上游请求格式、资源限制、取消/限时读取和 defer 释放保持接通。 |
| `backend/internal/service/openai_gateway_messages_anthropic_native.go` | 逐处保留 ResolveAccountProxyURL 错误传播，核对返回签名；上游请求格式、资源限制、取消/限时读取和 defer 释放保持接通。 |
| `backend/internal/service/openai_gateway_passthrough.go` | 逐处保留 ResolveAccountProxyURL 错误传播，核对返回签名；上游请求格式、资源限制、取消/限时读取和 defer 释放保持接通。 |
| `backend/internal/service/openai_gateway_responses_anthropic_native.go` | 逐处保留 ResolveAccountProxyURL 错误传播，核对返回签名；上游请求格式、资源限制、取消/限时读取和 defer 释放保持接通。 |
| `backend/internal/service/openai_gateway_usage.go` | 保留每窗口采样时刻和完整性记录，保持 Spark 与全局池隔离。 |
| `backend/internal/service/openai_images.go` | 逐处保留 ResolveAccountProxyURL 错误传播，核对返回签名；上游请求格式、资源限制、取消/限时读取和 defer 释放保持接通。 |
| `backend/internal/service/openai_images_responses.go` | 逐处保留 ResolveAccountProxyURL 错误传播，核对返回签名；上游请求格式、资源限制、取消/限时读取和 defer 释放保持接通。 |
| `backend/internal/service/openai_quota_service.go` | 修复自动合并旧返回签名；quota 请求保留当前绑定、审计、usage-only；受限/监控禁止模型回退和隐式重置。 |
| `backend/internal/service/openai_upstream_transport_error_handle_test.go` | 代理 fixture 具有 active 状态以匹配严格解析；保留传输失败归因断言。 |
| `backend/internal/service/openai_ws_http_bridge.go` | 逐处保留 ResolveAccountProxyURL 错误传播，核对返回签名；上游请求格式、资源限制、取消/限时读取和 defer 释放保持接通。 |
| `backend/internal/service/ratelimit_service.go` | 保留独立窗口时间与 Spark 隔离；上游限流及 Anthropic 周额度采样继续生效。 |
| `backend/internal/service/wire.go` | 核对新增 MCP 与定制代理/审计/监控 provider 参数；Ent/Wire 由 Actions 重生成并检查无差异。 |
| `deploy/config.example.yaml` | 只加定制 Claude 分流默认关闭；资源默认值保留上游，128 MiB 候选写在手动部署说明。 |
| `frontend/package.json` | 逐块采用 vue-i18n/message-compiler 11.4.16，保留固定安全 overrides；锁文件由 Actions 复核。 |
| `frontend/pnpm-lock.yaml` | 逐块采用 vue-i18n/message-compiler 11.4.16，保留固定安全 overrides；锁文件由 Actions 复核。 |
| `frontend/src/components/account/AccountUsageCell.vue` | 保留失败历史快照/时间/未知配额和 inactive 状态，兼容上游新增平台及窗口。 |
| `frontend/src/components/account/CreateAccountModal.vue` | 保留 AccountProxyField 严格代理提示与上游字段/权限控制，配置接口采用默认拒绝。 |
| `frontend/src/components/account/EditAccountModal.vue` | 保留 AccountProxyField 严格代理提示与上游字段/权限控制，配置接口采用默认拒绝。 |
| `frontend/src/components/account/__tests__/AccountUsageCell.spec.ts` | 保留历史快照与 inactive 回归，接入上游平台和额度测试。 |
| `frontend/src/components/keys/UseKeyModal.vue` | 普通/WS 默认 gpt-6.1-sol、搜索/图像采用上游；保留 supports_websockets、远程目录和 Claude Code 模板。 |
| `frontend/src/components/keys/__tests__/UseKeyModal.spec.ts` | 逐块合并默认模型与搜索/图像断言；保留定制远程目录/传输/平台导出覆盖。 |
| `frontend/src/i18n/locales/en/admin/accounts.ts` | 保留中英配额历史/未知、inactive、监控授权和手动运行文案；同时纳入上游新键，远程验证完整性。 |
| `frontend/src/i18n/locales/en/dashboard.ts` | 保留中英配额历史/未知、inactive、监控授权和手动运行文案；同时纳入上游新键，远程验证完整性。 |
| `frontend/src/i18n/locales/zh/admin/accounts.ts` | 保留中英配额历史/未知、inactive、监控授权和手动运行文案；同时纳入上游新键，远程验证完整性。 |
| `frontend/src/i18n/locales/zh/dashboard.ts` | 保留中英配额历史/未知、inactive、监控授权和手动运行文案；同时纳入上游新键，远程验证完整性。 |
| `frontend/src/types/index.ts` | 保留窗口采样、未激活和估算字段，同时保留上游 MCP/管理权限与新增 API 类型。 |
