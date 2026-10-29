# 第二阶段手动操作单：DNS、定向 TUN 与出口保护

验证状态见 [第二阶段切换前核对](PHASE2-PREFLIGHT.md)。本操作单由用户手动执行。第一阶段应用已经上线，本阶段继续使用当前程序，不重新编译或替换二进制。目标：官方 Claude 域名使用 Mihomo 固定出口；普通系统流量使用正常网络；账号独立代理继续按账号绑定执行。

**本阶段会短暂停止 Sub2API、Mihomo 和原本运行的云助手 `aliyun.service`。使用独立 SSH 或云控制台 VNC，保留第二个连接；不要在依赖云助手的命令窗口中进行切换。** 在另一个窗口保留本文件及回滚部分，切换可能中断当前工具会话。不要把所有代码块一次性粘贴。


**会话要求：各段在同一个 root Bash 中执行。新建 SSH 窗口、再次 `sudo -i` 或 shell 因报错退出后，变量不会自动保留。** 此时先执行 `source /root/本次第二阶段目录/session.env` 恢复变量，再继续。不要重用第一阶段的 session.env，也不要跳过代码块开头的检查。

只读预检时：第一阶段正常；没有官方 Claude 账号代理冲突；`127.0.0.1:53` 可用；优先级 2000 未占用；现有保护 confirmed 且一致；旧试运行 timer inactive。当前 Mihomo 使用全量 TUN 和 redir-host，观察到应用及云助手的旧 TUN 关联连接。除了下面列出的服务，操作者还需确认没有遗漏正在使用旧出口或显式代理的其他客户端；自带 DoH、固定官方 IP、独立远端代理的客户端不属于这套系统 DNS 路由的自动覆盖范围。

一、先安装缺少的 DNS 二进制，应用继续运行。若已经安装 dnsmasq-base 且 `/usr/sbin/dnsmasq` 可执行，可直接进入第二段。

```bash
sudo -i
```

```bash
set -Eeuo pipefail
apt-get -s --no-install-recommends install dnsmasq-base
```

本次预检计划为 `0 upgraded, 1 newly installed, 0 to remove`，唯一新增包为 dnsmasq-base。重新查看本机计划，符合后再执行下面一行；若有升级、删除或其他包变动，先停止并反馈。只安装 `dnsmasq-base`，不安装会自动启用通用服务的 `dnsmasq` 包。

```bash
NEEDRESTART_MODE=l apt-get install --no-install-recommends dnsmasq-base
command -v dnsmasq nft ip
python3 -c 'import yaml; print("PyYAML OK")'
```

二、建立新的第二阶段备份目录；不要复用第一阶段备份目录。

```bash
set -Eeuo pipefail
umask 077
APP_DIR=/home/pk/workspace/sub2api
POLICY_DIR="$APP_DIR/ops/changes/20261028-egress-policy"
PRIVATE_DIR=$(mktemp -d /root/sub2api-phase2-20261029.XXXXXX)
CLOUD_ASSIST_WAS_ACTIVE=0
if systemctl is-active --quiet aliyun.service; then CLOUD_ASSIST_WAS_ACTIVE=1; fi
declare -p APP_DIR POLICY_DIR PRIVATE_DIR CLOUD_ASSIST_WAS_ACTIVE > "$PRIVATE_DIR/session.env"
printf '请保存第二阶段备份目录：%s\n' "$PRIVATE_DIR"

systemctl is-active sub2api mihomo sub2api-egress nginx postgresql@16-main redis-server
/usr/local/sbin/mihomo-ctl status
curl -fsS --max-time 5 http://127.0.0.1:8080/health
printf '%s  %s\n' '6535d20036928c966bcd10c447d8449ae4d7c342a2baea1f2dcc72583f6706d0' "$APP_DIR/bin/sub2api" | sha256sum -c -
printf '%s  %s\n' 'e85cacdb1456abaed0152d604229aab098880f8e85de8d0488fbb746d037f049' "$POLICY_DIR/templates/egress_guard.py" | sha256sum -c -
rg '^hosts:' /etc/nsswitch.conf
ss -lntup
test ! -e /etc/systemd/system/dnsmasq-claude.service
test ! -L /etc/systemd/system/dnsmasq-claude.service
test ! -e /etc/dnsmasq-claude.conf
test ! -L /etc/dnsmasq-claude.conf
test "$(systemctl show dnsmasq-claude.service -p LoadState --value)" = not-found
test "$(systemctl show mihomo-trial-rollback.timer -p ActiveState --value)" = inactive
python3 - <<'CHECK'
import json, subprocess
rules = json.loads(subprocess.check_output(['ip', '-j', 'rule', 'show'], text=True))
assert not any(rule.get('priority') == 2000 for rule in rules), 'priority 2000 is occupied'
routes = json.loads(subprocess.check_output(['ip', '-j', 'route', 'show', 'table', 'main', 'exact', '198.18.0.0/16'], text=True))
assert not routes, 'the reserved main-table prefix is already in use'
print('Reserved rule priority and main-table prefix are available')
CHECK
```

