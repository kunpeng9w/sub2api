# Sub2API 原生部署运维说明

目标服务器：`ssh aly`；应用目录：`/home/pk/workspace/sub2api`。

版本固定为官方 `v0.2.18`，Linux amd64。压缩包已通过官方 `checksums.txt` 校验，二进制已运行 `-version` 验证。没有安装 Docker，也没有配置自动更新任务。

## 当前交付状态

2026-10-24 已完成原生安装、管理员初始化及用户手动确认；已接入一个 OpenAI OAuth 账号，账号并发为 1、所属分组为 `openai-default`。管理员和专用测试用户并发均为 1，下游 Key 已创建。

已通过健康检查、回环监听检查、禁用公开注册检查、无效 Key 拒绝检查。使用上游实际返回的模型目录中的 `gpt-6-luna`，已通过串行 10 次短请求、完整流式请求、客户端断开及后续请求，也已从 Mac 经 SSH 隧道使用下游 Key 完成实际调用。应用重启后账号授权、Key、分组和并发设置保留；数据库及 Redis 重启后，无需重启应用即可恢复实际调用。

完整备份已恢复到临时数据库，确认用户、上游账号、下游 Key 和账号分组记录可读；本机已有权限受限的备份副本。30 分钟资源观察已结束：最低可用内存 419.3 MiB、结束时 486.0 MiB，无 OOM 或异常重启；末尾约 5 分钟仍有小量 Swap 活动，保守资源检查未通过。因此功能部署完成，但第一阶段整体验收未通过，后续按计划进行内存升级及复测。

安装脚本保留在 `scripts/install.sh`，用于本机部署维护；重新执行会重启服务，应在维护时段操作。应用初始化使用官方向导后端接口，未修改应用源码或 API 协议。

## 访问与凭据

2026-10-24 已启用公网 HTTPS：后台 `https://idontknow.site`；API 根地址 `https://idontknow.site/v1`。无需 SSH 隧道；原隧道仍可作为备用访问方式。

本次已建立本机隧道。以后端口未占用时，在 Mac 终端中保持以下隧道运行：

```bash
ssh -N -L 18080:127.0.0.1:8080 aly
```

打开 `http://127.0.0.1:18080`。

管理员登录信息保存在远端 `.operator/admin.json`，仅 `pk` 可读。只在私人终端查看，不要贴到聊天、截图或运维记录：

```bash
ssh aly 'cat /home/pk/workspace/sub2api/.operator/admin.json'
```

上游登录授权由用户在本机浏览器完成；日常可在“账号管理”查看该账号状态。下游 API 根地址为 `https://idontknow.site/v1`（备用隧道为 `http://127.0.0.1:18080/v1`），验收使用 Responses 接口 `/v1/responses`，模型为 `gpt-6-luna`。

测试用户和 Key 分别保存在 `.operator/test-user.json`、`.operator/test-key.json`。只在私人终端查看 Key，并配置到客户端凭据存储；不要写入共享说明或提交到代码库。验收帮助程序在服务器上直接读取这些文件，不需要手动复制 Key：

```bash
ssh aly 'python3 /usr/local/lib/sub2api/api.py check'
ssh aly 'python3 /usr/local/lib/sub2api/api.py inventory'
```

`inventory` 只输出账号 ID、平台、状态、并发和分组，不输出账号令牌。

## 检查、启动与停止

```bash
ssh aly 'systemctl status sub2api --no-pager'
ssh aly 'sudo -n journalctl -u sub2api -n 80 --no-pager'
ssh aly 'curl -fsS http://127.0.0.1:8080/health'
ssh aly 'python3 /usr/local/lib/sub2api/api.py check'
ssh -t aly 'sudo /usr/local/lib/sub2api/verify.sh'
ssh -t aly 'sudo systemctl restart sub2api'
ssh -t aly 'sudo systemctl stop sub2api'
ssh -t aly 'sudo systemctl start sub2api'
```

应用仅监听 `127.0.0.1:8080`，PostgreSQL 和 Redis 也仅监听回环地址。用户已在云安全组放行 TCP 80/443；Nginx 监听公网 80/443，转发到本机应用。

