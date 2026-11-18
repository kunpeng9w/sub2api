# 重构方案实施审核

审核日期：2026-11-17（Asia/Singapore）。审核范围为固定起点 `0161258af6ca167f5885c145b454d3a982839939` 至当前 HEAD `3df1a585096fd3b677315ed2456105664ade8d26`，参照同目录 PLAN.md。本次只审核并新增此记录，没有修改被审核实现、提交、推送或部署。

结论：本轮有边界的结构整理基本落实，当前提交的远程验收充分，未发现本次重构引入的阻断性代码问题。发现一项需修正的验收文档问题；“每批独立验收”也未完全按原计划执行。维护收益已经体现在共享文件差异减少，但没有体现为共享修改文件数或下一版本文本冲突数减少，不能据此宣称上游升级成本已显著降低。

## 发现：P2，合并语义审查误将生产代码变化登记为元数据

位置：MERGE-REVIEW.md 第 27、31、61–63 行。五个路径被标成“文档或版本元数据”，而固定目标 `v0.2.22` 相对固定上游基线 `v0.2.21` 的 Git 差异实际包括：

| 文件 | 实际目标变化 | 应补充的升级审查内容 |
| --- | --- | --- |
| `backend/internal/config/config.go` | `Concurrency.PingInterval` 验证允许 0 | 禁用等待 SSE ping 的配置和消费端行为 |
| `backend/internal/service/account_test_service.go` | Vertex URL 构造调用增加参数 | 目标函数签名与区域选择，以及账号测试出口 |
| `backend/internal/service/openai_models_list.go` | passthrough 模型过滤参与模型列表投影 | 列表可见性与实际请求模型准入的一致性 |
| `backend/internal/service/openai_ws_forwarder_ingress.go` | 身份元数据注入增加 `codexClientSessionID(c)` | WS 会话身份与账号隔离 |
| `backend/internal/service/openai_ws_v2_passthrough_adapter.go` | 首帧及后续帧注入客户端 session ID | 首帧、后续 `response.create` / `session.update` 的身份处理一致性 |

这不构成当前 v0.2.21 重构版本的运行时回归，因为目标版本尚未合入。但它使逐路径语义审查记录不准确；后续升级如果沿用该清单，可能遗漏实际行为核查。应修正这五行并补充上述检查，不能以出口调用文本不变替代这些语义结论。

## 方案与实际交付

| 计划项 | 核查结果 | 判断 |
| --- | --- | --- |
| 固定基线、需求契约、接入清单 | CONTRACTS.md、EGRESS-CALLS.md 和固定 SHA 已登记；独立适配的最终位置另有补充 | 已完成 |
| 额度纯计算试点 | 已试验、保存远程用例证据并撤回；最终 quota_estimate 实现保持基线 | 符合停止条件，不应算作遗漏 |
| 刷新与监控协调 | 新增两个同 service package 的适配文件；原入口保留；权限、锁、保存和网络执行生命周期未改 | 已完成有限收拢 |
| 出口策略 | 逐类评估后保留现有实现；无删除解析校验、改写仓储事务或引入外层包装 | 按停止条件保留，未实现进一步收拢 |
| 前端整理 | Claude Code 默认项统一为一份数据，shell/JSON 生成复用；原组件通过一个调用接入 | 已完成 |
| 最终提交验收 | 当前 HEAD 的七个工作流全部成功，已读取原始日志和指定 artifacts | 已完成 |
| 合并演练 | 同一目标、基线、算法的两次演练已在 Actions 执行，当前 HEAD artifact 可用 | 文本证据完整；语义记录存在上述问题 |

## 源码核查

- 实际应用变更仅涉及 8 个文件：4 个既有 Go 文件、2 个新增 Go 适配器、1 个既有 Vue 组件和1 个新增 TypeScript 模板辅助文件。应用源码净增加 19 行。
- 独立从 Git 基线提取并比对 MOVED-FUNCTIONS.json 中的 12 个函数，正文全部一致；没有仅依赖该 JSON 的 `identical` 声明。
- 新增 `refreshOpenAIUsageSnapshot` 从原 `getOpenAIUsage` 提取闭包与 singleflight 等待逻辑；源码审阅未发现缓存键、权限作用域、取消处理、超时或错误返回顺序改变。
- 三种 shell 模板及 settings JSON 的默认字段、值、顺序、路径和 hint 保持原实现；现有组件测试覆盖默认项。
- 出口核心保护文件、数据库迁移、API 路由、Ent/Wire 生成文件和依赖锁文件没有本轮差异。额度包试点未留下常驻包或转发桥接。
- CI 的环境恢复和 DNS 主进程故障注入改动限于检查设施；后者仍保留恢复断言，并有真实 systemd 用例通过日志。

