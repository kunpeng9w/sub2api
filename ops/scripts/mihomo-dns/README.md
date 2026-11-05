# Mihomo 生命周期与 Claude DNS

系统 DNS 始终由 `dnsmasq-claude` 在 `127.0.0.1:53` 提供，普通上游保持独立。只有 Mihomo 启动且其 DNS 端口就绪后，才将 `anthropic.com`、`claude.ai`、`claude.com` 的解析交给 Mihomo。停止、崩溃、启动失败后移除专用转发，恢复普通上游。

当前是待部署实现，执行结果另行记录；源码提交或 CI 不会自动修改线上配置。

## 实现与边界

- `mihomo_dns.py on` 是 Mihomo 的 `ExecStartPost`，最多等待 8 秒确认本机 DNS 可回答，再原子替换 `/run/mihomo-dns/servers.conf` 并向 dnsmasq 发 SIGHUP。未就绪则保留普通解析并使启动失败。
- `off` 是 `ExecStopPost`，正常停止、崩溃和启动失败均移除转发；它不依赖主进程仍然存在。
- dnsmasq 的 `ExecStartPre=... prepare` 根据运行中的 Mihomo 及 DNS 就绪状态重建运行文件，覆盖开机和 dnsmasq 单独重启；不等待另一个 systemd 启动任务，避免启动顺序死锁。
- Mihomo 在 dnsmasq 之后启动。后续 DNS 切换仅发 SIGHUP，不反复重启 dnsmasq。运行目录不归 Mihomo 的 RuntimeDirectory 所有，不会随其停止而删除。
- 独立文件由 root 管理，目录 0755、文件 0644，使 dnsmasq 降权后仍可重读；flock 串行化更新。主配置的静态域名转发必须删除，否则清空运行文件仍会继续使用旧上游。

[dnsmasq 官方手册](https://dnsmasq.org/docs/dnsmasq-man.html)说明 SIGHUP 会重读 `servers-file`，但不会重读普通主配置；因此首次安装需重启 DNS 服务一次。

此处只切换 DNS。账号绑定代理、绑定失效即报错、应用 `claude_egress` 默认显式代理和 Fake-IP 保护不变。未绑定官方 Claude 账号的应用默认出口仍指向 Mihomo，停止后仍报错；不会因 DNS 切换悄悄把该应用请求变成直连。普通系统程序在 Mihomo 停止后可解析到真实官方地址，符合本次 DNS 生命周期要求。

dnsmasq 的缓存随 SIGHUP 清理；其他进程已缓存的 Fake-IP 或现存连接不能由 DNS helper 强制清除，必要时等待 TTL 或重建客户端连接。这里跟随 systemd 生命周期，未对仍存活进程内部的持续 DNS 故障实现额外健康监视。

## 安装位置与恢复

安装前备份下列实际文件及 drop-in；保留 Mihomo 当前启停状态和应用进程。线上操作应在 GitHub Actions 对应提交验证后执行：

| 源文件 | 安装位置 |
| --- | --- |
| `mihomo_dns.py` | `/usr/local/libexec/mihomo_dns.py`，root/0755 |
| `dnsmasq-claude.conf` | `/etc/dnsmasq-claude.conf`，root/0600 |
| `dnsmasq-lifecycle.conf` | `/etc/systemd/system/dnsmasq-claude.service.d/mihomo-dns.conf`，root/0644 |
| `mihomo-lifecycle.conf` | `/etc/systemd/system/mihomo.service.d/dns-lifecycle.conf`，root/0644 |

安装所有文件，`systemctl daemon-reload`，再重启 dnsmasq 一次，使新主配置生效。Mihomo 当前停止时不应为安装而启动它。回退时恢复备份主配置和旧 drop-in，daemon-reload 后重启 dnsmasq；若回退至原固定转发配置，Mihomo 停止时 Claude DNS 解析失败也会恢复，不能视为普通解析回退。

## 验证

`.github/workflows/ops-ci.yml` 的 `mihomo-dns` job 在 GitHub runner 安装 dnsmasq-base，运行单元测试和真实 systemd/dnsmasq 集成测试。测试仅使用本地假 DNS、临时目录、独立命名的临时服务；覆盖停止状态启动 DNS、启动/停止/重启 Mihomo、DNS 单独重启、Mihomo 崩溃、启动失败、降权后的 SIGHUP 重读及缓存切换。不会联系公网 DNS 或操作已安装的线上服务。

生产只做授权的安装与正常状态验收，不运行这些测试，也不为验证注入线上崩溃。检查当前停止状态的官方域名已恢复真实解析、普通域名仍正常、保护一致且应用健康；完整启动/停止/故障场景由 Actions 验证。