继续前必须确认：服务全部 active，health 为 ok，保护 confirmed/consistent；NSS 保持 `files dns myhostname`；`127.0.0.1:53` 空闲（`127.0.0.53:53`、`127.0.0.54:53` 是不同地址，可以保留）；账号中没有官方 Claude 独立代理绑定；其他使用旧出口的客户端已列出。不要自动清空账号代理。

三、从当前在线配置生成私有候选并备份网络原件。

```bash
set -Eeuo pipefail
: "${APP_DIR:?请先 source 本次第二阶段 session.env}"
: "${POLICY_DIR:?请先 source 本次第二阶段 session.env}"
: "${PRIVATE_DIR:?请先 source 本次第二阶段 session.env}"
: "${CLOUD_ASSIST_WAS_ACTIVE:?请先 source 本次第二阶段 session.env}"
case "$PRIVATE_DIR" in
  /root/sub2api-phase2-20261029.*) ;;
  *) printf '第二阶段备份目录不正确，停止操作\n' >&2; exit 1 ;;
esac
test -f "$PRIVATE_DIR/session.env"
test -x /usr/sbin/dnsmasq
test -f "$POLICY_DIR/prepare_private_config.py"
test -f "$APP_DIR/data/config.yaml"
python3 "$POLICY_DIR/prepare_private_config.py" \
  --mihomo-source /etc/mihomo/config.yaml \
  --app-source "$APP_DIR/data/config.yaml" \
  --output-dir "$PRIVATE_DIR/phase2-candidates"
mkdir -m 700 "$PRIVATE_DIR/phase2"
cp -a /etc/resolv.conf "$PRIVATE_DIR/phase2/resolv.conf"
cp -a "$APP_DIR/data/config.yaml" "$PRIVATE_DIR/phase2/config.yaml"
if systemctl is-enabled mihomo sub2api-egress mihomo-trial-rollback.timer > "$PRIVATE_DIR/phase2/service-enablement.txt"; then
  printf 'service enablement states recorded\n'
else
  cat "$PRIVATE_DIR/phase2/service-enablement.txt"
fi
ip rule show > "$PRIVATE_DIR/phase2/ip-rule.txt"
ip route show table all > "$PRIVATE_DIR/phase2/ip-route.txt"
tar --acls --xattrs -cpf "$PRIVATE_DIR/phase2/installed.tar" \
  /etc/mihomo/config.yaml \
  /etc/systemd/system/mihomo.service \
  /etc/systemd/system/sub2api-egress.service \
  /etc/systemd/system/mihomo.service.d \
  /etc/systemd/system/sub2api.service.d \
  /etc/systemd/system/mihomo-trial-rollback.service \
  /etc/systemd/system/mihomo-trial-rollback.timer \
  /usr/local/libexec/mihomo-ssh-route \
  /usr/local/libexec/mihomo-check \
  /usr/local/libexec/egress_guard.py \
  /usr/local/libexec/mihomo_control.py \
  /usr/local/sbin/mihomo-ctl \
  /var/lib/sub2api-egress
sha256sum "$PRIVATE_DIR/phase2/installed.tar" "$PRIVATE_DIR/phase2/config.yaml" > "$PRIVATE_DIR/phase2/files.sha256"
sha256sum -c "$PRIVATE_DIR/phase2/files.sha256"
tar -tf "$PRIVATE_DIR/phase2/installed.tar" > "$PRIVATE_DIR/phase2/archive-list.txt"
/usr/local/bin/mihomo -t -d "$PRIVATE_DIR/phase2" -f "$PRIVATE_DIR/phase2-candidates/mihomo.yaml"
/usr/sbin/dnsmasq --test --conf-file="$POLICY_DIR/templates/dnsmasq-claude.conf"
python3 "$POLICY_DIR/templates/egress_guard.py" render > "$PRIVATE_DIR/phase2/guard.nft"
```

