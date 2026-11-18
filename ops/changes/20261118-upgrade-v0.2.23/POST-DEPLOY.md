# v0.2.23 部署验收

完成记录时间：2026-11-18T13:54:30.452313+08:00（Asia/Singapore）。程序于 2026-11-18 13:49 上线，用户已授权合并和生产部署。

- PR #8 已按普通 merge 合入 main：`f1c26fcbd74aa4871f58a1634645066dc2a17453`，完整保留上游及定制历史。合并树与通过验收的候选完全一致。
- 运行版本 0.2.23，源码 `dc22d82aebb3b3484c98512678b435bbf48afd58`，来自 Actions 44244840668。安装包与运行进程均核对 SHA256：`f2bebe5334ff017bf2b7d55f9684fc6e6621cb901e5140e9c1180ddc74ac7f06`。
- 最终源码的七个分支工作流及七个 PR 工作流全部成功；前端 394 文件 / 3211 用例通过，生成补丁为空；迁移、权限、监控、额度、出口、取消隔离、竞态、Lint、安全扫描和构建证据保存在 `release/v0.2.23/dc22d82ae/evidence/final/`。本机未运行编译、单元测试、Lint 或类型检查。
- 在现有备份锁内停服，确认其他数据库连接为 0，备份数据库、全局角色、data、旧程序、服务及出口配置，验证 SHA256 和归档目录。恢复点：`/home/pk/workspace/sub2api/.operator/deploy-v0.2.23-20261118T134900`，root 私有；实际恢复演练未执行。
- 迁移从 306 增至 307，仅新增 `252_channel_pricing_ultrafast_multiplier.sql`；所有旧迁移和候选源码校验和一致，新增 nullable numeric 列已就绪。用户、账号、API 密钥及账号分组数量停服前后完全一致。
- HTTPS 健康、首页、公开设置和新版 JS 资源返回 200；未登录管理 API 返回 401。观察到实际流量 `/v1/responses`、`/v1/models`、`/api/v1/auth/me` 和监控列表成功。未额外发起付费模型请求。
- 专门新登录抽查未完成：历史 operator admin 账号在部署前备份及部署后数据库中都不存在；未修改用户、密码或鉴权规则。现有会话的 auth/me 已成功。未做专门 WS/SSE、图像或容量验收。
- 服务 active/running，NRestarts=0，观察日志无 ERROR/panic。保留 standard 模式、原配置和 drop-ins，Nginx/PostgreSQL/Redis active，出口 consistent=true、claude-only/confirmed，Mihomo inactive。
- 原工作区 Nginx 及相关未提交改动保留。原部署记录整体保存为 release 目录 `deployed-build.previous.json`，并纳入最新 [deployed-build.json](../../deployed-build.json) 的 previous_deployment_20261117；旧历史字段保留。

## 恢复

恢复前先停服务并保留故障日志及当前数据库。迁移已经执行，不能假定只换旧程序足够；以私有恢复点为基准评估数据库、程序和配置的一致恢复，并保留恢复点之后的业务写入。不要直接运行会丢弃新数据的自动恢复命令。备份 SHA256 与 dump 目录已验证，不表示实际恢复演练通过。

原始启动与运行日志仅存于私有恢复点；脱敏 HTTPS、来源、前后状态及检查证据存于 `release/v0.2.23/dc22d82ae/`。
