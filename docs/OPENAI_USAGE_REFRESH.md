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

## 验证结果

代码提交 `28902fce1e3279e95198b97fc00e5dd9a2e5bf20` 的[专用 GitHub Actions 验证](https://github.com/kunpeng9w/sub2api/actions/runs/41960241852)全部通过：

- Go service：43 个顶层测试通过，包含子用例共 163 个通过事件。
- Handler / repository：17 个顶层测试通过，包含子用例共 28 个通过事件。
- 服务器及依赖注入编译通过。
- 前端全量类型检查通过；相关 7 个组件测试文件的 131 个用例、3 个语言完整性用例全部通过。
- 源码与运维验证、安全扫描通过。
- `gofmt`、`git diff --check`、工作流 YAML/Shell 检查和 `actionlint` 通过。

[常规 CI](https://github.com/kunpeng9w/sub2api/actions/runs/41960241999)的全量 Go 单元测试、集成测试、lint、前端检查及发布辅助测试全部通过。首次运行发现的两处未检查类型断言已修复；现有计费内存测试在复跑中通过，没有调整阈值或断言。

同一代码提交的[源码与运维验证](https://github.com/kunpeng9w/sub2api/actions/runs/41960242113)和[安全扫描](https://github.com/kunpeng9w/sub2api/actions/runs/41960242296)也全部通过。

本地重型检查因约 900 MB 内存及持续换页而停止，重型验证已迁移至 GitHub Actions。测试使用本地模拟上游，无真实账号模型调用。

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