`tar` 提示移除开头 `/` 是正常提示；缺失文件或其他失败不能忽略。候选预检只是语法检查，没有启动第二份代理。所有备份和包含凭据的候选留在 root 私有目录，不要粘贴其内容到聊天。

四、开始维护切换。先手动暂停前面识别的其他客户端，然后逐段执行。

```bash
set -Eeuo pipefail
: "${APP_DIR:?请先 source 本次第二阶段 session.env}"
: "${POLICY_DIR:?请先 source 本次第二阶段 session.env}"
: "${PRIVATE_DIR:?请先 source 本次第二阶段 session.env}"
: "${CLOUD_ASSIST_WAS_ACTIVE:?请先 source 本次第二阶段 session.env}"
case "$PRIVATE_DIR" in
  /root/sub2api-phase2-20261029.*) ;;
  *) printf '第二阶段备份目录不正确，停止操作\n' >&2; exit 1 ;;
esac
test -f "$PRIVATE_DIR/session.env"
test -x /usr/sbin/dnsmasq
test -s "$PRIVATE_DIR/phase2/installed.tar"
test -s "$PRIVATE_DIR/phase2/config.yaml"
test -s "$PRIVATE_DIR/phase2-candidates/mihomo.yaml"
test -s "$PRIVATE_DIR/phase2-candidates/application-phase2.yaml"
sha256sum -c "$PRIVATE_DIR/phase2/files.sha256"
if [ "$CLOUD_ASSIST_WAS_ACTIVE" -eq 1 ]; then systemctl stop aliyun.service; fi
systemctl stop sub2api
systemctl disable --now mihomo-trial-rollback.timer
systemctl stop mihomo
install -o root -g root -m 755 "$POLICY_DIR/templates/egress_guard.py" /usr/local/libexec/egress_guard.py
install -o root -g root -m 755 "$POLICY_DIR/templates/mihomo_control.py" /usr/local/libexec/mihomo_control.py
install -o root -g root -m 755 "$POLICY_DIR/templates/mihomo-ctl" /usr/local/sbin/mihomo-ctl
install -o root -g root -m 644 "$POLICY_DIR/templates/sub2api-egress.service" /etc/systemd/system/sub2api-egress.service
install -o root -g root -m 644 "$POLICY_DIR/templates/mihomo.service" /etc/systemd/system/mihomo.service
install -o root -g root -m 644 "$POLICY_DIR/templates/dnsmasq-claude.service" /etc/systemd/system/dnsmasq-claude.service
install -o root -g root -m 600 "$POLICY_DIR/templates/dnsmasq-claude.conf" /etc/dnsmasq-claude.conf
install -o root -g root -m 600 "$PRIVATE_DIR/phase2-candidates/mihomo.yaml" /etc/mihomo/config.yaml
/usr/local/libexec/egress_guard.py migrate
systemctl daemon-reload
systemctl restart sub2api-egress
systemctl start mihomo
systemctl enable --now dnsmasq-claude
/usr/local/libexec/egress_guard.py verify
systemctl is-active mihomo sub2api-egress dnsmasq-claude
```

保护输出必须是 `phase: confirmed`、`mode: claude-only`、`consistent: true`。全部成功后才切换 DNS 文件和应用配置：

