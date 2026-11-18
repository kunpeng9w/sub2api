# v0.2.22 手动部署交接

2026-11-17（Asia/Singapore）完成前置准备，尚未部署。线上保持 v0.2.21 定制程序，实际 RUN_MODE=standard。本文件中的停服、安装和恢复命令仅供操作者在维护窗口执行。

## 已准备

- 部署目录：`/home/pk/workspace/sub2api/release/v0.2.22/9339f5c5d`。
- 固定源码：`9339f5c5d7fcfd8e1ee33ba9567b6537e98b506c`，官方基线 v0.2.22。
- 使用分支 push 的 Actions 44050220274 安装包，而非 PR 合成提交包。七个分支工作流和七个 PR 工作流均成功；原始日志、job 信息及完整验收在部署目录 `ACCEPTANCE.md`、`evidence/`。
- 压缩包 SHA256：`1784c8b1dd9d7053f16838384c8a07ce750cffa79301c82573b6d8c88655b9ad`。
- 新程序 SHA256：`35a4363f8602e78eb5ed9d9f2681d22f29a7c14acff928eb43cbfd0e81004cd2`。
- 当前程序：0.2.21，提交 `0161258af6ca167f5885c145b454d3a982839939`，SHA256 `631c007affe5c35ef1cf532d899e901c7ceeb6d5f587c5b7901e9f80f5af1bde`。
- 只读核对生产 PostgreSQL 16.15：304 份已应用迁移均与候选源码匹配，按执行器的 TrimSpace 后 SHA256 算法比较；待执行只有 `251_group_force_openai_ultrafast.sql`、`251_group_usage_rollup_nonblocking_build.sql`。两份 SQL 均保留上游原文。
- 私有快照：`/home/pk/workspace/sub2api/.operator/predeploy-v0.2.22-20261117`。已保存当前程序、配置、systemd unit/drop-ins、gateway.env、在线数据库 dump 和全局角色。目录 root 所有、权限 700，含秘密，禁止提交 Git。在线 dump 已完成并读取归档目录、核对 SHA256；尚未进行实际恢复演练。
- 前置快照期间应用持续运行；在线 dump 不能替代最终停服后的恢复点。未替换程序、未重启服务、未运行生产迁移、未调用真实上游。

`ops/README.md` 与 `ops/deployed-build.json` 含历史状态，不能用来判断本次部署。以实际 unit/drop-ins、部署目录 PREDEPLOY.json 和最终操作记录为准。当前 sub2api、nginx、PostgreSQL、Redis 为 active；Mihomo 为 inactive，出口控制报告 consistent=true、mode=claude-only、phase=confirmed。本次保留该状态。

## 你执行最终部署

先安排维护窗口、暂停外部业务写入和其他应用实例。备份的实际恢复可在独立环境验证；本机没有运行恢复测试或任何编译/测试。已有 Actions 证明迁移回归通过，不证明该生产备份可恢复。不要运行历史 backup.sh 来替代下面的最终备份，它有自动重启及旧版本标记逻辑。

以下在一个 root Bash 会话逐段执行。先进入 root shell，再设置变量、核对包与旧程序；任一步失败即停止。

```bash
sudo -i
set -euo pipefail
umask 077
base=/home/pk/workspace/sub2api
candidate="$base/release/v0.2.22/9339f5c5d"
# 与现有备份任务使用相同的锁；若获取失败，等备份结束再操作。
exec 9>/run/lock/sub2api-backup.lock
flock -n 9
cd "$candidate"
sha256sum -c SHA256SUMS
sha256sum -c sub2api.sha256
grep -Fx 'source_commit=9339f5c5d7fcfd8e1ee33ba9567b6537e98b506c' build-info.txt
printf '%s  %s\n' '631c007affe5c35ef1cf532d899e901c7ceeb6d5f587c5b7901e9f80f5af1bde' "$base/bin/sub2api" | sha256sum -c -
# 读取版本不会启动应用。
./sub2api -version
checkpoint="$base/.operator/deploy-v0.2.22-$(TZ=Asia/Singapore date +%Y%m%dT%H%M%S)"
mkdir -m 700 "$checkpoint"
cp -a "$base/bin/sub2api" "$checkpoint/sub2api.previous"
cp -a "$base/data" "$checkpoint/data"
cp -a /etc/systemd/system/sub2api.service /etc/systemd/system/sub2api.service.d "$checkpoint/"
cp -a /etc/sub2api-network/gateway.env "$checkpoint/"
printf '%s\n' "$checkpoint" > "$base/.operator/v0.2.22-checkpoint-path"
```

确认所有其他写入者已经停止后，停服务并产生最终数据库恢复点。以下失败时应用保持停止，先处理失败原因，不继续安装。

```bash
systemctl stop sub2api
! systemctl is-active --quiet sub2api
# 停服后再刷新 data 快照，包含最终状态。
cp -a "$base/data/." "$checkpoint/data/"
runuser -u postgres -- pg_dump -Fc sub2api > "$checkpoint/database.dump.partial"
mv "$checkpoint/database.dump.partial" "$checkpoint/database.dump"
runuser -u postgres -- pg_dumpall --globals-only > "$checkpoint/globals.sql"
pg_restore --list "$checkpoint/database.dump" > "$checkpoint/database.contents.txt"
(cd "$checkpoint" && sha256sum sub2api.previous database.dump globals.sql > SHA256SUMS && sha256sum -c SHA256SUMS)
# 仅验证目录可读不等于恢复演练。这里是安装前的最终检查点。
```

