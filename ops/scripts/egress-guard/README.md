# Sub2API 出站保护设计与实现

当前状态（2026-10-25）：本实现已安装到在线系统，已核对脚本一致；保护为 confirmed，规则存在且指纹一致。本次仓库整理未重新安装或修改防火墙。当前记录见 [运维说明](../../README.md)。

## 控制语义

**start 立即加锁，confirm 确认保留，只有显式 mihomo-ctl stop 解锁。**

| 事件 | 持久状态 | 出站保护 |
|---|---|---|
| 初次安装为未启用 | off | 无 |
| start 通过配置预检 | trial | 先加锁，再启动 Mihomo |
| confirm | confirmed | 保持 |
| 180 秒未确认 | trial | 停 Mihomo，保护继续保留 |
| Mihomo 崩溃、自动重启、手动 systemctl stop | 不变 | 保持 |
| 服务器重启 | 不变 | 先恢复规则，再允许 Sub2API/Mihomo 启动 |
| start 启动失败 | trial | 保持，不自动解锁 |
| mihomo-ctl stop 成功 | off | 停完 Mihomo 后解锁 |
| stop 失败或 Mihomo 仍存活 | 不变 | 保持 |

配置预检失败时不启动、不新增加锁；已有锁不变。试运行到期后可直接重新 start，全程保持保护；控制器区分定时器的 waiting/running 与 elapsed 状态，不会把已到期的 active/elapsed 定时器当作未结束的试运行。trial 状态重启后仍锁定，180 秒定时器不自动恢复；管理员可重新 start/confirm 或显式 stop。confirm 要求进程运行且实际规则与记录一致，并不以进程 active 代替代理可达性验收。

## 实际封锁

`egress_guard.py` 动态解析专用用户 sub2api 的 UID（当前为 999），建立独立表 `inet sub2api_egress_guard`：

- OUTPUT hook 优先级 -160，在当前 Mihomo 的 -150 自动重定向之前检查。
- 只匹配该 UID 的 socket；其他用户不受本表限制。
- 允许真正经 lo 发往 IPv4 回环、IPv6 ::1 的本机通信。
- 允许出口接口名称为 mihomo 的流量，由 Mihomo 接管。
- 拒绝该 UID 其余全部出站，包括 IPv6、UDP 与已经建立的直连连接。
- 不添加 `ct state established accept` 之类的全量豁免。
- 单次 nft 事务只替换自己的表，不清空系统规则，不改 Mihomo 的表。

TUN 设备消失后，规则仍存在；路由回落到 eth0 时该 UID 的包被拒绝。规则本身不随 Mihomo 生命周期删除，UID 发生改变时验证失败并拒绝服务启动，避免静默保护错用户。

这是“Sub2API 不能直接出站”的边界，不是对 root 的隔离。Mihomo 自己必须保持业务流量没有 DIRECT 回退；root 主动清空所有防火墙或修改脚本也可以破坏保护。规则不会监控或约束本机其他用户、系统 DNS 服务代发的流量；当前允许必要本机通信，因此不将其宣称为严格的全系统 DNS 隐私方案。

## 状态与启动顺序

`/var/lib/sub2api-egress/state.json` 由 root 管理，目录 0700、文件 0600。状态包含版本、phase、UID、TUN 名称和实际规则的语义指纹。先持久保存加锁意图，再安装规则；恢复时根据持久状态补齐规则。文件写入使用临时文件、原子 rename 和 fsync。

解锁写盘失败时，使用操作前保存的锁定状态重新安装规则并写回状态；即使错误发生在 rename 之后的目录 fsync，也不会误用刚写入的 off 状态跳过恢复。

`sub2api-egress.service` 为独立 oneshot 服务，无解锁的 ExecStop/ExecStopPost；Sub2API 与 Mihomo 都 Requires/After 该服务，并在每次启动时再次验证状态与实际规则。状态缺失、损坏、UID 改变、规则丢失或被修改时验证失败。恢复失败时依赖服务不能启动。