```bash
set -Eeuo pipefail
: "${APP_DIR:?请先 source 本次第二阶段 session.env}"
: "${POLICY_DIR:?请先 source 本次第二阶段 session.env}"
: "${PRIVATE_DIR:?请先 source 本次第二阶段 session.env}"
: "${CLOUD_ASSIST_WAS_ACTIVE:?请先 source 本次第二阶段 session.env}"
case "$PRIVATE_DIR" in
  /root/sub2api-phase2-20261029.*) ;;
  *) printf '第二阶段备份目录不正确，停止操作\n' >&2; exit 1 ;;
esac
test -f "$PRIVATE_DIR/session.env"
test -s "$PRIVATE_DIR/phase2-candidates/application-phase2.yaml"
test ! -e /etc/resolv.conf.egress-new
test ! -L /etc/resolv.conf.egress-new
install -o root -g root -m 644 "$POLICY_DIR/templates/resolv.conf" /etc/resolv.conf.egress-new
mv -T /etc/resolv.conf.egress-new /etc/resolv.conf
resolvectl flush-caches
install -o sub2api -g sub2api -m 600 "$PRIVATE_DIR/phase2-candidates/application-phase2.yaml" "$APP_DIR/data/config.yaml"
systemctl start sub2api
```

五、正常状态验收。不要通过停代理、断网或真实模型请求来做故障测试。

```bash
set -Eeuo pipefail
: "${APP_DIR:?请先 source 本次第二阶段 session.env}"
: "${POLICY_DIR:?请先 source 本次第二阶段 session.env}"
: "${PRIVATE_DIR:?请先 source 本次第二阶段 session.env}"
: "${CLOUD_ASSIST_WAS_ACTIVE:?请先 source 本次第二阶段 session.env}"
case "$PRIVATE_DIR" in
  /root/sub2api-phase2-20261029.*) ;;
  *) printf '第二阶段备份目录不正确，停止操作\n' >&2; exit 1 ;;
esac
test -f "$PRIVATE_DIR/session.env"
DEPLOY_READY=0
for attempt in {1..30}; do
  if curl -fsS --max-time 2 http://127.0.0.1:8080/health > "$PRIVATE_DIR/health-after.json"; then
    DEPLOY_READY=1
    break
  fi
  sleep 1
done
test "$DEPLOY_READY" -eq 1
cat "$PRIVATE_DIR/health-after.json"
systemctl is-active sub2api mihomo sub2api-egress dnsmasq-claude nginx postgresql@16-main redis-server
/usr/local/sbin/mihomo-ctl status
getent ahostsv4 api.anthropic.com
getent ahostsv4 example.com
ip route get 198.18.1.1
ip route get 1.1.1.1
ip rule show
```

预期：官方域名为 `198.18.0.0/16` 内地址，普通域名为真实公网地址；Fake-IP 经 mihomo，普通目标经 eth0；保护保持一致。任一项不符时不恢复其他客户端，执行下面的回滚。

确认通过后恢复云助手及之前暂停的其他客户端，使其重新解析 DNS、建立新连接：

```bash
set -Eeuo pipefail
: "${APP_DIR:?请先 source 本次第二阶段 session.env}"
: "${POLICY_DIR:?请先 source 本次第二阶段 session.env}"
: "${PRIVATE_DIR:?请先 source 本次第二阶段 session.env}"
: "${CLOUD_ASSIST_WAS_ACTIVE:?请先 source 本次第二阶段 session.env}"
case "$PRIVATE_DIR" in
  /root/sub2api-phase2-20261029.*) ;;
  *) printf '第二阶段备份目录不正确，停止操作\n' >&2; exit 1 ;;
esac
test -f "$PRIVATE_DIR/session.env"
if [ "$CLOUD_ASSIST_WAS_ACTIVE" -eq 1 ]; then
  systemctl start aliyun.service
  systemctl is-active aliyun.service
fi
printf '第二阶段部署完成；备份目录：%s\n' "$PRIVATE_DIR"
```

在聊天中回复“第二阶段部署完成，备份目录是 /root/…”。我收到消息后核对配置、DNS 分流、实际路由、保护一致性、服务日志、网页与代理绑定。当前模拟验证不代表内核切换已通过；上线以实际验收为准。

## 切换失败时回滚到第一阶段

任何命令失败先停止后续步骤。若 root shell 因 `set -e` 退出，重新 `sudo -i`，把下面路径换成本次真实目录后恢复变量。第一阶段已上线的新程序保留，本回滚只恢复网络组合和第一阶段应用配置。

