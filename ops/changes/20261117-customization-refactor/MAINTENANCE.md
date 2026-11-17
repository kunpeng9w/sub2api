# 接入与维护约定

## 实际实现归属

| 需求 | 唯一实现/必要接入 | 后续修改规则 |
| --- | --- | --- |
| 普通 OpenAI 用量刷新策略与共享任务 | `service/custom_openai_usage_adapter.go`；`account_usage_service.go:getOpenAIUsage` 单一协调调用，保留原窗口解析和本地费用读取 | 普通、Spark、受限读取、force、取消和监控各有现有契约；不能用升级的新策略直接替换 |
| 监控全局额度、调度与展示有效期 | `service/custom_monitor_status_adapter.go`；fetcher/service/aggregator 保留原同名调用 | 历史采集/可用率不因展示适配而重写；暂停、自动检测开关、12h 手动状态分开 |
| 监控执行与权限 | 原 `channel_monitor_access.go`、`account_monitor_usage.go`、`runCheck` | 保持锁/槽覆盖真实网络任务及保存；不复制 provider 网络请求 |
| 客户端默认配置 | `frontend/src/custom/client-config/claudeCode.ts`；`UseKeyModal.vue:generateAnthropicFiles` 一个调用 | shell 前缀、引号、设置路径、字段/值及顺序保持现有输出；用已有组件用例覆盖 |
| 额度增强 | 原 `service/quota_estimate.go` 与 `domain.QuotaEstimate` | 本轮试点停止，不为目录再增加桥接；时间来源、费用查询、历史读写保留 |
| 代理出口/生命周期 | 原出口解析、HTTP/TLS、WS、插件、后台 context 与仓储事务 | 本轮不搬迁、不删校验；新增请求路径必须登记实际客户端与最终校验；到期不重绑 |
| 网络状态 | 原独立服务、路由与页面 | 无需重写服务或建立通用注册框架 |

适配器保留在 `service` package，避免导出上游内部符号、复制 Account/Proxy/UsageProgress 或破坏缓存和锁的生命周期。本轮没有 schema/Wire/锁文件改动，没有新增迁移。

## 检查与证据

专项工作流须与迁移同批更新实际 package、format/lint 路径和筛选。`verify-evidence.py` 从 Go JSON 日志确认关键用例真实 pass；被筛掉或 skip 不能满足必选用例。通用 CI 仍检查全部 package。远程生成补丁应为空；任何生成修改只在 Actions 产生，下载审阅后再提交。

`ops-ci.yml` 在本重构分支/PR 生成 `customization-report-<SHA>`：固定两个候选、同一 v0.2.22 目标、相同 `git merge-file -p` 参数，输出完整路径、冲突、merge-base 和生产源码指标。它还检查 11 个核心出口/生命周期文件相对原始基线是否变化。该脚本有固定基线及目标，不能作为未来升级的常驻门禁；工作流仅在本重构分支/PR 执行。

文本自动合并和校验通过不能替代升级语义验收。以后同步上游时，重新读取 CONTRACTS.md、EGRESS-CALLS.md 和下一版本差异；先判断上游是否覆盖相同契约，再决定删除本地适配，保持 merge 祖先关系。