安装复用现有配置和 drop-ins。首次启动将执行两份迁移；失败时保留恢复点和日志，不反复尝试启动。

```bash
install -o root -g root -m 0755 "$candidate/sub2api" "$base/bin/sub2api.v0.2.22.pending"
printf '%s  %s\n' '35a4363f8602e78eb5ed9d9f2681d22f29a7c14acff928eb43cbfd0e81004cd2' "$base/bin/sub2api.v0.2.22.pending" | sha256sum -c -
mv -f "$base/bin/sub2api.v0.2.22.pending" "$base/bin/sub2api"
date -u +%Y-%m-%dT%H:%M:%SZ > "$checkpoint/start-time.txt"
if ! systemctl start sub2api; then
  systemctl stop sub2api
  journalctl -u sub2api --since "$(cat "$checkpoint/start-time.txt")" --no-pager > "$checkpoint/startup.log"
  exit 1
fi
ready=0
for attempt in {1..30}; do
  if systemctl is-active --quiet sub2api && curl -fsS --max-time 2 http://127.0.0.1:8080/health; then
    ready=1
    break
  fi
  sleep 2
done
journalctl -u sub2api --since "$(cat "$checkpoint/start-time.txt")" --no-pager > "$checkpoint/startup.log"
if [ "$ready" != 1 ]; then
  systemctl stop sub2api
  echo "启动未就绪；保留停止状态，检查 $checkpoint/startup.log"
  exit 1
fi
runuser -u postgres -- psql -X -v ON_ERROR_STOP=1 -d sub2api -c "SELECT filename, checksum, applied_at FROM schema_migrations WHERE filename LIKE '251_%' ORDER BY filename;"
systemctl show sub2api -p MainPID -p ActiveState -p NRestarts
sha256sum "$base/bin/sub2api"
```

核对两份 251 迁移均登记且日志无迁移失败、校验和错误或重启循环。浏览器检查公网 HTTPS、登录、standard 模式、账号/权限、监控可见范围、排序、手动检测、额度窗口；用自己的真实凭据少量验证 HTTP/SSE/WS 和常用模型。普通 OpenAI OAuth 的额度刷新沿用 v0.2.22 上游模型探测行为，可能产生上游请求及额度消耗；受限管理员仅额度 API。检查严格出口及内存后再恢复业务流量。健康端点成功不代表这些业务检查通过。

完成后保存版本、SHA、迁移和业务验收结果到 checkpoint，更新部署记录。退出 root shell即释放备份锁；维护结束前确保没有遗留持锁会话。PR #7 仍未合并，合并需保留 merge commit 历史，不能 squash/rebase；部署不依赖先合并 PR。

## 回滚

保持全部写入者停止。若新程序从未启动、确认迁移未执行，可以原子装回 checkpoint 中的 sub2api.previous 后启动。只要新程序尝试过启动，就先检查迁移账本，不假定数据库没有变化。

若迁移已经执行或无法确定，先停止新服务并保存失败日志及当前数据库副本。恢复涉及丢弃恢复点之后的数据，必须先确认恢复点与业务补偿；不要仅换回旧二进制、删除迁移记录或手动删列。

以下仅在明确选择整体恢复时执行；checkpoint 从私有路径记录读取，备份必须校验通过。停止其他写入者后，先备份故障库，再重建原数据库并恢复。角色仍存在时无需执行 globals.sql。

```bash
sudo -i
set -euo pipefail
umask 077
base=/home/pk/workspace/sub2api
checkpoint=$(cat "$base/.operator/v0.2.22-checkpoint-path")
exec 9>/run/lock/sub2api-backup.lock
flock -n 9
systemctl stop sub2api
(cd "$checkpoint" && sha256sum -c SHA256SUMS)
runuser -u postgres -- pg_dump -Fc sub2api > "$checkpoint/database.failed-$(date -u +%Y%m%dT%H%M%SZ).dump"
# 下一条开始替换数据库，确认已选择此恢复点再继续。
runuser -u postgres -- dropdb sub2api
runuser -u postgres -- createdb -O sub2api sub2api
runuser -u postgres -- pg_restore --exit-on-error -d sub2api < "$checkpoint/database.dump"
install -o root -g root -m 0755 "$checkpoint/sub2api.previous" "$base/bin/sub2api.rollback.pending"
mv -f "$base/bin/sub2api.rollback.pending" "$base/bin/sub2api"
# 本次安装不修改配置；若操作者另改过配置，应逐项恢复快照并保留 owner/mode。
systemctl start sub2api
curl -fsS --max-time 5 http://127.0.0.1:8080/health
```

恢复失败时保持服务停止，依据 startup.log、数据库日志和快照处理；不要恢复流量。恢复成功后仍需核对登录、权限与关键业务。所有本次前置检查均不代表生产迁移、恢复或真实上游验收已经完成。
