# OpenAI 用量刷新与验证

## 当前行为

账号用量的单次、批量刷新和渠道监控共用同一条路径：

1. 优先读取 `GET /backend-api/wham/usage`，不额外查询重置券详情。
2. 读取失败或没有可用窗口时，最多发送一次 `POST /backend-api/codex/responses` 模型请求兜底。这类请求可能消耗额度。
3. 普通 OAuth 账号使用模型响应中的 Codex 配额头。Spark 使用父账号凭据请求 Spark 模型，然后重读 `codex_bengalfox`；不把模型响应中的全局 Codex 配额头写入 Spark 快照。
4. 取得可信窗口后仅写展示快照，不写本地运行时限流状态、不触发额度重置或消费重置券。双方均失败则保留旧数值、原快照时间及错误。

10 分钟节流适用于整个刷新过程；节流命中不再次发送模型请求，最近的失败仍可见。`force=true` 绕过节流，`source=passive` 仅读取本地快照。WSv2 开关不影响刷新。

缺失窗口显示未知，成功返回单窗口时清空旧的另一窗口。过期时间本身不会将观测值归零。`updated_at` 是快照实际获取时间，无可信时间则为空。

账号页面展示“刷新失败，当前为历史数据”和快照时间；无快照显示未知。渠道监控在最终刷新失败时返回失败结果，401/403 沿用凭据异常分类，超时等错误不推断账号失效。

## 已完成的本地验证

- 前端相关 7 个测试文件、131 个用例通过。
- 中英文语言键结构一致，新增的 4 个提示键均存在。
- `gofmt` 和 `git diff --check` 通过。
- 全量前端类型检查及 Go 编译/回归未完成：本地内存约 900 MB，检查期间持续换页，已停止重型检查。
- 新增和调整的 Go 模拟测试已写入代码，尚未取得执行结果。测试均使用本地模拟上游，无真实账号模型调用。

## 在资源充足的环境执行

在仓库根目录运行：

```bash
cd backend
go test -tags=unit -p 1 ./internal/service -run 'Test(OpenAIUsageRefresh|ShouldRefreshOpenAI|AccountUsageService|GetOpenAIUsage|BuildCodexUsageProgress|BuildCodexSpark|QueryUsage|OpenAIQuota|ChannelMonitorQuota|ExtractOpenAIModelTest)' -count=1
go test -tags=unit -p 1 ./internal/handler/admin ./internal/repository -run 'Test.*(OpenAIQuota|OpenAIRefreshQuota|ResetQuota|ChannelMonitorQuota|CodexDisplaySnapshot)' -count=1
go test ./cmd/server -run '^$'
cd ../frontend
pnpm install --frozen-lockfile
pnpm typecheck
pnpm exec vitest run src/components/account/__tests__/AccountUsageCell.spec.ts src/components/account/__tests__/UsageProgressBar.spec.ts src/components/account/__tests__/OpenAIQuotaResetCell.spark_shadow.spec.ts src/components/common/__tests__/MonitorQuotaView.spec.ts src/components/user/__tests__/MonitorCard.quota.spec.ts src/utils/__tests__/accountUsageRefresh.spec.ts src/views/admin/__tests__/AccountsView.usageWindowsHint.spec.ts --maxWorkers=1 --minWorkers=1
pnpm check:i18n
```

Go 模拟测试覆盖：三个入口、普通/Spark、成功及 401/403/429/5xx/超时/解析失败/空窗口，模型兜底成功和失败、父子凭据与快照隔离、节流和强刷、历史数据时间、单窗口和错误恢复。成功的直接读取应有零次模型请求；失败时最多一次模型请求，Spark 额外的读取仍只映射 `codex_bengalfox`。

本次交付不包含生产部署。

## GitHub Actions

工作流 `.github/workflows/openai-usage-refresh-ci.yml` 将 Go 和前端放到独立的 Ubuntu runner：

- 推送到 `codex/openai-usage-refresh*` 分支自动运行；相关文件的 PR 和手动触发也支持运行。
- 后端使用 `unit` 标签，以包含 handler 的单元测试；只选择模拟上游和本地仓储桩，不需要真实账号、生产数据库或生产密钥。
- 前端运行全量类型检查、全部 7 个相关组件测试和语言包完整性检查。
- 常规 CI 的关键前端测试列表也已包含这 7 个文件。
- 每个 job 保存结果摘要，测试日志作为 Actions artifacts 保留 14 天；没有部署步骤。

首次提交的新工作流会在分支 push 时运行。网页中的手动运行入口需工作流先进入默认分支；验证本次分支使用 push 触发即可。
