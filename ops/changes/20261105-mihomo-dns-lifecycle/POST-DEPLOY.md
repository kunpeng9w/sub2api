# Mihomo 生命周期 DNS：线上验收

2026-11-05 15:41:58（新加坡时间）安装完成。**Mihomo 保持停止，Claude 域名已恢复普通 DNS 解析。** 后续正常启停 Mihomo 时，通过 systemd hook 自动启用或撤销专用 DNS 转发，无需人工修改 resolv.conf。

## 源码与验证

- 源码提交：`99aef1b6acf01052fcc7e7eb807b12cd0f5ad340`。
- [草稿 PR #3](https://github.com/kunpeng9w/sub2api/pull/3)，目标 `codex/upgrade-v0.2.19`；未合并。
- 本 chat 的独立源码目录：`/home/pk/.codex/worktrees/mihomo-dns-lifecycle/sub2api/ops/scripts/mihomo-dns/`。
- [生命周期专项](https://github.com/kunpeng9w/sub2api/actions/runs/41988563171/job/127115613720)：6 项测试通过，含真实 systemd/dnsmasq 的启动、停止、重启、崩溃、启动失败、DNS 单独重启与降权后重载；仅使用本地假上游。
- [运维检查与既有回归](https://github.com/kunpeng9w/sub2api/actions/runs/41988563171/job/127115613634)：仓库检查通过，21 项既有测试通过。
- 首轮测试发现假 DNS 未正确剥离 EDNS 附加段，修正测试 fixture 后重跑通过；没有将首轮失败作为线上 DNS 故障处理。
- 部署时完整应用 CI、出口审计及嵌入构建仍在运行；本次未修改或部署应用程序，不将已通过的两项运维 job 表述为所有 CI 已通过。本机未运行编译、测试、Lint、类型检查或测试容器。

## 已安装

| 文件 | 用途 |
| --- | --- |
| `/usr/local/libexec/mihomo_dns.py` | DNS 就绪检查、原子更新运行文件、SIGHUP 重载 |
| `/etc/dnsmasq-claude.conf` | 独立普通上游与 `servers-file`，已删除原静态 Claude 转发 |
| `/etc/systemd/system/mihomo.service.d/dns-lifecycle.conf` | DNS 后启动，ExecStartPost 启用转发、ExecStopPost 撤销转发 |
| `/etc/systemd/system/dnsmasq-claude.service.d/mihomo-dns.conf` | DNS 启动前重建与当前 Mihomo 状态相符的运行文件 |

运行文件是 `/run/mihomo-dns/servers.conf`，由 root 管理且允许 dnsmasq 降权后读取；它不随 Mihomo 的 RuntimeDirectory 被删除。安装后 daemon-reload，并仅重启 dnsmasq 一次，使主配置的 servers-file 生效。以后切换发 SIGHUP，不反复重启 DNS。

## 线上验收

- Mihomo 为 inactive，未为验证而启动或制造线上进程崩溃。
- dnsmasq PID 从 310118 变为 587559，active；启动日志明确输出 `Claude DNS upstream: ordinary`，上游仅 `100.100.2.136`、`100.100.2.138`。
- `/run/mihomo-dns/servers.conf` 为空，未保留任何转给 1053 的规则。
- `api.anthropic.com`、`claude.ai`、`claude.com` 返回真实地址，均不再返回 `198.18.0.0/16` Fake-IP；`api.openai.com` 解析正常。此处只验证 DNS，不证明官方服务直连可用。
- Sub2API PID 保持 583415，未重启，GET `/health` 为 `{"status":"ok"}`。
- 保护保持 `consistent=true`、`claude-only`、`confirmed`，未调整 nft、路由或账号绑定。
- 未发送真实模型请求；完整启停和失败场景在 Actions 执行，未在生产环境反复演练。

## 保留的行为

系统 DNS 现在随 Mihomo 启停切换。账号绑定代理及失效时不降级的应用策略不变；应用对未绑定官方 Claude 账号的默认出口仍指向本机 Mihomo，停止后该应用路径会报错，不因 DNS 切换自动直连。

客户端自己缓存的 Fake-IP 和已建立的连接不受 helper 强制清理；必要时等待 TTL 或重建客户端连接。helper 跟随 systemd 生命周期，未对主进程仍存活但内部 DNS 持续失效的场景增加周期健康监视。

## 备份与回退

root 私有备份：`/root/mihomo-dns-lifecycle-20261105.x7kpn54m`，包含原主配置、resolv.conf、服务与已有 drop-in、变更前后状态、已安装候选文件、对应 Actions 日志、文件摘要及线上 DNS 结果。

如需回退，先核对没有后续修改：恢复备份中的 `/etc/dnsmasq-claude.conf`，删除本次新装的两个 drop-in，daemon-reload 后重启 dnsmasq；再移除不再引用的 helper。保留其他 drop-in 和既有保护。回退旧主配置会恢复“始终指向 Mihomo”的旧行为，因此 Mihomo 停止时官方域名解析失败也会恢复。未执行线上回滚演练。
