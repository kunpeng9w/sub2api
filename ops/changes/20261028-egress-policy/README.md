# 代理策略改造交付

状态：候选交付，未部署。测试状态见 [验证报告](TEST-REPORT.md)。所有候选文件仅供审查和手动部署，不能直接覆盖在线文件。

本次只修改受 Git 管理的源码、模板和文档。测试在独立 CI runner 上使用模拟上游、代理、数据库与系统命令；不运行在线安装器、内核测试或真实模型请求。

阅读顺序：[发现与修正](CHANGELOG.md) → [验证报告](TEST-REPORT.md) → [部署教程](DEPLOY.md) → [回滚教程](ROLLBACK.md)。

| 场景 | 候选行为 |
|---|---|
| 官方 Claude | 整机受管 DNS 定向分流至 Mihomo 固定代理；Sub2API 同时使用本机代理入口 |
| 其他账号绑定代理 | 所有上游账号请求固定绑定，异常报错，保留绑定 |
| 其他账号未绑定代理 | 系统正常出站 |
| Mihomo 停止 | Claude 停流；普通 DNS、非 Claude 出站独立运行 |
| 分组跨账号重试 | 保留，新账号仍使用自身出口 |

两阶段独立回滚：第一阶段只替换应用并保持新 Claude 策略关闭，可长期停留；第二阶段再迁移 DNS、TUN 和保护。第三方 Claude 渠道不按模型名称强制分流。

`generate_templates.py` 从 `backend/internal/pkg/claudedomain/domains.txt` 生成脱敏候选。`prepare_private_config.py` 供操作者私下合并现有设置和代理节点，只写 root 私有候选目录，不安装文件；本次开发不执行它读取在线配置。

整机范围限于使用系统 DNS 或本机 Mihomo 的受管客户端。自带 DoH、固定 IP 或套其他远端代理的客户端必须先纳管。数据库、Redis、对象存储等独立系统渠道保留自身配置；账号的模型、鉴权、探测、上游媒体和批处理请求执行绑定策略。代理商是否固定公网 IP 仍由供应商保证。

候选文件与线上安装文件分开维护；Git 更新、源码提交及 CI 都不代表完成部署。当前历史运维材料保持原样。

设计核对依据（2026-10-28）：[Mihomo DNS](https://wiki.metacubex.one/config/dns/)、[Mihomo TUN](https://wiki.metacubex.one/config/inbound/tun/)、[dnsmasq 手册](https://thekelleys.org.uk/dnsmasq/docs/dnsmasq-man.html)。候选使用显式 `route-address`、`dns-mode: disabled`、关闭 `strict-route`/`auto-redirect` 和空 DNS 劫持列表。文档语义核对不代表安装中内核已经验证这些设置。
只读查询到安装中 Mihomo 为 `v1.19.33`；同时核对该版本的 [TUN 配置字段](https://github.com/MetaCubeX/mihomo/blob/v1.19.33/listener/config/tun.go) 和 [TUN 选项传递](https://github.com/MetaCubeX/mihomo/blob/v1.19.33/listener/sing_tun/server.go)，确认候选使用的字段在对应源码中存在。查询版本没有重启代理，也没有在本机加载候选配置。