`Restart=on-failure` 恢复异常退出；程序的 Web UI“重启”执行正常退出，因此本部署统一使用 `systemctl restart sub2api`。首次初始化结束的正常退出由安装脚本显式重启。

日志进入系统 journal，应用文件日志关闭；单位日志限制为每 30 秒最多 300 条。系统 journal 原有全局磁盘保留策略不修改。

## 资源配置

- PostgreSQL：30 连接、64 MB shared buffers、2 MB work_mem、32 MB maintenance_work_mem。
- 应用数据库池：10 最大连接、2 空闲连接；Redis 池：16 最大、2 最小空闲。
- Redis：64 MB 数据上限，`noeviction`，AOF 开启。上限不包含进程全部开销；达到上限会拒绝写入，而不是淘汰认证/调度数据。
- Go：`GOMEMLIMIT=192MiB` 是垃圾回收软目标，并非内存硬限制。
- 应用入口和网关请求体均限制为 16 MiB。
- 网关纯文本请求上限也设为 16 MiB，满足该版本不大于网关总上限的校验规则。
- 管理员、测试用户、上游账号并发均已核实为 1，并通过应用重启验证。
- 固定 JWT 和 TOTP 密钥保存在权限受限文件内；系统服务仅可写应用 `data` 目录。

## 调用验收与观察

已使用该账号实际返回目录中的 `gpt-6-luna` 完成实际调用。需要复测时运行：

```bash
ssh aly 'python3 /usr/local/lib/sub2api/api.py accept --model gpt-6-luna'
```

该命令依次执行 10 次短普通请求、一次完整流式请求、一次收到文本后取消的流式请求，再发一次普通请求检查取消后的槽位可用性。输出不包含 Key 或模型正文；HTTP 失败时只打印状态码。

资源观察在另一终端运行，至少持续 30 分钟；前段执行调用，末尾至少留 5 分钟空闲：

```bash
ssh aly 'python3 /usr/local/lib/sub2api/observe.py'
```

结果写入 `.operator/resources.jsonl` 和 `.operator/resource-result.json`。要求无 OOM、无应用自动重启、三个服务持续正常；最后 5 分钟可用内存至少 100 MiB，且不持续换页。

取消测试证明客户端可断开、后续调用仍可进行；日志中观察到了断开记录。这不证明上游立即停止生成或计费，该版本可能继续读取上游以完成用量核算。依赖重启后的模型实际调用已经通过。上游失败不自动添加代理；当前机器资源验收失败则建议升级内存，不宣称稳定运行。

## 备份与恢复

安装成功后创建并恢复验证首份备份。timer 每天北京时间 03:30 执行，保留远端最近 3 份。为保证应用配置和数据库一致，备份会短暂停止 Sub2API，完成数据库导出和配置复制后恢复服务。

```bash
ssh -t aly 'sudo systemctl start sub2api-backup.service'
ssh aly 'systemctl list-timers sub2api-backup.timer --no-pager'
ssh -t aly 'sudo /usr/local/lib/sub2api/backup.sh --verify-latest'
```

备份包括数据库、应用数据、操作员凭据、固定密钥、服务定义、数据库/Redis 配置及版本校验信息。启用 HTTPS 后，还包括完整 `/etc/nginx` 和 `/etc/letsencrypt`，后者含证书私钥及 ACME 账户密钥。Redis 运行时缓存不直接恢复；数据库和上游授权保存在数据库备份中。

备份包含全部秘密。`.operator/latest-backup.tar.gz` 是供 `pk` 下载的副本；应至少复制一份到 Mac 的受限目录，权限设为 `600`，不要发布到共享输出目录。

恢复验证使用临时数据库，完成后自动删除；不会覆盖运行中的数据库。验证会核对恢复后的下游 Key 与归档内操作员文件一致，并核对配置与秘密备份中的 JWT、TOTP、数据库和 Redis 密钥一致；不输出这些值。正式恢复时先停止应用、保存故障现场，准备原版本数据库角色，再恢复到独立数据库并核对数据，最后切换配置启动。不要直接覆盖现有库；新版本数据库迁移不能仅靠更换旧二进制回退。