guard helper 使用独立文件锁串行化状态/规则事务；mihomo-ctl 使用控制锁串行化 start/confirm/stop/试运行超时，避免 confirm 与超时回滚竞争。调用 systemctl 启动服务时不持有 guard 锁，避免启动前验证死锁。

`status` 输出 phase、规则是否存在、规则指纹是否一致、Mihomo 是否 active、试运行定时器是否 active。规则计数器变化不影响指纹；服务 active 但规则缺失时返回失败。

## 文件

- `egress_guard.py`：持久状态、规则安装/验证/恢复。CLI 只公开 initialize/restore/verify/status；解锁由控制器的 stop 调用。
- `mihomo_control.py`、`mihomo-ctl`：替换当前控制脚本。
- `sub2api-egress.service`、`guard-dependency.conf`：恢复与启动验证。
- `mihomo-trial-rollback.*`：180 秒试运行到期只停 Mihomo，不解锁。
- `install.sh`：备份后安装；不会因安装失败自动解除已有保护。
- `test_state.py`、`test_install.py`、`test_kernel.py`：控制状态、安装故障测试及隔离网络内真实报文测试。

## 安装方式（仅供重建或明确维护时使用）

当前 Mihomo 已运行且启用开机启动。安装时可直接接纳为 confirmed 状态，不重启 Mihomo 或 Sub2API：

```bash
sudo bash /home/pk/workspace/sub2api/ops/scripts/egress-guard/install.sh --adopt-confirmed
sudo mihomo-ctl status
```

第一次安装应在可观察的维护时段进行。安装会将旧控制脚本、已有同名服务配置与状态备份到 root 私有的 `/var/backups/sub2api-egress.*`；只修改相关 drop-in，不覆盖现有 Sub2API/Mihomo 主服务文件。若原 180 秒试运行尚未结束，安装拒绝继续。

安装器先写入恢复服务和两个服务的启动依赖，使用 sync 同步其所在文件系统并重新加载 systemd，再写入锁定意图。启动依赖准备失败时不会开始加锁。保护规则安装失败时，候选安装器会停止 Sub2API，保持已写入的锁定意图；下次开机必须先恢复保护，恢复失败则依赖服务无法启动。恢复规则后需启动服务。恢复旧脚本不等于解除保护，不应直接删除状态文件或全量清空防火墙。

另有 `--unlocked` 安装模式，只允许 Mihomo 已停止且原状态未锁定时使用，不会把已有锁静默变为 off。

## 验证范围

当前验证结果：15 个控制状态/规则指纹测试、6 个安装测试、12 组隔离内核测试通过。三个缺陷对应的回归测试在修复前副本上均能复现失败。候选 systemd 单元此前已通过校验，本次未修改单元文件。具体范围和未测项目记录在 `validation.json`。

执行：

```bash
cd /home/pk/workspace/sub2api/scripts/egress-guard
python3 -m unittest -v test_state.py test_install.py
python3 test_kernel.py
```

内核测试先创建独立网络命名空间，并在任何网络操作前断言与宿主命名空间不同；远端 echo 服务也在另一个独立空间中。使用真实 nftables、IPv4/IPv6 和 UID=999 socket，检查新旧直连被拒、本机通信正常、其他用户不受影响、与 Mihomo OUTPUT 自动重定向顺序兼容、TUN 删除后仍阻止直连、持久状态恢复及显式解锁。

安装测试仅在临时副本中重定向部署路径和 root 检查，模拟 guard、systemctl 与文件系统同步命令；检查启动依赖安装、同步、重新加载、加锁及激活各阶段的失败行为。它不运行在线安装器，也不代替真实重启验收。内核测试额外注入 rename 后的目录 fsync 故障，验证规则和锁定状态恢复，以及随后丢失规则时的恢复行为。

这些测试不停止在线代理、不产生真实模型调用。它们不替代安装后的在线普通/流式/WebSocket 验收，也不声称已经完成整机重启或真实代理崩溃测试。
