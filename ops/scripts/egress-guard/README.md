# Claude 定向出口保护

本机使用 v2 `claude-only` 策略。Mihomo 将官方 Claude 域名解析到 Fake-IP 网段，普通域名沿用系统出口。Mihomo 停止后，独立 DNS 转回普通解析；保护服务继续阻止已有 Fake-IP 流量经其他接口送出。应用中绑定了代理的账号继续使用该代理。[Mihomo 配置模板](../../config/mihomo/config.yaml.example) · [应用模板](../../config/application.yaml.example)

## 从部署备份重建

日常应用备份另需配套保存代理私有配置和保护状态。重建时恢复本机私有的应用、代理凭据和 `/var/lib/sub2api-egress/state.json`。保护目录属主为 root、权限 0700，状态文件属主为 root、权限 0600。`restore` 从 v2 状态恢复黑洞路由、定向策略路由及 nftables 保护表。旧版已确认的 v1 部署可按历史迁移手册升级。[历史归档](../../archive.md)

| 源文件 | 安装位置 |
|---|---|
| [出口保护程序](egress_guard.py) 和 [控制程序](mihomo_control.py) | `/usr/local/libexec/`，root/0755 |
| [控制命令](mihomo-ctl) | `/usr/local/sbin/mihomo-ctl`，root/0755 |
| [SSH 回程脚本](../mihomo-ssh-route) | `/usr/local/libexec/mihomo-ssh-route`，root/0755 |
| [保护单元](../../config/systemd/sub2api-egress.service)、[Mihomo 单元](../../config/systemd/mihomo.service)、[DNS 单元](../../config/systemd/dnsmasq-claude.service) | `/etc/systemd/system/`，root/0644 |
| [启动依赖](../../config/systemd/guard-dependency.conf) | `sub2api.service.d/egress-guard.conf` 和 `mihomo.service.d/egress-guard.conf`，位于 `/etc/systemd/system/` |
| [networkd 配置](../../config/systemd/networkd.conf.d/70-mihomo-routing.conf) | `/etc/systemd/networkd.conf.d/70-mihomo-routing.conf` |
| [系统解析器配置](../../config/resolv.conf) | `/etc/resolv.conf`，在本机 DNS 就绪后切换 |

把脱敏代理模板中的节点参数换成私有值，安装到 `/etc/mihomo/config.yaml`，权限 0600。按 DNS 手册安装 dnsmasq 配置、生命周期脚本和两个 drop-in，再执行 `systemctl daemon-reload`。先恢复并启动保护服务与 DNS，再按部署记录恢复应用和 Mihomo 的启停状态。[DNS 生命周期与安装位置](../mihomo-dns/README.md) · [部署记录](../../deployed-build.json)

## 验收

运行 `sudo /usr/local/libexec/egress_guard.py verify`，核对 `mode=claude-only`、`phase=confirmed` 和 `consistent=true`。用 `sudo mihomo-ctl status` 确认代理状态。`mihomo-ctl stop` 只停止代理，保护与独立 DNS 继续运行。恢复后检查本机应用健康、普通域名解析、SSH 回程，以及需要使用的 Claude 业务路径。
