# 实施及验收记录

日期：2026-11-17（Asia/Singapore）。代码与工作流验收提交：`c33ae44657289c6049a9f9e7e07594313bd74250`，分支 `codex/customization-refactor`。以下都是该 SHA 的 push 运行，不是 PR 合成 merge SHA。验收文档提交不改变下面验证过的应用源码和工作流；同分支后续提交的复跑结果及精确 SHA 以 Actions 日志和同 SHA artifacts 为准。

## 完成范围及停止决定

1. 固定 v0.2.21 定制起点、需求契约和 120 个生产代理解析调用点（CONTRACTS.md、EGRESS-CALLS.md）。
2. 额度纯函数包试点通过既有用例，但没有减少共享定制、重复公式或调用依赖，已撤回并保留原结构（PILOT.md、PILOT-EVIDENCE.json）。没有常驻 custom/quotaestimate 包。
3. 提取本地 OpenAI 刷新/缓存协调和监控状态适配到同 package 独立文件。保留额度 API 优先、失败模型兜底、Spark/受限隔离、取消及真实执行期限。保留原解析、本地费用、执行链、权限与锁/槽生命周期。
4. 按实际出口评估后保留全部现有网络保护；不删除代理校验或改动生产出口/生命周期规则。11 个核心出口文件与基线完全一致（EGRESS-REVIEW.md、DIFF-REPORT.json）。
5. 提取 Claude Code 默认模板，四种配置输出共用一份默认值；组件继续控制原有客户端选择/路径/hint。网络状态、原生 7d 公式、API/JSON/数据库/迁移保持既有契约。

12 个原有函数迁移后实现文本完全一致，逐项哈希见 MOVED-FUNCTIONS.json。新增 refreshOpenAIUsageSnapshot 只把原 closure/singleflight 等待结果返回给原入口，没有新建领域结构或导出内部符号。

## 同 SHA 远程验收

| 工作流 | Actions run | 结果 |
| --- | --- | --- |
| Channel monitor isolated verification | [44027516230](https://github.com/kunpeng9w/sub2api/actions/runs/44027516230) | success |
| Source and operations verification | [44027516272](https://github.com/kunpeng9w/sub2api/actions/runs/44027516272) | success |
| Security Scan | [44027516328](https://github.com/kunpeng9w/sub2api/actions/runs/44027516328) | success |
| Upstream upgrade verification | [44027516241](https://github.com/kunpeng9w/sub2api/actions/runs/44027516241) | success |
| Egress policy simulation | [44027516335](https://github.com/kunpeng9w/sub2api/actions/runs/44027516335) | success |
| OpenAI usage refresh verification | [44027516270](https://github.com/kunpeng9w/sub2api/actions/runs/44027516270) | success |
| CI | [44027516295](https://github.com/kunpeng9w/sub2api/actions/runs/44027516295) | success |

- 基础 CI：前后端测试、真实数据库 integration、Go lint 和既有运维 shell/发布辅助检查。
- usage：实际额度/模型兜底、快照、权限/受限读取、Spark、缺失与显式零、采样和历史；重复 10 次 caller cancellation race 检查与服务端编译。
- monitor：隔离网络的状态/可见性/手动/自动/锁与冷却/取消生命周期、数据库 integration、前端回归、lint、生成及前后端构建。
- egress：HTTP/SSE 不回退、最终绑定、OAuth、WS lease 复核、插件、后台媒体和批任务、到期 outbox 原子性；TestProxyExpirySuite 的 legacy direct/backup 和续期快照 integration 必须真实 pass。日志为 Go JSON，verify-evidence.py 拒绝把缺失或 skip 的必选用例算作通过。
- ops：独立网络状态真实 PostgreSQL 测试、出口审计/连接生命周期回归、Mihomo DNS 真实 systemd 测试、模板与嵌入构建、匹配 source SHA 的产物来源。
- upgrade 工作流只验证本次 v0.2.21 定制候选：生成、全部前端用例、两个历史数据库基线升级/重复启动、跨模块 race；没有执行 v0.2.22 升级。
- Ent/Wire/前端锁文件生成的升级 patch 为 0 字节；未手工修改生成代码。

完整 run/artifact 元数据见 CI-RESULTS.json；实际必选测试结果见 TEST-EVIDENCE.json；构建来源见 BUILD-PROVENANCE.md。每个 artifact 名均带来源 SHA，构建产物从 Actions 下载。未运行真实上游账号探测，未部署。

## 差异与合并演练收益

按 report.py 固定口径，排除 ops、测试和生成代码：

| 指标 | 重构前 | 重构后 |
| --- | ---: | ---: |
| 修改的上游既有生产文件 | 163 | 163 |
| 这些文件的新增差异行 | 3016 | 2701 |
| 这些文件的删除差异行 | 1338 | 1403 |
| 这些文件的差异总行数 | 4354 | 4104 |
| 全部有差异的生产文件（含新增独立文件） | 189 | 192 |
| 对同一 v0.2.22 的共同修改路径 | 65 | 65 |
| 同参数逐文件文本冲突文件 | 8 | 8 |

共享文件差异总行数减少 250 行；新增两个 Go 适配器和一个前端模板文件，应用源码本次改动净增加 19 行。getOpenAIUsage 从 82 行缩至 49 行，按控制语句行的静态口径从 18 减至 12；增加一个明确的刷新适配调用。没有声称减少共享文件数或文本冲突数。

重构前、后的 merge-base 均为 v0.2.21 提交 `10ba8f9b4dc2e6023d4a9879ab77b21841babd10`，演练目标固定 `b0fa9f3e6eeab1968a53e1289b69daa4bb40ca2b`，两个候选均未合入目标。27 个自动合并且含出口调用的路径已做调用行比对：25 个不变，两个只改变赋值声明语法；完整路径和语义边界见 MERGE-REVIEW.md、DIFF-REPORT.json。文本结果不代表升级兼容。

## CI 修正与历史失败

- 额度试点首次因空白格式失败，后在 Actions 上修正并执行到纯包公式与两个 service 接入用例。试点结论依据维护收益，不依赖绿色 job 扩大目录迁移。
- 隔离脚本在 sudo 丢弃环境时使用错误 Go 缓存目录；保留原 UID/组/网络隔离，增加 NUL 分隔的原环境快照恢复。没有改变生产服务。
- 第一版适配遗留 aggregator 的未使用 time import，已删除并重新验证。
- DNS SIGKILL 用例在原始基线也失败（run 43417474087，重跑 job 133843996010）；日志显示杀整个 cgroup 连 ExecStopPost 一同杀死。测试改为只杀主进程，所有恢复断言保留，最终真实 systemd 测试通过。生产 DNS/出口代码未改。
- 新增严格出口数据库任务首次在网络隔离后下载 testcontainers 依赖被拒绝；已在隔离前显式 go mod download，最终数据库断言实际执行并通过。
- 被下一批推送取消的旧运行不计作通过；最终以本表同 SHA 的完整运行覆盖。原有未提交 v0.2.22 计划目录保持原样。

后续维护及上游替代/删除条件见 MAINTENANCE.md。v0.2.22 的请求策略、未知/刷新失败窗口、Fable 采样/恢复、Vertex 区域、计费装配等变化另行升级评估，本轮不采用。