## 目录与本机副本

```text
/home/pk/workspace/sub2api/
├── bin/sub2api
├── data/                    # 仅服务用户可读写；含 config.yaml 和 .installed
├── backups/                 # root 私有；最近三份备份
├── scripts/                 # 部署与验收脚本副本
├── release/                 # 官方压缩包与 checksums.txt
├── audits/                  # 验收报告、审计报告及对应证据
├── validation/              # 验证配置与历史启动日志，见目录内 README.md
├── .operator/               # pk 私有；管理员、测试 Key、备份导出、资源记录
└── README-ops.md
```

报告索引见 [audits/README.md](audits/README.md)；验证配置的来源、差异和用途见 [validation/README.md](validation/README.md)。原根目录的验收报告与 HTTPS 验证结果现已归入 `audits/`，原 `.validation/` 及 `scripts/config.validation.yaml` 已归入 `validation/`。

PostgreSQL 与 Redis 的数据保留在软件包默认目录。应用目录打包不能替代 `pg_dump`。

交付脚本包不含二进制和秘密；固定版本二进制包与官方校验文件保留在远端 `release/`。

Mac 本次备份位于本任务目录 `work/private-backups/sub2api-authorized-backup.tar.gz`，文件权限 `600`，父目录 `700`。该文件含秘密，不在公开交付压缩包内。

## 恢复操作要点

临时恢复验证命令不会覆盖在线数据库。正式恢复应在维护时段先停用备份 timer、停止应用，并保留当前数据库和配置的独立副本。以 root 在权限 `700` 的目录解压经过验证的备份；确认现有 `sub2api` 数据库角色及密码与备份配置一致。在独立数据库恢复，示例命令在服务器 root shell 中执行：

```bash
# 从备份解压目录执行；sub2api_recovered 必须是新数据库名。
runuser -u postgres -- createdb -O sub2api sub2api_recovered
runuser -u postgres -- pg_restore --exit-on-error --no-owner --role=sub2api \
  -d sub2api_recovered < database.dump
runuser -u postgres -- psql -X -d sub2api_recovered \
  -c 'SET ROLE sub2api; SELECT count(*) FROM accounts; SELECT count(*) FROM api_keys;'
```

随后恢复备份 `data/` 中的配置、初始化标记和固定密钥，目录及文件恢复为 `sub2api` 所有、目录 `700`、配置 `600`；在配置中将数据库名改为刚恢复的库。保留原库，不直接覆盖。若恢复的是历史版本，使用 `VERSION` 和 `checksums.txt` 指定的官方二进制；数据库迁移回退必须使用对应数据库备份。检查服务定义、固定密钥及数据库连接一致，再启动应用，执行健康、登录和实际模型调用检查，最后恢复 timer。

OAuth 令牌的有效性仍取决于上游，历史备份中的授权被撤销或刷新后可能需要重新授权。第一阶段没有自动升级任务；升级前必须备份。

## 资源验收未通过项

观察时间：2026-10-24 11:00:49–11:30:53，持续 1804 秒，共 121 个采样点。无 OOM、无异常自动重启，所有服务采样均正常，最低可用内存 419.3 MiB，末尾约 5 分钟最低 454.4 MiB。

脚本采用保守标准：末尾 20 个采样点的 19 个间隔中，若至少一半间隔出现任意 Swap 页活动，则判定“持续换页”。本次有 13 个间隔活动，期间换入 584 KiB、换出 2500 KiB，因此资源检查未通过。即时诊断的内存压力 PSI 平均值为 0，连续 9 秒的 vmstat 间隔没有换页；这些诊断不取消原检查的失败结果，也不能仅凭这一个保守门槛断言机器一定无法承载。

保留全部原始采样、诊断和失败结果，不宣称当前资源通过了完整稳定性验收。后续按原计划进入内存升级和相同负载复测；本次没有变更服务器规格。

## 官方资料

