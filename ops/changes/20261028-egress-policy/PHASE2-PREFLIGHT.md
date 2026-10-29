# 第二阶段切换前核对（2026-10-29）

记录时间：2026-10-29T09:49:01.213418+08:00。

状态：**候选保护兼容性已修复，13 项纯模拟及 Mihomo 配置语法预检通过。线上仍为第一阶段，网络切换由用户手动执行。** 使用 [第二阶段操作单](PHASE2-MANUAL.md)，先安装缺少的 DNS 依赖，完成全部现场预检后再切换。

- 候选网络脚本与操作单提交：`c73e3dbb70f9af53eb1b326b631bf1e0c4711551`。
- 已上线应用继续使用 `1dce7da6bcd3330c9b4ce92e6e5aa6a8746e2eaf`。
- [独立 Actions 网络模拟](https://github.com/kunpeng9w/sub2api/actions/runs/40802167616)：13 项通过，包含部署/回滚 Bash 代码块的语法检查。通过 `network_only=true` 运行，未重新构建应用，未执行真实 nft/ip/systemctl 修改。
- 本机 iproute2 输出采用独立 `dst` / `dstlen`，旧候选只接受 CIDR 字符串。先用真实格式复现 4 个错误，再修复兼容判断，继续拒绝错误网段、路由表及重复优先级。
- 候选保护脚本 SHA256：`e85cacdb1456abaed0152d604229aab098880f8e85de8d0488fbb746d037f049`。操作单在停服前校验该摘要，避免误装旧候选。

只读现场预检：

| 项目 | 结果 |
| --- | --- |
| 第一阶段应用、Mihomo、保护、Nginx、PostgreSQL、Redis | active |
| 现有保护 | confirmed，规则一致 |
| Mihomo | v1.19.33；全量 TUN，DNS redir-host |
| Mihomo 候选语法 | root 私有临时候选 `-t` 通过，临时文件已清理 |
| DNS 依赖 | dnsmasq-base 尚未安装；APT 模拟仅新增该包，0 升级、0 删除 |
| DNS 监听 | 127.0.0.1:53 空闲；现有 resolved 位于 127.0.0.53/54 |
| NSS | files dns myhostname |
| 新 DNS 服务/配置路径 | 不存在，没有发现覆盖冲突 |
| 优先级 2000 | 未占用 |
| 路由表 2022 | 由现有 Mihomo 使用；切换时由其生命周期更新 |
| 官方 Claude 独立代理冲突 | 未发现；预检时没有官方 Claude 账号 |
| 进程显式代理环境 | 快照中未发现非空 HTTP_PROXY/HTTPS_PROXY/ALL_PROXY |
| 官方域名 hosts 覆盖 | 未发现 |
| 旧试运行 timer | inactive |

观察到应用与云助手的旧 TUN 关联连接。操作单记录云助手原状态，在独立 SSH/VNC 中维护时暂停并恢复 `aliyun.service`；操作者还需梳理其他客户端及缓存。不要在依赖云助手的命令窗口内进行切换。

DNSmasq 的实际语法检查需由用户安装二进制后在现场执行。这里没有安装软件、修改线上配置或内核路由，没有进行在线故障注入。模拟和语法检查不能代替第二阶段完成后的实际 DNS、路由和保护验收。
