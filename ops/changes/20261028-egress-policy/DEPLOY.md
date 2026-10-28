# 手动部署教程

**本文只供操作者手动执行。源码交付和 CI 不执行本文命令。** 网络迁移涉及短时停服务，不能承诺零中断。模拟测试不能替代生产环境的实际稳定性观察。严格传输校验会增加账号/代理元数据读取；本次不做生产压测，第一阶段应独立观察数据库负载与正常请求延迟。

两阶段可以分开实施。第一阶段完成后可长期保留当前全量 TUN；不要为了完成本教程而立即进入第二阶段。

## 0. 准备与停止条件

执行位置：服务器 `aly`。先以 `pk` 查看候选源码和构建记录；涉及安装的步骤在一个独立 root Bash shell 中手动执行，并保留第二个 SSH 会话。启用下方失败停止设置；任何命令失败后都停止当前阶段，按对应回滚入口恢复，不能跳过失败命令继续粘贴后续步骤。

前置条件：`TEST-REPORT.md` 已填写最终通过的 CI 提交与链接；构建包来自该提交的 `Source and operations verification`，包含 `SHA256SUMS`、`build-info.txt` 和 Linux amd64 嵌入式程序。尚未通过 CI 时，停止，不安装。

在 root shell 中设置本次会话变量。`DELIVERY_DIR` 必须指向你已下载、解包清单可核实的构建交付目录：

```bash
set -Eeuo pipefail
APP_DIR=/home/pk/workspace/sub2api
POLICY_DIR="$APP_DIR/ops/changes/20261028-egress-policy"
DELIVERY_DIR=/root/sub2api-egress-delivery
PRIVATE_DIR=/root/sub2api-egress-20261028
DATABASE_NAME=sub2api
umask 077
mkdir -m 700 "$PRIVATE_DIR"
```

如果私有目录已存在，使用新的带时间后缀目录，不能覆盖上一次备份。不要把配置、认证信息或备份存入 Git 仓库。

本次已下载的独立 CI 包位于 `/home/pk/workspace/sub2api-egress-delivery/6d6b4a40baec99f672f3a10829b7ebf11d5a5e33/`，只是交付文件，没有安装或执行。核对报告里的 SHA256 后，在上述 root shell 中复制到私有交付目录：

```bash
install -d -o root -g root -m 700 "$DELIVERY_DIR"
cp /home/pk/workspace/sub2api-egress-delivery/6d6b4a40baec99f672f3a10829b7ebf11d5a5e33/{sub2api-linux-amd64.tar.gz,SHA256SUMS,build-info.txt,template-tests.log} "$DELIVERY_DIR/"
```

预期：仅复制候选包和构建清单。路径不存在、提交不符或校验不符就停止，不能改用在线程序冒充交付包。Actions 构建产物保留 7 天；本地副本保留至操作者自行清理。

只读记录基线：

```bash
date -Iseconds
systemctl is-active sub2api mihomo sub2api-egress nginx postgresql@16-main redis-server
systemctl show sub2api mihomo -p MainPID -p ExecMainStartTimestamp
systemctl cat sub2api mihomo sub2api-egress
/usr/local/sbin/mihomo-ctl status
curl -fsS --max-time 3 http://127.0.0.1:8080/health
sha256sum "$APP_DIR/bin/sub2api"
ip rule show
ip route show table all
free -m
df -h "$APP_DIR" /root
```

预期：现有服务健康，保护为 `confirmed` 且一致，没有待确认试运行。记录当前服务、规则和资源状态。任何已有故障、保护不一致、空间不足或不明路由冲突都应先停止本次部署；不要在迁移中顺便修复无关问题。避开现有备份及证书维护任务的执行时段。

## 1. 第一阶段：仅升级应用