- https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.18
- https://github.com/Wei-Shaw/sub2api/blob/v0.2.18/deploy/README.md
- https://github.com/Wei-Shaw/sub2api/blob/v0.2.18/deploy/config.example.yaml
- https://github.com/Wei-Shaw/sub2api/blob/v0.2.18/deploy/sub2api.service


## 公网 HTTPS 运维

Nginx 配置：`/etc/nginx/sites-available/sub2api`。证书目录：`/etc/letsencrypt/live/idontknow.site/`。使用 Let's Encrypt ECDSA 证书；当前证书到期日期 2027-01-22，准确时间以证书及 `certbot certificates` 为准。

80 端口保留 HTTP-01 验证路径，其他请求重定向至 HTTPS；443 端口反向代理至 `127.0.0.1:8080`。关闭响应缓冲以支持 SSE，请求体上限 16 MiB，读取超时 300 秒。访问日志不记录查询参数或认证头。

Certbot 使用 webroot 模式，避免每次续期自动改写 Nginx 配置。未登记联系邮箱，续期由 `certbot.timer` 自动执行；需通过日志检查失败情况。续期部署钩子先检查 Nginx 配置，再 reload 服务。

```bash
ssh aly 'sudo -n nginx -t'
ssh aly 'systemctl status nginx certbot.timer sub2api-web-routing --no-pager'
ssh aly 'sudo -n certbot certificates'
ssh aly 'sudo -n certbot renew --dry-run --run-deploy-hooks --no-random-sleep-on-renew'
ssh aly 'sudo -n journalctl -u certbot.service -n 80 --no-pager'
```

当前服务器启用了 Mihomo/TUN。为保证 Web 回程，`sub2api-web-routing.service` 在开机时添加优先级 101/102 的 TCP 源端口 80/443 回主路由规则，优先级 100 原有 SSH 规则保留。根用户管理的辅助脚本位于 `/usr/local/lib/sub2api/`。

证书服务域名 `~letsencrypt.org` 使用 eth0 的 DNS；`acme-network.py` 解析正式及测试 ACME 服务地址，并维护对应的 IPv4 主路由，避免证书申请和续期依赖 TUN。续期前置钩子会重新生成配置和路由，DNS 查询带短暂失败重试。该运行时配置会在续期时重建，恢复备份后可先手动执行脚本：

```bash
ssh aly 'sudo -n /usr/local/lib/sub2api/acme-network.py'
```

恢复 HTTPS 时还需恢复 `config/nginx/`、`letsencrypt/`、`systemd/sub2api-web-routing.service` 与辅助脚本，并重新安装 Nginx/Certbot 软件包。保护私钥权限；启动路由服务，检查 Nginx 配置后再启动 Nginx，启用 `certbot.timer` 并执行续期演练。

公网普通和完整流式请求已使用真实上游 `gpt-6-luna` 验证。HTTPS 配置没有重新进行 30 分钟资源验收，原第一阶段资源检查未通过的结论保留。


## Git 版本管理

本目录使用独立的本地 Git 仓库，默认分支为 `main`，用于维护部署脚本、服务配置、运维文档、审计记录和验证模板。这里运行的是官方发行二进制，不包含上游应用源码及其提交历史。初始化时未配置远程仓库。

`.gitignore` 排除 `.operator/` 凭据、`data/` 线上配置和数据、`backups/` 备份、`bin/` 二进制、`release/` 发行文件、`scripts/ubuntu-packages/` 安装包，以及日志、Python 缓存和本地环境文件。`validation/` 中的 YAML 仅含测试占位值，可随代码管理；不要将真实凭据填入这些模板。

日常变更先检查差异，再选择文件提交：

```bash
cd /home/pk/workspace/sub2api
git status --short
git diff
git add <本次修改的文件>
git diff --cached
git commit -m "说明本次变更"
```

提交前检查是否包含密码、令牌或线上数据，不要使用 `git add -f` 强行纳入忽略项。Git 用于记录脚本与文档变更，数据库和线上配置仍按本文的备份与恢复流程管理。
