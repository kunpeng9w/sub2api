# v0.2.22 部署验收

完成时间：2026-11-17T20:56:05.857964+08:00（Asia/Singapore）。用户授权后完成生产部署。

- 从运行中的 0.2.21（0161258af）升级到 0.2.22（9339f5c5d），保留 standard 模式和现有配置/drop-ins。
- 程序来自 Actions 44050220274，运行进程 SHA256 已核对为 `35a4363f8602e78eb5ed9d9f2681d22f29a7c14acff928eb43cbfd0e81004cd2`。
- 使用现有备份锁，停服后确认数据库没有其他连接，保存最终数据库 dump、全局角色、data、程序及服务配置。恢复点：`/home/pk/workspace/sub2api/.operator/deploy-v0.2.22-20261117T205409`（root 私有目录）。已校验 SHA256 和 dump 目录；实际恢复演练未执行。
- schema_migrations 从 304 增至 306；只新增两份 251，旧记录及所有迁移内容校验一致。用户、账号、API 密钥及账号分组数量保持一致。
- 本地与域名 HTTPS 健康正常；首页、主 JS 资源、公开设置 HTTP 200；未登录管理接口 HTTP 401。
- 部署后的真实流量日志观察到 Responses、模型列表、登录状态、监控列表/手动执行成功。未另外发起付费模型验证。未执行专门的 WS/SSE、图像或容量验收。
- 观察期服务 active/running，NRestarts=0；未见 ERROR、panic 或迁移校验失败。既有启动配置警告保留于私有日志，包括定价回退文件缺失；本次没有改变对应设置。
- Nginx、PostgreSQL、Redis active；Mihomo inactive，出口状态 consistent=true、claude-only/confirmed，保持部署前状态。

完整脱敏状态在 [deployed-build.json](../../deployed-build.json)。Actions 原始证据在 `release/v0.2.22/9339f5c5d/evidence/`；本次原始启动及运行日志在私有恢复点。回滚参见 [DEPLOY.md](DEPLOY.md)，迁移已执行，回退须评估数据库恢复及恢复点之后写入的保留，不能只换旧二进制。PR #7 本次未合并。
