# Mihomo 定向规则修复验收

2026-11-05 用户授权执行后，**修复完成，正常运行验收通过**。networkd 于新加坡时间 15:15:03 重启，15:15:38 完成 DNS、路由、保护及 TLS 检查。

## 已执行的变更

- 使用已安装 v2 `egress_guard.py restore` 恢复 main 表黑洞路由和 `pref 2000` 定向规则，保留 `confirmed/claude-only` 状态，重建原保护表及指纹。
- 使用已有 `mihomo-ssh-route add` 恢复 IPv4/IPv6 优先级 100 的 SSH 回程规则。
- 安装 [70-mihomo-routing.conf](70-mihomo-routing.conf) 到 `/etc/systemd/networkd.conf.d/70-mihomo-routing.conf`，root 所有、权限 0644；安装前同名文件不存在，未覆盖其他配置。
- 核对合并配置的两个清理选项均为 `no`，通过一次性 systemd 单元重启 networkd，随后验收成功；无需重启 Mihomo。

## 验收结果

| 项目 | 结果 |
| --- | --- |
| networkd 重启 | 一次性操作单元 `Result=success`、退出码 0；networkd PID 从 558193 变为 582553，active |
| Sub2API / Mihomo / DNS | PID 分别保持 553061 / 314955 / 310118，全部 active |
| 出口保护 | `consistent=true`、`mode=claude-only`、`phase=confirmed`，Mihomo active |
| 定向与 SSH 规则 | networkd 重启后 IPv4 pref 2000、IPv4/IPv6 pref 100 均保留 |
| 黑洞路由 | `blackhole 198.18.0.0/16 metric 42760` 保留 |
| Fake-IP 路由 | `198.18.1.1 via 198.18.0.2 dev mihomo table 2022` |
| 普通公网路由 | `1.1.1.1` 仍经 eth0 |
| DNS | api.anthropic.com / claude.ai 为 Fake-IP，api.openai.com 为真实公网地址 |
| TUN HTTPS | api.anthropic.com 根路径 HEAD 返回 404、证书验证结果 0、连接地址 `198.18.0.4` |
| 显式代理 HTTPS | 经 `127.0.0.1:7890` 的 CONNECT 返回 200，根路径 HEAD 为 404、证书验证结果 0 |
| nft 观察 | TUN 探测后允许计数 26 包 / 5726 字节，拒绝计数 0；未清空全机防火墙 |
| 应用健康 | GET `/health` 返回 `{"status":"ok"}` |
| 原用量刷新交付预检 | `deploy.sh --check` 退出 0，输出 `Preflight passed. No files installed and no services stopped.` |
| networkd 本次启动日志 | DHCP 地址/网关恢复；样本未出现之前的策略规则清理错误 |

官方根路径 404 是连通性观察，不是模型请求失败；本次没有调用模型，也不代表账号鉴权、推理或真实用量刷新已验收。

## 备份与回退

root 私有备份目录：`/root/mihomo-routing-20261105.87x7rloe`。保存变更前保护状态、已安装 helper、networkd 原配置及合并视图、路由/规则、防火墙快照、服务 PID，以及变更后快照、networkd 日志、metadata 和部署预检结果。也保存了更新前的用量刷新交付说明和 validation.json。

回退配置的条件与步骤见 [操作单](README.md)。回退时只移除本次新装且未被后续修改的配置文件，在维护窗口重启 networkd 并恢复/验证保护；不把原先已缺失规则的故障快照灌回系统。回退会重新引入默认清理风险。

## 范围

本次仅修复网络分流与其持久配置。Mihomo 保持运行，应用程序与账号绑定未变，数据库未修改。原用量刷新交付的网络预检阻塞已经解除；未执行应用 `--apply`，未部署用量刷新修正。

未运行本机编译、测试、Lint 或类型检查；本次未推送或执行 GitHub Actions。验收是实际线上网络配置和服务状态核对，不能表述为源码测试通过。已验证一次 networkd 重启后的规则保留，未执行整机重启、停用 Mihomo、故障注入或回滚演练。
