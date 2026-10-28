# 手动回滚教程

只在操作者部署后需要恢复时执行。本次源码交付不运行这些命令。使用部署教程同一个 root shell 的 `APP_DIR`、`PRIVATE_DIR` 和 `DATABASE_NAME`；如果更换会话，先恢复实际目录变量，并执行 `set -Eeuo pipefail`；每一步失败都停止，不继续启动应用。恢复前用相应阶段的 `files.sha256` 检查备份。

不要用清空防火墙、删除状态文件或解除保护的方式恢复服务。第二阶段必须先恢复整套旧网络和保护，再启动应用。

## 第一阶段回滚

适用：只升级了应用，或第二阶段已经完整回滚。影响范围：Sub2API 短暂停止；DNS、Mihomo 和全量保护不变。

```bash
systemctl stop sub2api
install -o root -g root -m 755 "$PRIVATE_DIR/phase1/sub2api" "$APP_DIR/bin/sub2api.egress-restore"
mv -T "$APP_DIR/bin/sub2api.egress-restore" "$APP_DIR/bin/sub2api"
install -o sub2api -g sub2api -m 600 "$PRIVATE_DIR/phase1/config.yaml" "$APP_DIR/data/config.yaml"
/usr/local/sbin/mihomo-ctl status
systemctl start sub2api
curl -fsS --max-time 3 http://127.0.0.1:8080/health
```

预期：旧程序和原应用设置恢复，原保护仍一致。失败则保留应用停止状态，检查备份和原服务日志；不能通过关闭保护强行启动。

第一阶段规范化只影响代理回退字段，没有改账号绑定。默认保留 `none` 更符合禁止自动换出口的要求，回滚程序不要求恢复旧回退策略。若明确需要恢复历史字段，保持应用停止，在私有目录使用备份 CSV 建立临时表，按代理 ID 更新这两个字段；不恢复整个生产数据库，不覆盖期间产生的正常业务数据。数据库备份仅用于灾难恢复，不在普通回滚流程中执行全库还原。

如明确选择恢复旧回退字段，只执行下面这一段客户端管道；root 读取私有文件，PostgreSQL 只接收 SQL 输入，不放宽备份目录权限。执行时应用必须保持停止。

```bash
python3 - "$PRIVATE_DIR/phase1/proxy-fallback-before.csv" <<'PY' | sudo -u postgres psql -X -v ON_ERROR_STOP=1 -d "$DATABASE_NAME"
import pathlib, sys
print("BEGIN; CREATE TEMP TABLE restore_proxy_fallback(id bigint, fallback_mode text, backup_proxy_id bigint);")
print("COPY restore_proxy_fallback FROM STDIN WITH CSV HEADER;")
print(pathlib.Path(sys.argv[1]).read_text(), end="")
print("\\.")
print("UPDATE proxies p SET fallback_mode=b.fallback_mode, backup_proxy_id=b.backup_proxy_id FROM restore_proxy_fallback b WHERE p.id=b.id; COMMIT;")
PY
```

只在你主动选择恢复旧回退字段时执行。恢复这些字段会重新允许旧程序的自动回退行为。

## 第二阶段回滚

适用：网络切换任一步失败，或正常业务观察发现异常。目标是回到第一阶段的完整全量 TUN 网络，而不是无保护直连。

先暂停 Sub2API 及其他受管官方 Claude 客户端。以下停止动作是恢复配置所需的维护操作，不是故障测试：

```bash
systemctl stop sub2api
systemctl stop mihomo
systemctl stop dnsmasq-claude
systemctl stop mihomo-trial-rollback.timer
```

保留现有定向规则和黑洞路由，恢复旧脚本、单元、Mihomo 配置和 v1 状态：

```bash
tar --acls --xattrs -xpf "$PRIVATE_DIR/phase2/installed.tar" -C /
install -o sub2api -g sub2api -m 600 "$PRIVATE_DIR/phase2/config.yaml" "$APP_DIR/data/config.yaml"
systemctl daemon-reload
/usr/local/libexec/egress_guard.py restore
/usr/local/libexec/egress_guard.py verify
```

预期：旧全量保护已恢复并验证一致。这里失败则保持应用停止，不删除任意保护表或状态。

恢复 DNS 符号链接或文件本身，随后恢复旧 Mihomo：

```bash
test ! -e /etc/resolv.conf.egress-restore
test ! -L /etc/resolv.conf.egress-restore
cp -a "$PRIVATE_DIR/phase2/resolv.conf" /etc/resolv.conf.egress-restore
mv -T /etc/resolv.conf.egress-restore /etc/resolv.conf
systemctl disable dnsmasq-claude
systemctl start mihomo
/usr/local/sbin/mihomo-ctl status
```

预期：旧代理 active、v1 保护 `confirmed` 且一致，DNS 回到原有入口。只在旧保护恢复后，删除本次新增的精确黑洞路由：

```bash
ip rule del pref 2000 to 198.18.0.0/16 lookup 2022
ip route del blackhole 198.18.0.0/16 metric 42760
systemctl start sub2api
systemctl is-active sub2api mihomo sub2api-egress nginx postgresql@16-main redis-server
curl -fsS --max-time 3 http://127.0.0.1:8080/health
```

如果精确规则或路由不存在，失败停止设置会终止该段。记录情况并通过只读路由确认后，只执行尚未完成的恢复步骤；不要删除其他路由。新 DNS 服务保持停用，候选配置文件暂留便于复核，不执行通配清理。按照 `service-enablement.txt` 保持原服务启用状态；原基线没有待执行试运行，因此不要重新启用旧试运行 timer。

重新启动维护前暂停的受管客户端，检查正常状态与已有日志。此时可以保留第一阶段的新程序，也可以再执行第一阶段程序回滚。记录回滚原因、时间、恢复文件校验值和未完成事项，不把回滚记录成新策略已部署。
