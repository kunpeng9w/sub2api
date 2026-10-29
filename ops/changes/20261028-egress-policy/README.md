# 代理策略改造交付

状态：最新交付为 [2026-10-29 安全修复版](../20261029-security-release/README.md)，安全扫描、专项及全量回归、lint、前端和正式构建全部通过，**第一阶段应用与第二阶段网络策略均已部署，正常运行状态验收通过**。下方设计及模板继续适用；2026-10-28 的验证和失败记录保留在 [历史验证报告](TEST-REPORT.md)。

源码与模板开发使用独立 CI runner 模拟验证；后续在用户授权下执行了线上第二阶段切换。实际执行、一次自动回滚和最终验收见 [第二阶段上线验收](PHASE2-POST-DEPLOY.md)。基础验收时未发起模型请求；后续经授权完成一次 [真实流式业务验证](BUSINESS-SMOKE.md)，现有 OpenAI 兼容渠道响应与用量记录通过。

第二阶段于新加坡时间 2026-10-29 11:41:55 切换成功，DNS、定向 TUN、SSH 回程和保护检查通过。[逐段手动操作单](PHASE2-MANUAL.md) 留作过程参考；已切换主机无需再次执行。运行应用为 `1dce7da6b`，网络模板为 `095d0913c`。

阅读顺序：[发现与修正](CHANGELOG.md) → [验证报告](TEST-REPORT.md) → [部署教程](DEPLOY.md) → [回滚教程](ROLLBACK.md)。

| 场景 | 候选行为 |
|---|---|
| 官方 Claude | 整机受管 DNS 定向分流至 Mihomo 固定代理；Sub2API 同时使用本机代理入口 |
| 其他账号绑定代理 | 所有上游账号请求固定绑定，异常报错，保留绑定 |
| 其他账号未绑定代理 | 系统正常出站 |
| Mihomo 停止 | Claude 停流；普通 DNS、非 Claude 出站独立运行 |
| 分组跨账号重试 | 保留，新账号仍使用自身出口 |

两阶段独立回滚：第一阶段只替换应用并保持新 Claude 策略关闭，可长期停留；第二阶段再迁移 DNS、TUN 和保护。第三方 Claude 渠道不按模型名称强制分流。

`generate_templates.py` 从 `backend/internal/pkg/claudedomain/domains.txt` 生成脱敏候选。`prepare_private_config.py` 供操作者私下合并现有设置和代理节点，只写 root 私有候选目录，不安装文件；本次线上切换使用私有合并候选，实际安装结果见验收记录。

整机范围限于使用系统 DNS 或本机 Mihomo 的受管客户端。自带 DoH、固定 IP 或套其他远端代理的客户端必须先纳管。数据库、Redis、对象存储等独立系统渠道保留自身配置；账号的模型、鉴权、探测、上游媒体和批处理请求执行绑定策略。代理商是否固定公网 IP 仍由供应商保证。

候选文件与线上安装文件分开维护；Git 更新、源码提交及 CI 都不代表完成部署。当前部署以第二阶段验收为准，历史运维记录保留原有时点。

设计核对依据（2026-10-28）：[Mihomo DNS](https://wiki.metacubex.one/config/dns/)、[Mihomo TUN](https://wiki.metacubex.one/config/inbound/tun/)、[dnsmasq 手册](https://thekelleys.org.uk/dnsmasq/docs/dnsmasq-man.html)。候选使用显式 `route-address`、`dns-mode: disabled`、关闭 `strict-route`/`auto-redirect` 和空 DNS 劫持列表。文档语义核对不代表安装中内核已经验证这些设置。
只读查询到安装中 Mihomo 为 `v1.19.33`；同时核对该版本的 [TUN 配置字段](https://github.com/MetaCubeX/mihomo/blob/v1.19.33/listener/config/tun.go) 和 [TUN 选项传递](https://github.com/MetaCubeX/mihomo/blob/v1.19.33/listener/sing_tun/server.go)，确认候选使用的字段在对应源码中存在。上述开发阶段的版本查询没有重启代理或加载候选；后续实际安装与路由验证见第二阶段上线验收。