## 当前 HEAD 的远程结果

以下从 `kunpeng9w/sub2api` 实时读取，不是沿用 c33ae4465 的验收状态；七次均为 `push`，`headSha` 均为 `3df1a585096fd3b677315ed2456105664ade8d26`。所有 job 成功，步骤元数据未出现 skipped；这不代表所有测试套件中不存在个别条件跳过。

| 工作流 | Run ID | 结果 |
| --- | --- | --- |
| CI | 44029142757 | success |
| OpenAI usage refresh verification | 44029142722 | success |
| Channel monitor isolated verification | 44029142858 | success |
| Egress policy simulation | 44029142771 | success |
| Source and operations verification | 44029142741 | success |
| Upstream upgrade verification | 44029142881 | success |
| Security Scan | 44029142871 | success |

已下载七次运行的原始日志，核查到以下执行证据：

- 完整前端套件：390 个测试文件、3,166 个用例通过；UseKeyModal 专项 32 个用例通过。
- usage、monitor、egress 的必选用例检查均输出 `missing_or_not_passed: []`。相关 JSON 测试事件中未见 fail/skip；计数包含父测试和子测试，不能当作独立用例总数。
- 共享刷新取消及已取消调用者场景：普通回归一次，加 race 阶段重复十次，均有 pass 事件。
- 出口真实数据库用例包括 legacy direct/backup 保留绑定及 `TestSweep_SkipsChangedSnapshot/renew`，均实际 pass。
- DNS 的 `test_real_lifecycle_start_stop_restart_crash_and_failed_start` 为 `ok`。
- 已下载 `upgrade-generated-3df1a585096fd3b677315ed2456105664ade8d26`，其中 `upgrade-generated.patch` 为 0 字节。
- 已下载 `customization-report-3df1a585096fd3b677315ed2456105664ade8d26`；来源 SHA、固定起点、目标、merge-base 和保护文件结果均匹配。

日志及下载的审核证据保存在 `/tmp/sub2api-refactor-audit-20261117/`。本次没有下载或重新核验当前 HEAD 的二进制压缩包；已有 BUILD-PROVENANCE.md 对应 c33ae4465，两个提交间仅验收文档及 JSON 记录变化。构建成功结论来自当前 HEAD 的远程 job 与日志，不包含部署验证或真实账号请求验证。

## 收益与过程偏差

当前 HEAD 的 Actions 报告与原验收统计一致：

| 指标 | 重构前 | 当前 HEAD |
| --- | ---: | ---: |
| 修改的上游既有生产文件 | 163 | 163 |
| 这些文件的新增与删除差异总行数 | 4,354 | 4,104 |
| 全部有差异的生产文件（含新增文件） | 189 | 192 |
| 对固定 v0.2.22 的共同修改路径 | 65 | 65 |
| 同算法文本冲突文件 | 8 | 8 |

共享差异减少 250 行，约 5.7%；实现归属更清晰，但维护的独立文件增加 3 个。额度试点与出口评估的停止决定合理。现有证据支持“有限的结构改善”，不支持“已减少升级冲突”。

PLAN §7.2 要求每批独立验收。历史运行显示，9ae3629d1 的 usage/CI、260763cc9 的 usage/CI，以及 d088c6997 的多数运行被后续推送取消；试点的完整 race 未在扩大下一批前完成。这一点在 PILOT.md 和 VERIFICATION.md 已有部分披露，但不能表述为每批全部通过。最终 c33ae4465 和当前 HEAD 的完整成功结果补足了最终版本验证，不能追溯替代中间批次的独立验收。后续应在批次所需检查结束后推进，或明确记录改用最终累计验收的偏差。

本机只做源码/Git 比对、日志读取、artifact 元数据及文本检查和 `git diff --check`，没有执行编译、测试、Lint、类型检查或生成。原有未提交的 v0.2.22 升级计划保持原状。
