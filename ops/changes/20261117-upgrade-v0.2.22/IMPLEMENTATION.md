# v0.2.22 升级实现与审查记录

固定起点 `3df1a585096fd3b677315ed2456105664ade8d26`，目标 `b0fa9f3e6eeab1968a53e1289b69daa4bb40ca2b`，共同祖先 `10ba8f9b4dc2e6023d4a9879ab77b21841babd10`。普通 merge 的两个父提交由 Git 保留；重构验收不替代本轮验收。

## 行为与保留边界

| 范围 | 最终行为/接入 |
| --- | --- |
| 普通 OpenAI OAuth | 上游模型探测读取 x-codex 头；可能产生模型请求及额度消耗；按上游 TTL/force 和 active/passive 语义 |
| Spark | QueryUsage 解析 bengalfox；父账号凭据与出口、影子行快照；禁止使用普通模型头污染 Spark |
| 受限管理员 | 先校验账号及父账号范围；仅额度 API；按权限隔离刷新缓存及 singleflight，不生成模型请求、不通知重置 |
| 监控 | 独立 GetMonitorUsageForAccount/getMonitorUsage；保留 route-scoped 缓存、期限、执行锁与取消生命周期；不会加入页面探测 |
| 查询失败 | 页面返回旧观测/未知及 quota_refresh_failed；监控记录 error、当前显示 unknown；明确 401 或结构化失效 403 判 failed |
| 403 | invalid_api_key/token_expired/invalid_token/account_deactivated/account_disabled 才明确失效；其他权限/区域/出口拒绝为一般错误 |
| 过期窗口 | 页面按上游显示零，时间不更新；监控查询成功才采用新观测，失败/负缓存不自动恢复 |
| 窗口时间 | 仅刷新 5h/7d 中一个时保留另一个及其旧 sampled_at；缺失时间显示未知；新使用率缺 reset 时清旧 reset，不组合新旧数据估算剩余 |
| 监控业务 | 全局 5h/7d、耗尽 failed、手动结果 12h 有效、暂停与关闭自动检测分开；保留手动权限与关联账号状态 |
| 其余定制 | 严格出口、额度估算、历史容量、网络状态、客户端配置模板和现有 API/定制字段保留 |
| 前端请求 | 普通、主动、批量请求入队前固定账号身份和代次；所有响应/错误/缓存/loading 更新校验代次；卸载后不更新 |

## 旧实现与测试映射

| 删除/迁移 | 接替与证据入口 |
| --- | --- |
| queryOpenAIUsageSnapshot API-first/fallback 引擎 | getOpenAIUsage 上游分支及 refreshOpenAICodexSnapshot；AllEntryPoints/ModelProbeSuccess/SparkRejectsGlobalHeaders |
| refreshOpenAIUsageSnapshot、openAIFailures、旧账户级成功缓存 | 上游十分钟探测节流 + 最小权限范围 singleflight/等待取消接入；Upgrade*、CallerCancellation* |
| queryUsageFromModel 的 Spark 模型回退/重读 | 普通 probeCodexUsage 使用原严格出口和鉴权 transport；Spark 始终 QueryUsage；EgressAuditOpenAIQuota* |
| 旧整快照清空缺失窗口 | 上游增量更新 + 窗口采样/缺 reset 清理；PartialWindowKeepsOwnTimeAndClearsReset、QuotaEstimateOpenAI* |
| persistOpenAICodexProbeSnapshot/Context（已无调用点） | 删除异步写入；改为刷新成功后同步 UpdateExtra，原不改运行时限流语义由同名持久化测试迁移验证 |
| 旧统一观测时间/重复错误提示 | 上游错误提示与 utilization_unknown；窗口级时间显示；AccountUsageCell 与 async 测试 |
| 旧普通账号不依赖 WSv2 的过期刷新断言 | 上游正常账号 staleness 门控；Spark 不依赖 WSv2；ShouldRefreshOpenAICodexSnapshot* |
| 上游监控 quota_refresh_failed 豁免/DoesNotFlap | 用户确认的失败 error/未知规则；MonitorExpiredFailureNeverRecovers、AuthenticationOverridesGenericFailure、UsageFailureInfo 矩阵 |
| 旧过期值始终保留的页面断言 | 上游 ZerosExpiredWindow；监控专用 observed helper 仍保留真实观测 |

openAIUsageFromExtra、hasOpenAIQuotaDisplayWindows、openAIQuotaDisplayUpdates 仍由监控/估算调用，保留；并未因删除页面旧引擎而删去这些依赖。原测试覆盖的凭据分类、权限、出口、取消与监控执行槽迁入对应新路径，旧回退策略用例由上述矩阵替代。

## 数据库与生成

两份不同完整文件名的上游 251 迁移原文加入：251_group_force_openai_ultrafast.sql、251_group_usage_rollup_nonblocking_build.sql。所有已执行 SQL 文件内容和名称不变，无新定制表。迁移工作流从空库、最早定制基线 56ef3746、本轮起点 3df1a5850 使用实际 runner 升级并重复启动，验证 ledger、列/default、汇总过期边界/重建失效、已有监控内容和权限。

Ent/Wire/pnpm 锁及 gofmt 只由 Actions 生成后下载补丁。生成文件自动合并只是暂存结果，最终以 Actions 重新生成且补丁为空为准。

## 检查记录约定

七个工作流：CI、OpenAI usage refresh verification、Channel monitor isolated verification、Egress policy simulation、Source and operations verification、Upstream upgrade verification、Security Scan。最终验收只汇总同一个分支源码 SHA 的完整结果；PR 合成提交 checkout 与产物单独记录。必选 Go 用例由 JSON pass 事件校验，完整前端和迁移日志保存在 artifacts。

首个候选 38fb9766c 的检查完整结束后修复，不取消检查推进。发现并修复 Fable bytes 导入、SQL 保护误包含 Go 测试、测试 i18n mock。Actions 44042866119 生成补丁已经下载审查；后续以最终候选完整重新验收，初始失败不作为通过证据。

PR #7 面向 main，包含 PR #6 全部提交及后续定制/重构；#6 保持原状。安装包来自 Source and operations verification 的 embedded-build artifact，版本/SHA/校验和另附验收记录。本轮不部署生产，不调用真实账号做验收。

第二轮 e7978205e：生成补丁为空、安全扫描及嵌入构建通过；空库/两种旧库迁移及重复启动通过，监控后端通过。完整回归指出旧 API-first 和保留旧 reset 的断言需迁移，Lint 指出两个失去调用点的异步持久化函数；删除后以新的同步持久化测试保留其不改调度语义。新异步测试发现重复账号切换刷新，改为身份 watcher 负责切换、内容 watcher 仅负责同账号更新。另补 Spark/受限 JSON 窗口缺 reset 时保留未知的契约。最终仍须完整重验。