影响范围：Sub2API 在替换程序时短暂停止；DNS、Mihomo、保护脚本和系统规则不变。失败返回 [第一阶段回滚](ROLLBACK.md#第一阶段回滚)。

### 1.1 核验交付包并准备私有配置

```bash
cd "$DELIVERY_DIR"
sha256sum -c SHA256SUMS
cat build-info.txt
tar -tzf sub2api-linux-amd64.tar.gz
tar -xzf sub2api-linux-amd64.tar.gz -C "$DELIVERY_DIR" sub2api
"$DELIVERY_DIR/sub2api" -version
```

预期：校验通过，包内程序名为 `sub2api`，版本输出的提交与 CI、`build-info.txt` 一致。发现不一致即停止。

以下脚本只在你手动运行时读取在线配置；它保留其他设置，并复制现有唯一 HTTP 代理节点，不输出认证信息：

```bash
python3 "$POLICY_DIR/prepare_private_config.py" \
  --mihomo-source /etc/mihomo/config.yaml \
  --app-source "$APP_DIR/data/config.yaml" \
  --output-dir "$PRIVATE_DIR/candidates"
```

预期：仅新增私有候选文件，权限 `600`。本阶段只使用 `application-phase1.yaml`，其 `gateway.claude_egress.enabled` 必须为 `false`。候选不是完整默认配置重建，原数据库、密钥、连接池等设置会保留。脚本拒绝多个代理节点、递归代理域名、仓库内输出及重复覆盖；失败即停止。

### 1.2 备份、停止应用并规范历史回退设置

```bash
mkdir -m 700 "$PRIVATE_DIR/phase1"
cp -a "$APP_DIR/bin/sub2api" "$PRIVATE_DIR/phase1/sub2api"
cp -a "$APP_DIR/data/config.yaml" "$PRIVATE_DIR/phase1/config.yaml"
sha256sum "$PRIVATE_DIR/phase1/sub2api" "$PRIVATE_DIR/phase1/config.yaml" > "$PRIVATE_DIR/phase1/files.sha256"
cd /tmp
sudo -u postgres pg_dump -Fc "$DATABASE_NAME" > "$PRIVATE_DIR/phase1/database.dump"
```

预期：备份存在且非空，数据库备份命令成功。备份期间应用可继续服务；`pg_dump` 使用一致性快照。备份失败不能继续。

现在进入应用维护窗口，先停止应用，避免旧到期任务与规范化操作竞争：

```bash
systemctl stop sub2api
sudo -u postgres psql -X -d "$DATABASE_NAME" -c \
  "COPY (SELECT id, fallback_mode, backup_proxy_id FROM proxies ORDER BY id) TO STDOUT WITH CSV HEADER" \
  > "$PRIVATE_DIR/phase1/proxy-fallback-before.csv"
sudo -u postgres psql -X -d "$DATABASE_NAME" -c \
  "COPY (SELECT id, proxy_id FROM accounts ORDER BY id) TO STDOUT WITH CSV HEADER" \
  > "$PRIVATE_DIR/phase1/account-bindings-before.csv"
sudo -u postgres psql -X -v ON_ERROR_STOP=1 -d "$DATABASE_NAME" \
  < "$POLICY_DIR/templates/normalize-proxy-fallback.sql"
sudo -u postgres psql -X -d "$DATABASE_NAME" -c \
  "COPY (SELECT id, proxy_id FROM accounts ORDER BY id) TO STDOUT WITH CSV HEADER" \
  > "$PRIVATE_DIR/phase1/account-bindings-after.csv"
cmp "$PRIVATE_DIR/phase1/account-bindings-before.csv" "$PRIVATE_DIR/phase1/account-bindings-after.csv"
sha256sum "$PRIVATE_DIR/phase1/"*.csv "$PRIVATE_DIR/phase1/database.dump" > "$PRIVATE_DIR/phase1/database-files.sha256"
```

预期：只将旧回退策略改为 `none`、清空备用代理字段，账号绑定前后完全一致。本次没有新增数据库结构迁移。SQL 或比较失败时停止，按第一阶段回滚恢复应用；不能忽略失败继续安装。

### 1.3 原子替换程序与应用配置

```bash
install -o root -g root -m 755 "$DELIVERY_DIR/sub2api" "$APP_DIR/bin/sub2api.egress-new"
mv -T "$APP_DIR/bin/sub2api.egress-new" "$APP_DIR/bin/sub2api"
install -o sub2api -g sub2api -m 600 "$PRIVATE_DIR/candidates/application-phase1.yaml" "$APP_DIR/data/config.yaml"
systemctl start sub2api
systemctl is-active sub2api mihomo sub2api-egress nginx postgresql@16-main redis-server
curl -fsS --max-time 3 http://127.0.0.1:8080/health
/usr/local/sbin/mihomo-ctl status
```

预期：应用恢复，旧保护仍为 `confirmed`，其他服务保持原状。查看既有日志，确认没有启动失败或反复重启。不要发送真实模型测试，也不要停代理测试。如果应用失败，立即执行第一阶段回滚。

到这里可以结束。新账号代理约束已经由新程序执行，但全局网络仍保持原结构，非 Claude 出站暂时仍依赖现有 TUN。不要把这一阶段记录成网络迁移已完成。

## 2. 第二阶段：独立 DNS、定向 TUN 与定向保护

影响范围：维护窗口内暂停 Sub2API 和其他受管官方 Claude 客户端，切换 Mihomo、DNS 入口和保护。失败返回 [第二阶段回滚](ROLLBACK.md#第二阶段回滚)，恢复完整旧网络组合。

### 2.1 前置检查与新鲜备份

- 先确认第一阶段正常运行；后台所有官方 Claude 账号没有独立代理绑定。第三方 Claude 渠道可以保留自己的代理，不能自动清空任何账号绑定。
- 查明服务器上的其他官方 Claude 客户端，维护期间由你手动暂停；迁移后重启这些客户端以清理真实 IP 和旧长连接缓存。
- 系统客户端必须使用 `/etc/resolv.conf` 的 DNS 路径。本机 `hosts:` 当前为 `files dns myhostname`；若环境已变为优先使用 `nss-resolve`，先停止迁移并调整设计，不能直接套用本文。
- `/etc/hosts` 不能包含官方域名的真实 IP 覆盖；自带 DoH、固定 IP 和其他远程代理的客户端应先纳管。
- 确认 `198.18.0.0/16` 和路由表 `2022` 除现有 Mihomo 外没有其他用途，策略规则优先级 `2000` 未被占用，`127.0.0.1:53` 未被其他程序占用；新 DNS 服务和文件不得覆盖已有同名自定义服务。
- 必须已有 `/usr/sbin/dnsmasq`、PyYAML、nft 和 ip。若需要安装，只安装 `dnsmasq-base`，不要安装会自动启动通用 DNS 服务的 `dnsmasq` 服务包；先查看 APT 模拟变更清单，出现升级或自动重启计划就停止。不要在网络切换中临时安装依赖。

```bash
command -v dnsmasq nft ip
python3 -c 'import yaml'
rg '^hosts:' /etc/nsswitch.conf
ss -lntup
test ! -e /etc/systemd/system/dnsmasq-claude.service
test ! -e /etc/dnsmasq-claude.conf
/usr/local/sbin/mihomo-ctl status
```

重新准备私有候选，读取的是第一阶段之后的最新配置，避免覆盖后来的正常设置变更：

```bash
python3 "$POLICY_DIR/prepare_private_config.py" \
  --mihomo-source /etc/mihomo/config.yaml \
  --app-source "$APP_DIR/data/config.yaml" \
  --output-dir "$PRIVATE_DIR/phase2-candidates"
mkdir -m 700 "$PRIVATE_DIR/phase2"
cp -a /etc/resolv.conf "$PRIVATE_DIR/phase2/resolv.conf"
cp -a "$APP_DIR/data/config.yaml" "$PRIVATE_DIR/phase2/config.yaml"
if systemctl is-enabled mihomo sub2api-egress mihomo-trial-rollback.timer > "$PRIVATE_DIR/phase2/service-enablement.txt"; then
  printf 'all recorded units enabled\n'
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
  /usr/local/libexec/egress_guard.py \
  /usr/local/libexec/mihomo_control.py \
  /usr/local/sbin/mihomo-ctl \
  /var/lib/sub2api-egress
sha256sum "$PRIVATE_DIR/phase2/installed.tar" "$PRIVATE_DIR/phase2/config.yaml" > "$PRIVATE_DIR/phase2/files.sha256"
```

预期：备份保存了原 DNS 符号链接本身和安装文件、v1 状态及控制脚本，并记录了备份校验值。所有校验清单留在私有目录。`systemctl is-enabled` 对禁用的 timer 返回非零是正常状态记录；必须确认 timer 没有待执行试运行。`tar` 中任一必需路径不存在，应先核对实际安装位置，不能用忽略错误选项继续。

### 2.2 仅预检候选，不启动第二份代理

```bash
/usr/local/bin/mihomo -t -d "$PRIVATE_DIR/phase2" -f "$PRIVATE_DIR/phase2-candidates/mihomo.yaml"
/usr/sbin/dnsmasq --test --conf-file="$POLICY_DIR/templates/dnsmasq-claude.conf"
python3 "$POLICY_DIR/templates/egress_guard.py" render > "$PRIVATE_DIR/phase2/guard.nft"
```

预期：Mihomo 和 DNS 配置语法通过，规则文本只涉及自有表与 Fake-IP 网段。候选明确设置 `strict-route: false`、`dns-mode: disabled`、`auto-redirect: false` 及空 `dns-hijack`；不要依赖默认值自动管理系统 DNS。这里不启动 TUN，不执行 nft 内核测试。预检失败不能进入切换。

### 2.3 手动切换

先暂停前面识别的其他官方 Claude 客户端，然后执行：

```bash
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
```

预期：v1 `confirmed` 状态转换为 v2 `claude-only`；拒绝路由、优先级 `2000` 的 Fake-IP 定向查表规则和 nft 规则存在，Mihomo、独立 DNS 正常运行。查表规则先于 Mihomo 的主路由查询，避免黑洞路由遮蔽运行中的 TUN。旧服务 drop-in 仍验证同一路径的 helper，不清空它们。迁移命令只允许已备份的 v1 `confirmed` 状态；失败时不删除保护，不启动应用，直接回滚。

只有前一步全部成功后，才原子替换 DNS 入口，避免在符号链接目标上直接写入：

```bash
test ! -e /etc/resolv.conf.egress-new
test ! -L /etc/resolv.conf.egress-new
install -o root -g root -m 644 "$POLICY_DIR/templates/resolv.conf" /etc/resolv.conf.egress-new
mv -T /etc/resolv.conf.egress-new /etc/resolv.conf
install -o sub2api -g sub2api -m 600 "$PRIVATE_DIR/phase2-candidates/application-phase2.yaml" "$APP_DIR/data/config.yaml"
systemctl start sub2api
```

原 `systemd-resolved` 不停止，保留为回滚来源；应用使用新的显式 DNS 文件。新 DNS 服务不依赖 Mihomo 的启动或停止。新控制器的 `stop` 只停 Mihomo，保留定向保护；本候选不使用旧的自动试运行 timer，因此不要重新启动该 timer。

### 2.4 正常状态的只读核验

```bash
systemctl is-active sub2api mihomo sub2api-egress dnsmasq-claude nginx postgresql@16-main redis-server
curl -fsS --max-time 3 http://127.0.0.1:8080/health
/usr/local/sbin/mihomo-ctl status
ip rule show
ip route show table all
getent ahostsv4 api.anthropic.com
getent ahostsv4 example.com
ip route get 198.18.1.1
ip route get 1.1.1.1
```

预期：官方域名返回 `198.18.0.0/16` 内地址，普通域名返回真实地址；Fake-IP 路由经 `mihomo`，普通目标经正常网卡，保护状态一致。查看既有应用与 Mihomo 日志，确认没有启动错误、配置冲突或反复重启；重启之前暂停的受管客户端，避免复用旧真实 IP 连接。

不要停代理、杀进程、制造断网或发送真实模型请求来测试故障行为。若上述正常核验失败，停止恢复流量并执行第二阶段回滚。若核验通过，在本目录记录操作者、实际版本、时间及只读结果；生产稳定性继续以正常业务观察为准，不能把模拟通过记录成在线故障验收通过。
