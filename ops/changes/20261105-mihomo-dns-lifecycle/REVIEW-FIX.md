# DNS 审查修复与线上验收

2026-11-05 16:09:12（新加坡时间），按用户“修复这个”的指示完成线上 helper 更新。Mihomo 仍为停止状态，没有重启 dnsmasq 或 Sub2API。

## 修复内容

原实现会在 dnsmasq 单独重启时对 Mihomo 做一次 200ms DNS 探测；即使 Mihomo 服务仍为 active，一次超时也会清空转发规则。进程随后恢复响应时不会重新执行启动钩子，导致持续使用普通上游。

现在 `prepare` 根据服务生命周期重建规则：Mihomo 为 active 且主进程存在时保留转发。该状态表示 `ExecStartPost=on` 已完成首次 DNS 就绪检查；dnsmasq 重启不再用新的单次探测撤销规则。短暂失去响应期间，官方域名解析暂时失败，恢复响应后自然恢复 Fake-IP 解析，其他域名仍使用普通上游。Mihomo 停止、崩溃或启动失败时恢复普通解析的逻辑不变。

修复无需新增轮询服务、定时器或 systemd 配置。账号绑定、应用默认显式代理及出口保护均未修改。

## 源码与验证

- 提交：`919bcf422f1d49c2b7cd929f36ee115930a2bd41`。
- [PR #3](https://github.com/kunpeng9w/sub2api/pull/3)，未合并；源码位于 `/home/pk/.codex/worktrees/mihomo-dns-lifecycle/sub2api/ops/scripts/mihomo-dns/`。
- [DNS 专项](https://github.com/kunpeng9w/sub2api/actions/runs/41991954782/job/127126017879)：7 项测试通过，已读取日志。
- 新增真实 systemd/dnsmasq 回归：暂停 runner 上的假 Mihomo DNS 进程，确认查询超时后重启 dnsmasq；验证普通域名正常、官方域名未切到普通上游；恢复假进程后确认 Fake-IP 解析恢复，两个服务均无需再次重启；最后停止假 Mihomo，确认普通解析恢复。
- [运维检查](https://github.com/kunpeng9w/sub2api/actions/runs/41991954782/job/127126017274)：仓库检查及 21 项既有运维测试通过，已读取日志。
- 安装时其余应用 CI、出口审计和构建仍在运行，不将上述结果表述为完整 CI 通过。本机仅阅读、编辑、轻量 diff 检查及线上只读验收，未运行编译、测试、Lint 或类型检查。

交付前复查：[运维工作流](https://github.com/kunpeng9w/sub2api/actions/runs/41991954782)已全部成功，出口审计回归和前后端嵌入构建均通过并核查日志。应用 CI 的前端、Lint、shell 和发布辅助检查已通过；[完整后端测试](https://github.com/kunpeng9w/sub2api/actions/runs/41991954683)仍在执行单元测试、集成测试尚未开始。因此不宣称完整应用 CI 已通过，也不宣称上一版后端内存断言失败已修复；此次只修改 DNS 运维逻辑。

## 部署与验收

只原子替换 `/usr/local/libexec/mihomo_dns.py`，所有者 root:root、权限 0755。替换前核对旧文件摘要与被审查版本相符，替换后摘要与新提交一致。

- 旧 SHA-256：`62115c82d6a83fd2af76d0a9114d5248e0a520a1b482fd76326f2b54b4db0c03`。
- 新 SHA-256：`584a2b26193cce7f59fb501fba4e16447bd96c1a838df4fab3c9ec5280a9775b`。
- Mihomo 保持 inactive、MainPID=0；dnsmasq PID 保持 587559；Sub2API PID 保持 583415。
- `/run/mihomo-dns/servers.conf` 仍为空。
- `api.anthropic.com`、`claude.ai`、`claude.com` 均返回真实地址，非 `198.18.0.0/16`；`api.openai.com` 解析正常。
- `/health` 返回 `{"status":"ok"}`；保护为 `consistent=true`、`claude-only`、`confirmed`。
- 未启动线上 Mihomo、未注入故障、未发送模型请求。线上只验证当前停止状态，完整生命周期与超时恢复由 Actions 验证。

## 备份与回退

root 私有目录 `/root/mihomo-dns-review-20261105.fZ8Upe` 保存替换前后脚本、摘要、服务状态、保护状态及专项/运维 Actions 日志。

如需回退，先确认当前 helper 摘要仍与上述新 SHA-256 一致，再将 `mihomo_dns.py.before` 以 root:root/0755 复制到同目录临时文件并原子替换正式 helper。无需修改 drop-in 或重启服务。回退会重新引入本次短暂超时后无法自动恢复的问题；未执行线上回退演练。
