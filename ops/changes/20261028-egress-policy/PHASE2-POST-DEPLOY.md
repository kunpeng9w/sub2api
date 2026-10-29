# 第二阶段上线验收（2026-10-29）

结果：**第二阶段已切换成功，正常运行状态验收通过。** 切换于新加坡时间 2026-10-29 11:41:46–11:41:55 完成；最终复查开始于 11:58:29（UTC 03:58:29，服务器本地为 10 月 28 日 23:58:29 EDT）。完整脱敏证据见 [PHASE2-POST-DEPLOY-EVIDENCE.json](PHASE2-POST-DEPLOY-EVIDENCE.json)。用户在手动准备遇阻后授权代为执行；暂停期间服务保持运行，恢复后完成日志核对及归档。

运行应用仍为 `1dce7da6bcd3330c9b4ce92e6e5aa6a8746e2eaf`，本阶段没有替换二进制。网络模板对应 `095d0913cc0cec3bb9c77d47e178dabc6b17f422`；其 [Actions 网络模拟](https://github.com/kunpeng9w/sub2api/actions/runs/40813231305) 15 项通过。应用完整构建和安全扫描沿用 [安全发布证据](../20261029-security-release/README.md)，本阶段没有在低内存服务器重新编译。

## 现场验收

| 项目 | 结果 |
| --- | --- |
| 运行程序身份 | `/proc/310143/exe` SHA256 为 `6535d20036928c966bcd10c447d8449ae4d7c342a2baea1f2dcc72583f6706d0`，与 CI 产物一致 |
| 服务 | 应用、Mihomo、dnsmasq、保护、Nginx、PostgreSQL、Redis、resolved、云助手均 active；自动重启计数均为 0 |
| 开机启用 | sub2api、mihomo、dnsmasq-claude、sub2api-egress 均 enabled；未实际重启主机验证 |
| 健康 | 本机和公网 **GET** `/health` 均 HTTP 200、`status=ok`；公网 TLS 校验通过 |
| 应用配置 | 与私有候选一致，`gateway.claude_egress.enabled=true` |
| DNS | api.anthropic.com、claude.ai、claude.com 返回 `198.18.0.0/16` 地址；api.openai.com、example.com 返回公网真实地址 |
| TUN 范围 | 路由表 2022 仅含 `198.18.0.0/16`；Fake-IP 走 mihomo，普通地址和阿里云内网软件源走 eth0 |
| SSH 回程 | IPv4/IPv6 均保留优先级 100、TCP 源端口 22、main 表规则 |
| 出口保护 | `claude-only`、`confirmed`、`consistent=true`，实际安装脚本与候选一致 |
| 配置安装 | 应用、Mihomo、dnsmasq、服务单元及 resolv.conf 与私有候选/冻结模板一致 |
| 账号绑定 | 切换时保存的前后 CSV 逐字节相同；未将此历史比较表述为新的数据库快照 |
| 备份 | 有效重试备份的摘要重新校验通过 |

早期 HTTP 探测全部使用 HEAD：公网首页及原先无法访问的软件包地址返回 200；官方域名根路径经 TUN 或显式代理返回 404，但 TLS 校验成功，显式代理 CONNECT 为 200。HEAD `/health` 的 404 不作为健康结论；最终使用 GET 验证为 200。以上官方根路径探测没有调用模型，也不能证明模型鉴权和推理成功。

## Mihomo 日志提示核对

当前进程启动后只有一条 WARNING，发生于 UTC 03:41:53.136436：

```text
[TUN] default interface changed by monitor, => eth0
```

核对 [Mihomo v1.19.33 源码](https://github.com/MetaCubeX/mihomo/blob/v1.19.33/listener/sing_tun/server.go#L366-L374)：默认网卡监视回调在网卡非空时以 WARNING 记录名称，随后刷新网卡缓存和 DNS 连接；网卡丢失另有 ERROR 分支。结合仅在启动时出现一次、当前普通流量走 eth0、Fake-IP 走 mihomo、DNS 与 HTTPS 检查均通过，判断这是本次启动的网卡选择通知，无需修改配置。未将 WARNING 隐藏或改记为 INFO。

最终日志样本从各当前进程启动时间开始：sub2api 为 206 条 INFO；Mihomo 为 14 条 INFO、上述 1 条 WARNING；dnsmasq 为 8 条 INFO；云助手为 3 条 INFO。该样本未见 ERROR/FATAL/PANIC，未见其他 WARNING；不代表未来持续监控。

## 执行中的问题和修复

1. 手动准备时 shell 变量丢失，导致脚本和配置路径退化到根目录。操作单补齐上下文及路径检查；错误产生的 `/phase2` 不作为有效备份。
2. 原全量 TUN 下，阿里云内网镜像 `100.100.2.148:80` 连接失败。通过可用公共镜像取得相同版本 `dnsmasq-base 2.91-0ubuntu0.24.04.2`，与 APT 索引摘要 `2af43430c69bcb25078975c613b49288bc0474b98fc97af6e1e059dc973416f1` 核对后安装。成功切换后内网源直连 HEAD 返回 200。
3. 首次实际切换于 UTC 03:30:25–03:30:40 触发 `SSH return rule changed`，自动回滚成功，恢复第一阶段。原因是旧 Mihomo 停止钩子移除了 SSH 回程规则，新单元遗漏恢复钩子。
4. `095d0913c` 恢复 IPv4/IPv6 SSH 规则的启动/停止钩子，加入回归并补齐备份范围。15 项模拟和独立 Actions 通过后，使用全新备份重试成功。首次失败与回滚记录保留，不计为成功验收。

## 回滚和验证边界

本次有效第二阶段备份目录：`/root/sub2api-phase2-20261029.retry.zepmvg`。目录内保留原配置、安装文件归档、resolv.conf 符号链接、账号绑定快照、摘要、执行日志和回滚脚本。首次失败资料保留在 `/root/sub2api-phase2-20261029.480bTL`；第一阶段应用备份仍在 `/root/sub2api-egress-20261029.FAbSXj`。

如需恢复第二阶段切换前的网络与第一阶段应用配置，可在维护窗口执行本次冻结的回滚脚本（会短暂停止相关服务）：

```bash
sudo /usr/bin/python3 /root/sub2api-phase2-20261029.retry.zepmvg/switch-phase2.py --rollback
```

该回滚不降级应用二进制；恢复原 DNS、网络保护及第一阶段配置。通用步骤见 [回滚教程](ROLLBACK.md)。首次失败已实际走通自动回滚；本次成功后的在线状态没有再进行故障注入或回滚演练。

本验收覆盖正常运行时的服务、DNS、路由、SSH 规则、保护一致性和 HTTP/TLS 连通性；未执行计费模型请求、主机重启或代理故障注入。自带 DoH、固定 IP、其他远端代理的客户端仍需按既有范围单独纳管。

## 后续真实业务验证

新加坡时间 2026-10-29 12:41:48，用户授权的单次真实流式请求通过，见 [真实业务验证](BUSINESS-SMOKE.md)。覆盖现有 OpenAI 兼容渠道及 simple 模式用量记录；尚无官方 Claude 账号可用于推理验收。上述基础验收的历史范围保持不变。
