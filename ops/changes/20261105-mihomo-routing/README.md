# Mihomo 定向规则丢失排查（2026-11-05）

状态：**已获用户授权完成线上修复，2026-11-05 15:15 验收通过；Mihomo 保持运行。** 已恢复规则、安装 networkd 配置并重启 networkd，Sub2API/Mihomo/DNS 服务保持原进程。未替换应用程序或修改数据库。时间均为新加坡时间。执行结果和备份见 [修复验收](POST-DEPLOY.md)；下文故障现场与操作单保留用于追溯。

后续更新：用户随后停止 Mihomo，并要求 DNS 随其生命周期切换；2026-11-05 15:41 已完成该变更，当前 Mihomo 停止时使用普通 DNS。见 [DNS 生命周期验收](../20261105-mihomo-dns-lifecycle/POST-DEPLOY.md)。本目录此前的固定 DNS 依赖说明保留为历史现场。

## 结论与证据

最可能的原因是自动更新后的 `systemd-networkd` 重启清理了外部路由和策略规则。更新与重启有直接日志；具体每条规则的删除没有审计记录，因此不能断言已经逐条证实。

- `/var/log/apt/history.log`：07:00:24 记录升级 `libpcre2-8-0`；对应 dpkg 日志批次为 07:00:21–07:00:37。不是 Mihomo 更新，也不是本次升级 systemd 软件包。
- `/var/log/unattended-upgrades/unattended-upgrades-dpkg.log`：该批次 `needrestart` 的命令明确包含 `systemctl restart ... systemd-networkd.service ...`。
- networkd journal：07:00:30 启动；报告 `Could not drop routing policy rule: Missing goto target for action goto. Invalid argument`，证明它尝试清理策略规则，不证明某条指定规则已被删除。
- 本机 systemd 为 `255.4-1ubuntu8.18`；没有覆盖默认 `ManageForeignRoutingPolicyRules=yes`、`ManageForeignRoutes=yes` 的配置。eth0 由 `/run/systemd/network/10-netplan-eth0.network` 管理，文件只配置 DHCP 和链路本地地址，没有登记外部保护规则。
- Mihomo 自 10 月 29 日 13:20:03 运行至检查时；Sub2API 本次进程启动于 11 月 5 日 03:30:13。未见此次更新重启 Mihomo 的证据。

