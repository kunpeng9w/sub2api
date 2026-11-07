# v0.2.20 定制升级

2026-11-07（Asia/Singapore）开始。本记录保留实施与检查过程；最终验收状态及源码 SHA 以 PR 和交付目录验证清单为准。

- 升级 PR：https://github.com/kunpeng9w/sub2api/pull/5 ，目标 `main`，不自动合并。
- 保留监控 PR：https://github.com/kunpeng9w/sub2api/pull/4 ，不关闭、不合并。
- 定制基线：`56ef3746843ba857d281b39049a36fa82d9cad56`，领先 `main` 38 个提交，全部保留。
- 上游标签：`v0.2.20`，固定提交 `dab829a4e9b7d1cc307189aea8b0319eb29e29d0`。
- 普通合并提交：`043b143ebb9b05290e3ec102afefd9e62c7c6c2f`，第一父提交为定制基线，第二父提交为固定上游。
- 所有安装依赖、生成代码、格式化、构建、测试、Lint、类型检查均在 GitHub Actions。本机仅编辑、阅读与轻量检查。
- 仓库外交付目录：`/home/pk/deliveries/sub2api-v0.2.20/`。最终交付包含归档、SHA256、版本、真实 Git SHA/构建时间/source 来源、Actions 日志、最终验收清单与本说明。

## 合并决策

9 个冲突文件逐项处理，未整批选用 ours/theirs：三份工作流保留 Node.js 22/pnpm 9；OAuth 同时保留上游代理授权校验与严格代理解析；HTTP transport 同时保留当前绑定校验、监控禁止回退、Claude 跳转策略、审计和上游有限读取；usage 保留 API 优先/共享刷新/取消隔离并在读取缓存及刷新前验权，受限读取与特权回退使用不同 flight，受限读取禁止模型回退与重置；package/lock 使用 vue-i18n 与 message-compiler 11.4.16 并保留安全覆盖；客户端模板测试接受普通及 WS 默认 gpt-6.1-sol 和搜索/图像配置，保留远程目录与 Claude Code 定制。

70 个双方共同修改文件清单及逐项复核见 [共同修改文件](common-file-review.md)。复核发现并修复额度服务自动合并的旧返回值签名；上游旧 probe 持久化测试改为通过新 usage API 刷新验证无重置通知。新增临时 PostgreSQL 回归从精确定制基线迁移目录升级，同时验证空库初始化和三次启动幂等性。

迁移 `249_channel_monitor_manual_access.sql`、`250_channel_monitor_sort_order.sql` 保持基线原文；`249_mcp_oauth.sql` 保持上游原文。执行器按完整文件名和校验和识别，两个 249 文件不改名。MCP 默认关闭，生产不配置 `MCP_PUBLIC_URL`。受限管理密钥对既有定制接口继续默认拒绝。

## 验证状态

首轮 Actions（合并提交，尚非最终验收）：

