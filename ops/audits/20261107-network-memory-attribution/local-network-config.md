# 本机网络配置检查

2026-11-07 09:11–09:14，Asia/Singapore。只读检查及少量 ICMP/UDP 路径探测；未修改网络参数、服务配置或重启。保留所有既有未提交文件。本次不涉及项目编译、单元测试、Lint 或 CI。

结论：未发现能够解释此前慢上传的本机网络配置错误。不能据此完全排除内核/虚拟网卡运行时问题，也不能把云平台或运营商责任当作已证实。

| 项目 | 观察及解释 |
|---|---|
| 网卡 | eth0，virtio_net，UP，MTU 1500，单收发队列。驱动 RX drops=0，TX timeouts=0；接口 errors/dropped=0。未见当天匹配 NETDEV/virtio/eth0/conntrack/TCP/watchdog 的内核日志。 |
| 路由 | DHCP 默认路由经 eth0；没有活动 TUN。额外策略和黑洞仅针对 198.18.0.0/16 fake-IP 范围，不影响已探测公网地址。 |
| 网络管理 | systemd-networkd active，NetworkManager、Mihomo inactive；Netplan DHCP4，DHCP6 关闭，运行配置一致，无自定义 MTU/限速。 |
| 防火墙 | nft 仅见 fake-IP 出口保护；非 mihomo 出口拒绝计数为 0。iptables-save/ip6tables-save 输出为空。未见公网重定向、MSS 修改或限速规则。 |
| 流量控制 | fq_codel，drops/overlimits/requeues/backlog=0，无 class/filter 或 ingress/egress filter。 |
| TCP | cubic，窗口缩放/SACK/timestamps/接收自动调节开启；tcp_rmem 最大约 6 MiB、tcp_wmem 最大 4 MiB，未见显式发送速率限制。core 缓冲值 212992 不能直接视为 TCP 自动调节上限。 |
| MTU | ip_no_pmtu_disc=0，PMTU discovery 未关闭；tcp_mtu_probing=0 表示未启用 TCP 层黑洞探测，不等于关闭普通 PMTU discovery。该设置本身不证明异常。 |
| offload | TSO/GSO/GRO 与校验和卸载开启，未作开关对照；不能仅凭开启状态断言驱动无缺陷。 |
| 服务隔离 | sub2api PrivateNetwork=no，无 NetworkNamespacePath 或 IPAddressAllow/Deny；未列出命名 netns。 |

本机 sysctl 有既有调优：tcp_max_tw_buckets=5000、tcp_synack_retries=2、tcp_max_syn_backlog=1024、tcp_slow_start_after_idle=0。检查时 TIME_WAIT 24、orphan 0，未见这些参数造成当前大请求上传缓慢的证据。rp_filter 持久配置存在不同文件赋值，但当前实际 all/eth0 均为 0；不应把配置文件中的覆盖关系直接判为网络故障。

MTU 探测：向默认网关及 1.1.1.1 各发 3 个 1472 字节 payload、DF 的 IPv4 ICMP 包（IP 包 1500 字节），均 3/3 成功；平均 RTT 分别 0.076 ms、2.447 ms。tracepath 限制 6 跳，显示 PMTU 1500，未到达终点。不能将这组探测推广到代理路径，也不能用 3 个包证明长期无丢包。

内核累计 IpOutDiscards=114、ListenDrops=2，但它们是历史累计，未定位时间与原因。随后 10 秒自然流量窗口的 OutDiscards、RetransSegs、TCPTimeouts、ListenDrops、TCPMemoryPressures、TCPBacklogDrop、TCPRcvQDrop 与 softnet dropped 增量全部为 0；该窗口没有重做上传，不能覆盖此前异常。累计 TCPMemoryPressures、TCPBacklogDrop、TCPRcvQDrop 为 0，不能据此否定整机曾有 Swap 压力。

当前更合理的下一步是云侧出口限速/丢弃指标和代理端收包观测，或第二台服务器同目标对照。尚无证据支持直接改 MTU、关闭 offload、切换拥塞算法或重启来修复。

TCP 参数解释参考 Linux 官方文档：https://docs.kernel.org/networking/ip-sysctl.html