[systemd 255 官方说明](https://www.freedesktop.org/software/systemd/man/255/networkd.conf.html)确认两个选项默认清理非 networkd 配置的规则/路由；设为 `no` 可保留它们。

15:00 左右的现场状态：

| 检查项 | 结果与影响 |
| --- | --- |
| 策略规则 `pref 2000 to 198.18.0.0/16 lookup 2022` | 缺失；Fake-IP 查不到 Mihomo 的表 |
| main 表 `blackhole 198.18.0.0/16 metric 42760` | 缺失；兜底保护不完整 |
| IPv4/IPv6 `pref 100 ipproto tcp sport 22 lookup main` | 均缺失；当前定向模式下普通公网仍走 main，因此 SSH 暂未受影响 |
| Mihomo 表 2022 | 仍有 `198.18.0.0/16 via 198.18.0.2 dev mihomo` |
| Mihomo 9000 段策略规则 | 只剩 `9001 iif mihomo goto 9010 [unresolved]`；不应仅凭进程 active 判断规则完整 |
| `ip route get 198.18.1.1` | 经 eth0；与预期经 mihomo 不符 |
| 普通公网路由 | `1.1.1.1` 经 eth0，正常 |
| nft `inet sub2api_egress_guard` | 仍存在，拒绝 Fake-IP 从非 mihomo 接口出站；本次未见这张表被清空 |
| guard verify / mihomo-ctl status | `persistent reject route is missing`，不能通过部署预检 |
| DNS | api.anthropic.com 返回 `198.18.0.4`；api.openai.com 返回真实公网地址 |
| 应用 `/health` | `{"status":"ok"}`；健康接口不能证明上游分流正常 |

缺失定向规则时，Fake-IP 被选到 eth0，但现存 nft 仍会拒绝这些包。未证实发生业务流量直连泄漏。显式使用 `127.0.0.1:7890` 的应用请求与依赖 Fake-IP/TUN 的请求路径不同，不能由本故障推断所有代理请求均失败。

## 修复操作单（本次执行结果见修复验收）

建议先恢复既有保护，再在可用云控制台的维护窗口启用持久配置。无需停用 Mihomo、调整账号绑定或替换 Sub2API 程序。

先保存当前状态，使用 root 私有备份目录；保留其实际输出路径：

```bash
sudo -i
repair_backup=$(mktemp -d /root/mihomo-routing-20261105.XXXXXX)
chmod 700 "$repair_backup"
cp -a /var/lib/sub2api-egress "$repair_backup/"
ip -4 rule show > "$repair_backup/rules-v4.txt"
ip -6 rule show > "$repair_backup/rules-v6.txt"
ip -4 route show table all > "$repair_backup/routes-v4.txt"
nft list ruleset > "$repair_backup/nft-before.txt"
systemd-analyze cat-config systemd/networkd.conf > "$repair_backup/networkd-before.txt"
printf '%s\n' "$repair_backup"
```

仍在同一 root shell 中，确认 `pref 2000` 未被其他用途占用、表 2022 只含当前 Mihomo 的定向路由，然后通过**已安装的 v2 helper**恢复：

```bash
ip -4 rule show pref 2000
ip -4 route show table 2022
/usr/local/libexec/egress_guard.py restore
/usr/local/libexec/egress_guard.py verify
/usr/local/sbin/mihomo-ctl status
```

`restore` 保留 `confirmed/claude-only` 状态，恢复黑洞路由、优先级 2000 的规则及既有保护表，并更新指纹。它不重启 Sub2API 或 Mihomo。预期 `consistent=true`，Mihomo 为 active。失败时停止后续操作，不绕过启动前验证，不删除保护表或黑洞路由。

恢复 SSH 回程规则前核对 IPv4、IPv6 的优先级 100 都为空；有其他规则时不要调用 add，不覆盖其用途：

```bash
ip -4 rule show pref 100
ip -6 rule show pref 100
/usr/local/libexec/mihomo-ssh-route add
```

接着安装本目录 [70-mihomo-routing.conf](70-mihomo-routing.conf)，路径必须不存在；已有同名配置时先审阅、备份再决定如何合并，不能直接覆盖：

```bash
test ! -e /etc/systemd/networkd.conf.d/70-mihomo-routing.conf || exit 1
test ! -L /etc/systemd/networkd.conf.d/70-mihomo-routing.conf || exit 1
install -d -o root -g root -m 755 /etc/systemd/networkd.conf.d
install -o root -g root -m 644 \
  /home/pk/workspace/sub2api/ops/changes/20261105-mihomo-routing/70-mihomo-routing.conf \
  /etc/systemd/networkd.conf.d/70-mihomo-routing.conf
systemd-analyze cat-config systemd/networkd.conf
```

以上命令应逐项成功后才继续；确认合并后的最后生效设置均为 `no`。这两个选项影响 networkd 对全机外部路由/规则的清理，保留后它们由创建者负责删除；这是与 Mihomo、保护 helper 协调所有权的选择。

**systemd 255 的普通 reload 不足以启用这两个全局选项。** [v255 的 manager_reload 源码](https://github.com/systemd/systemd/blob/v255/src/network/networkd-manager.c#L995-L1023)只重新加载 netdev/network，未重新读取全局配置。`systemctl daemon-reload` 也不使它们生效。配置安装后需在维护窗口执行以下重启，并复查恢复结果；远程主机应先确认云控制台可用，不能承诺网络服务重启完全不中断连接：

```bash
systemctl restart systemd-networkd.service
/usr/local/libexec/egress_guard.py verify
/usr/local/sbin/mihomo-ctl status
ip -4 rule show
ip -6 rule show pref 100
ip -4 route show table main exact 198.18.0.0/16
ip -4 route get 198.18.1.1
ip -4 route get 1.1.1.1
dig +time=2 +tries=1 +short @127.0.0.1 api.anthropic.com A
dig +time=2 +tries=1 +short @127.0.0.1 api.openai.com A
curl -fsS --max-time 5 http://127.0.0.1:8080/health
journalctl -u systemd-networkd --since '5 minutes ago' --no-pager
```

预期 Fake-IP 经 mihomo、普通公网经 eth0、黑洞路由和 100/2000 规则保留，guard 一致。DNS 仍应区分官方域名 Fake-IP 和普通公网地址。该路径由 `pref 2000` 直接选择表 2022，无需手工猜测补齐 9000 段规则；将来 Mihomo 正常重启时会重建自身规则。

若重启后仍有规则缺失，先在控制台检查新配置是否被覆盖，保留日志，再运行已有 helper 恢复；不能宣称持久修复验收通过。无需为了验收主动停止线上 Mihomo、删除 TUN 或制造生产故障。

配置回退仅移除本次新装且内容仍与候选一致的 `70-mihomo-routing.conf`，在维护窗口重启 networkd，并再次运行 guard restore/verify。回退恢复默认清理行为，因此也恢复本故障风险。不要把故障前备份直接灌回路由/防火墙；不要撤销已恢复的保护来模拟原故障。

保护修复验收通过后，原 OpenAI 用量刷新交付的 `deploy.sh --check` 可再执行；网络修复不代表应用已部署，也不授权执行 `--apply`。

## 后续停用 Mihomo 的依据

本次只读数据库聚合显示：当前未删除账号共 4 个，均为 OpenAI；3 个 OAuth 账号绑定外部地址代理，1 个 API Key 账号未绑定代理。未发现未删除的 Claude 账号。未删除代理共 7 个，均为 active、无到期时间、地址非本机；这些仅是配置盘点，**未验证代理可连接或路径独立**，没有输出凭据或代理地址。

应用仍配置 `gateway.claude_egress.enabled=true`、入口 `127.0.0.1:7890`；系统 DNS 仍经 `dnsmasq-claude` 把官方 Claude 域名转给 Mihomo 的 1053 端口。Sub2API 和 Mihomo 仍依赖 `sub2api-egress.service`，启动前验证保护。已检查的 systemd、cron、全局环境和用户 service 文件中，相关引用集中在这些单元；这不是对所有用户程序或外部客户端的完整依赖证明。

因此停用条件比原先预想简单，但不能只执行 `systemctl stop mihomo`。后续应另行完成：

1. 验证实际使用账号的外部代理可独立工作，并盘点其他客户端是否依赖本机代理/DNS。保留账号代理绑定及失效即报错的应用行为。
2. 备份配置，关闭应用 Claude 默认 Mihomo 出口；在一次受控应用重启中切换，避免仍运行的旧配置继续连接本机代理。
3. 将系统 DNS 改为不依赖 Mihomo 的解析路径，再撤掉官方域名专用转发；核对普通解析和残留 Fake-IP 缓存。不能先停 DNS 上游。
4. 精确撤销 Sub2API/Mihomo 的 guard 启动依赖及验证 hook；保留审查过的账号代理策略。
5. 停止并禁用 Mihomo，精确撤销其保护表、2000 规则、黑洞路由及回程规则；确认路由、DNS 和应用出口后停用不再需要的保护/DNS单元。不得清空全机 nftables 或覆盖其他服务规则。
6. 确认没有其他外部路由创建者需要保留选项后，单独回退本次 networkd drop-in；记录云控制台恢复与反向切回步骤。

线上安装的是 v2 控制器：`mihomo-ctl stop` **保留 Claude 保护和独立 DNS**，且当前会先因 verify 失败而拒绝执行。不能套用仓库旧 v1“stop 即解锁”的说明，也不能为了停用安装回旧版 helper。

## 验证范围

初次排查只执行源码/配置/日志阅读、数据库只读聚合、路由和服务状态检查、DNS 查询及健康接口 GET。用户随后授权修复；已恢复规则、安装配置、重启 networkd 并完成线上验收，详见 [修复验收](POST-DEPLOY.md)。未重启 Mihomo、进行代理故障演练或发送真实模型请求。未运行本机编译、测试、Lint 或类型检查；未推送提交或执行本次 GitHub Actions，线上操作验收不代表代码测试或 CI 通过。已验证本次 networkd 重启后规则保留，未执行整机重启验收。