- [升级检查/生成补丁与完整前端](https://github.com/kunpeng9w/sub2api/actions/runs/42355792744)：生成差异由 Actions 导出，下载审查后提交；最终需无差异重跑。
- [CI](https://github.com/kunpeng9w/sub2api/actions/runs/42355806182)、[源码/运维](https://github.com/kunpeng9w/sub2api/actions/runs/42355806189)、[Security Scan](https://github.com/kunpeng9w/sub2api/actions/runs/42355806169)。首轮安全扫描成功；源码检查发现历史本地审计目录链接已失效，修为本地目录说明。
- [监控](https://github.com/kunpeng9w/sub2api/actions/runs/42355806211)、[额度刷新](https://github.com/kunpeng9w/sub2api/actions/runs/42355806183)、[出口专项](https://github.com/kunpeng9w/sub2api/actions/runs/42355818817)。首轮额度检查拦截未格式化文件，已应用远程补丁；测试编译发现旧 probe 方法测试，已适配新入口。

最终结果将写入仓库外交付清单 `verification.json`，绑定完整源码 SHA 和各 Actions run URL。只有全部必需工作流成功、生成代码无差异、PR 脱离草稿并完成产物核对后才算源码升级验收。CI 不证明生产升级成功。

## 本机配置候选（未应用）

保留上游全局默认值，不将本机候选写入默认配置。当前小内存主机可从以下保守起始候选评估：

```yaml
gateway:
  request_body_memory_budget_bytes: 134217728 # 128 MiB，仅请求体缓冲分配预算
  request_body_read_timeout_seconds: 120
  downstream_write_timeout_seconds: 120
```

预算不是总进程内存限制，也不是已验证生产容量；还需为 Go 堆、连接池、解压、响应缓冲、前端、数据库和系统留余量。预算不足会拒绝缓冲请求，不保证单个超大请求可以执行。读写超时是每次读/写或 flush 的限制，不是整条长流的总时长。上线前结合实际内存余量、并发、图片/请求体规模及慢客户端场景复核。MCP 保持默认关闭，不配置生产 `MCP_PUBLIC_URL`；不更改既有数据库、Redis、出口和网络配置。

## 手动部署与健康检查（尚未执行）

以下仅供后续人工维护窗口执行，本次没有运行这些生产命令。先确认目标是 Linux amd64，交付 `build-info.txt` 的源码 SHA 与 PR 最终验收清单一致，并在交付目录执行 `sha256sum -c SHA256SUMS`；不在本机编译或重新打包源码。

1. 记录当前程序路径、systemd unit、配置来源、运行用户、旧程序版本和 SHA256。为旧程序、配置和 unit 创建带时间戳的独立备份，不覆盖历史备份。保留线上密钥权限，备份不提交 Git。
2. 暂停业务写入并停止应用（包括所有其他实例/后台写入者）。使用实际数据库连接参数执行 `pg_dump --format=custom --file=<备份路径>`，另备份必要角色/权限；在隔离数据库验证 `pg_restore` 可恢复并核对监控、授权与排序数据。记录备份校验和、恢复命令和维护窗口。备份未经恢复验证不得继续。
3. 审查 Actions 的空库、基线升级和重复启动迁移日志；核对三份迁移名称及校验和。生产迁移只允许由维护窗口启动的新程序运行。不要改已应用 SQL，不手工重命名 249，不用 Ent 自动 schema 创建替代迁移执行器。
4. 将压缩包解到独立暂存目录，核对程序 SHA256 与 `sub2api.sha256`。先保留旧程序，再通过同文件系统原子替换安装新程序，恢复执行权限/运行用户。复用已有生产配置；候选值仅在完成容量复核后人工应用。
5. 启动一个实例，观察迁移日志及 `schema_migrations`。确认无校验和不匹配、迁移失败或重启循环后，再恢复其他实例。检查本地 `/health`、Nginx HTTPS、登录、权限拒绝、监控可见范围/排序/手动检查、额度窗口倒计时。核对现有监控配置和授权完整，新 MCP 未开启。
6. 由操作者批准并使用真实凭据验证少量 HTTP/SSE、WS、非流式 Responses、图像请求。检查严格代理绑定、Claude 分流、断连后的有限读取、HTTP/WS 审计归因和内存占用。随后逐步恢复流量；记录实际验收时间、版本、SHA、错误率/延迟/内存和未通过项。

## 回退

应用启动或验收失败立即停止新实例并冻结写入，保留失败日志和数据库副本。若尚未执行迁移，可原子恢复备份程序和原配置后启动。若已迁移，不能假定旧程序兼容新 schema：默认在全部应用停止且有明确恢复点的条件下恢复数据库备份、原程序和原配置，再验证健康及数据。恢复数据库会丢失备份点之后的写入，必须先确认业务补偿方案。SQL 无自动 down 迁移，不手动删除迁移记录或新表来伪装回退。网络、Nginx、数据库和 MCP 的生产配置本次没有变更，无对应自动配置回滚动作。

未覆盖的生产验证：真实生产迁移与恢复、真实上游凭据、实际代理节点切换、长期 WS/SSE、真实图像负载、并发内存容量和流量验收。历史部署记录保持原有时间含义，不以本次 CI 更新为已部署。