```bash
set -Eeuo pipefail
source /root/sub2api-phase2-20261029.替换为实际后缀/session.env
set -Eeuo pipefail
: "${APP_DIR:?请先 source 本次第二阶段 session.env}"
: "${POLICY_DIR:?请先 source 本次第二阶段 session.env}"
: "${PRIVATE_DIR:?请先 source 本次第二阶段 session.env}"
: "${CLOUD_ASSIST_WAS_ACTIVE:?请先 source 本次第二阶段 session.env}"
case "$PRIVATE_DIR" in
  /root/sub2api-phase2-20261029.*) ;;
  *) printf '第二阶段备份目录不正确，停止操作\n' >&2; exit 1 ;;
esac
test -f "$PRIVATE_DIR/session.env"
test -s "$PRIVATE_DIR/phase2/config.yaml"
test -s "$PRIVATE_DIR/phase2/installed.tar"
sha256sum -c "$PRIVATE_DIR/phase2/files.sha256"
systemctl stop sub2api
systemctl stop mihomo
if [ "$(systemctl show dnsmasq-claude.service -p LoadState --value)" != not-found ]; then
  systemctl stop dnsmasq-claude
fi
systemctl stop mihomo-trial-rollback.timer
tar --acls --xattrs -xpf "$PRIVATE_DIR/phase2/installed.tar" -C /
install -o sub2api -g sub2api -m 600 "$PRIVATE_DIR/phase2/config.yaml" "$APP_DIR/data/config.yaml"
systemctl daemon-reload
/usr/local/libexec/egress_guard.py restore
/usr/local/libexec/egress_guard.py verify
test ! -e /etc/resolv.conf.egress-restore
test ! -L /etc/resolv.conf.egress-restore
cp -a "$PRIVATE_DIR/phase2/resolv.conf" /etc/resolv.conf.egress-restore
mv -T /etc/resolv.conf.egress-restore /etc/resolv.conf
resolvectl flush-caches
if [ "$(systemctl show dnsmasq-claude.service -p LoadState --value)" != not-found ]; then
  systemctl disable dnsmasq-claude
fi
systemctl start mihomo
/usr/local/sbin/mihomo-ctl status
```

确认旧保护已恢复为 confirmed 且一致、旧 Mihomo 正常后，按实际存在情况删除本次新增的精确规则和黑洞。以下检查支持切换中途失败、规则尚未创建的情况；若优先级 2000 被其他规则占用则停止，不删除它。

```bash
set -Eeuo pipefail
: "${APP_DIR:?请先 source 本次第二阶段 session.env}"
: "${POLICY_DIR:?请先 source 本次第二阶段 session.env}"
: "${PRIVATE_DIR:?请先 source 本次第二阶段 session.env}"
: "${CLOUD_ASSIST_WAS_ACTIVE:?请先 source 本次第二阶段 session.env}"
case "$PRIVATE_DIR" in
  /root/sub2api-phase2-20261029.*) ;;
  *) printf '第二阶段备份目录不正确，停止操作\n' >&2; exit 1 ;;
esac
test -f "$PRIVATE_DIR/session.env"
RULE_SNAPSHOT=$(ip -4 rule show)
if printf '%s\n' "$RULE_SNAPSHOT" | grep -q '^2000:'; then
  printf '%s\n' "$RULE_SNAPSHOT" | grep -Eq '^2000:[[:space:]]+from all to 198\.18\.0\.0/16 lookup 2022[[:space:]]*$'
  ip rule del pref 2000 to 198.18.0.0/16 lookup 2022
fi
ROUTE_SNAPSHOT=$(ip -4 route show table main exact 198.18.0.0/16)
if printf '%s\n' "$ROUTE_SNAPSHOT" | grep -Eq '^blackhole 198\.18\.0\.0/16 .*metric 42760([[:space:]]|$)'; then
  ip route del blackhole 198.18.0.0/16 metric 42760
fi
systemctl start sub2api
```

然后重复第五段就绪等待与 health 检查，并检查第一阶段六个服务及旧保护状态；不要再要求已停用的 dnsmasq-claude 为 active，也不要执行第二阶段 Fake-IP 路由预期。恢复原本运行的云助手及其他客户端，反馈失败步骤。候选 DNS 文件保留供核对；再次尝试前先复核残留状态。不要清空防火墙、删除保护状态文件或全库恢复数据库。
