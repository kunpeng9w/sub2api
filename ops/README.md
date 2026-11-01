# Sub2API 源码与服务器运维

本分支源码以官方 **v0.2.19**（`dece26153621959ccd7635814f4b2d08304e681a`）为基线，保留官方完整源码和本地定制提交历史。`origin` 是 `kunpeng9w/sub2api`，`upstream` 是 `Wei-Shaw/sub2api`。已有部署仓库的初始化提交也保留在历史中。升级构建说明见 [v0.2.19 定制升级](changes/20261101-upgrade-v0.2.19/README.md)；源码基线升级不代表线上已部署，以下带日期的部署记录保留其历史含义。

`backend/`、`frontend/`、`deploy/` 等目录沿用官方结构；服务器额外配置、脚本、审计及验证材料集中在本目录。官方文档仍以官方行为为准，本页说明本机实际差异。

## 待部署的代理策略候选

2026-10-28 新增[严格绑定与 Claude 定向分流交付](changes/20261028-egress-policy/README.md)，包含修正记录、模拟测试、配置候选和两阶段手动部署/回滚教程。**未部署**；现有运行状态不因源码或 CI 改变，验证结果以该目录报告为准。

## 当前部署与源码对应关系

服务器 `aly` 的运行目录仍为 `/home/pk/workspace/sub2api`，systemd 启动 `bin/sub2api`。它是基于 v0.2.18 的既有定制构建：普通 OpenAI Codex 配置模板的首选和兜底模型为 `gpt-6.1-sol`，生成 `supports_websockets = true`，旧线上构建不生成 `model_catalog_url`；本次源码已在 `base_url` 下一行恢复远程模式的 `model_catalog_url = "<base_url>/models"`，待用户手动部署。模型目录的原有选择逻辑、WebSocket 专用页签和后端 API 保持原样。

这些改动已恢复到前端源码及对应测试，不再依赖 `/tmp` 补丁作为维护来源。当前线上程序由此前的外部构建产生，SHA256、原始构建信息、官方基线和本次核对状态保存在 [deployed-build.json](deployed-build.json)。新仓库提交号不代表该旧程序的构建提交，也不承诺不同工具链产生字节相同的程序。

本次迁移只更新仓库受跟踪文件，未替换程序、安装配置、重启服务、修改路由或迁移数据库。数据库和线上业务设置不在 Git 中；配置模板里的默认并发不代表数据库中的当前用户或账号并发。

## 运行结构与核实状态

公网 HTTPS → Nginx → `127.0.0.1:8080` Sub2API → PostgreSQL / Redis；上游流量经 Mihomo TUN 和代理节点。程序包含前端静态资源，服务器无需单独运行前端开发服务。当前运行模式是 `simple`，不能按完整计费与额度控制模式理解。

2026-10-25 核对时应用、Nginx、PostgreSQL、Redis 和 Mihomo 均运行，本机 `/health` 正常。Nginx 已配置 SSE 和 WebSocket 转发。出口保护已安装，`confirmed` 状态下只允许 sub2api 用户进行必要的回环通信或经 mihomo 接口出站；服务启动前验证规则。

**待修复：** `sub2api-web-routing.service` 显示 active/exited，但内核中预期的优先级 101/102、TCP 源端口 80/443 查主路由表规则缺失。本目录保存现有服务与脚本的配置意图，并不表示规则现已生效；本次未修复，不能用 oneshot 的 active 状态代替实际规则检查。

历史验收与审计见 [audits/README.md](audits/README.md)。历史发现不自动代表当前状态，也不会因本次整理而视为已修复；原资源验收未通过的结论保留。[初始交付说明](audits/deployment-handover-20261024.md) 仅用于历史追溯。

## 配置来源与部署对应表

配置以本次读取的线上文件为依据；带 `.example` 的文件含占位值，必须在受限目录填写秘密，不能直接覆盖线上文件。占位符不会由应用自动替换。执行下面的部署步骤属于单独维护操作，本次没有执行。

| 仓库内容 | 实际位置 | 用途与验证 | 部署方式 |
|---|---|---|---|
| [config/nginx/sub2api.conf](config/nginx/sub2api.conf) | `/etc/nginx/sites-available/sub2api` | HTTPS、SSE、WebSocket；`sudo nginx -t` | 备份旧配置，安装文件并检查，通过后 `systemctl reload nginx` |
| [config/systemd/](config/systemd/) 的应用、备份及回程单元 | `/etc/systemd/system/` 下同名单元 | 应用运行参数、每日备份、回程规则意图；`systemd-analyze verify`、`systemctl cat/show`、`ip rule show` | 备份后逐项安装，`systemctl daemon-reload`；重启或启用相关单元需另行维护 |
| [config/systemd/mihomo.service](config/systemd/mihomo.service) 与 [config/mihomo/config.yaml.example](config/mihomo/config.yaml.example) | `/etc/systemd/system/mihomo.service`、`/etc/mihomo/config.yaml` | TUN、DNS、分流和代理；`mihomo-check`、`mihomo -t` | 私下补齐代理参数，备份并设置配置权限 600，预检后按控制器流程启用 |
| systemd 中的出口保护、回滚单元及依赖 drop-in；[scripts/egress-guard/](scripts/egress-guard/) | `/etc/systemd/system/`、`/usr/local/libexec/`、`/usr/local/sbin/mihomo-ctl` | 持久出站约束；`sudo mihomo-ctl status` 检查规则和状态 | 维护时显式执行安装器；详细语义见该目录 README，不能手动清空防火墙 |
| [config/application.yaml.example](config/application.yaml.example) | `data/config.yaml` | 应用、连接池、JWT/TOTP、日志设置；配合健康与登录检查 | 在权限 700 的私有目录补齐秘密，安装为 sub2api 所有、权限 600；应用重启单独执行 |
| [config/postgresql/99-sub2api.conf](config/postgresql/99-sub2api.conf) | `/etc/postgresql/16/main/conf.d/99-sub2api.conf` | 本机监听和资源参数；读取 `pg_settings` 验证 | 备份后安装，由数据库参数决定 reload 或 restart |
| [config/redis/redis.conf.example](config/redis/redis.conf.example) | `/etc/redis/redis.conf` | 线上有效指令的脱敏快照；认证连接、`PING` 和关键 `CONFIG GET` 验证 | 私下填写密码，与应用配置保持一致；备份后在维护时部署 |
| [config/certbot/idontknow.site.conf.example](config/certbot/idontknow.site.conf.example)、续期脚本 | `/etc/letsencrypt/renewal/`、`renewal-hooks/pre/10-sub2api-network`、`renewal-hooks/deploy/20-sub2api-nginx` | ACME webroot 与续期前后网络处理；`certbot certificates`、续期演练 | 示例仅供恢复参考；安装脚本为 755，安装系统 Certbot 并保留/恢复私有账户与证书材料 |
| [scripts/](scripts/) 下的 API、备份、观察和网络辅助脚本 | `/usr/local/lib/sub2api/` 下同名文件 | 验收、备份、资源采样和网络处理；语法检查与针对性验证 | 按文件备份并安装为 root 所有、权限 755；不会因 Git 更新自动部署 |
| [scripts/mihomo-check](scripts/mihomo-check)、[scripts/mihomo-ssh-route](scripts/mihomo-ssh-route) | `/usr/local/libexec/` 下同名文件 | 代理预检和 SSH 回程；语法检查、实际规则检查 | 维护时单独安装；不要为验证而执行 add/del 改动线上规则 |

部分脚本目录中的配置文件是指向 `config/` 的兼容符号链接，只有一份实际配置内容。保留这些链接以兼容既有安装器和测试。

## Git 跟踪与私有运行文件

Git 跟踪源码、锁文件、配置模板、脚本、说明及脱敏证据。运行目录里的 `bin/`、`release/`、`data/`、`.operator/`、`backups/`，遗留安装包、日志、依赖和构建输出均不跟踪。PostgreSQL 与 Redis 数据在系统软件包的数据目录，不在本仓库。

不提交 `/etc/sub2api-native-secrets.json`、真实应用配置、代理认证、证书/ACME 私钥或数据库备份。私密文件只登记位置与用途，不把内容复制到文档。历史 `scripts/ubuntu-packages/`、`validation/logs/` 等忽略文件仍可留在原路径；它们不是源码，迁移不会移动或清理它们。

以后修改配置：先更新本目录模板并审查，单独部署到系统位置，验证后记录状态；Git 提交本身不会更新 `/etc` 或重启服务。线上应急修改后及时同步回仓库，避免再次漂移。

## 构建、更新与部署分离

GitHub Actions 保留官方 CI，另由 `Source and operations verification` 工作流运行前端模板测试、前端生产构建和嵌入前端的 Linux amd64 编译，以及运维脚本检查。使用保留安全修复的仓库锁文件、Node 22、pnpm 9 和 `backend/go.mod` 指定的 Go 工具链。构建顺序是前端先、后端后；根目录默认 `make build` 不能替代这里的嵌入前端构建流程。

构建程序通过 `backend/scripts/resolve-version.sh` 读取版本，当前非标签定制分支使用 `backend/cmd/server/VERSION` 中的 `0.2.19`，同时写入真实 Git SHA、UTC 构建时间及 `source` 类型。工作流上传 Linux amd64 构建包、SHA256 和来源信息作为 Actions artifact，保留 7 天，不自动部署。2026-10-27 本次构建经用户授权，通过独立临时交付任务和产物分支完成 Git SSH 下载；下载校验后清理临时产物分支及写入任务，源码保留在 `build/codex-model-catalog` 分支，常规工作流继续使用只读仓库权限。用户手动部署，保留 `deployed-build.json` 登记的官方程序备份，不额外备份当前定制程序。

更新官方版本时，在独立开发克隆里执行 `git fetch upstream --tags`，选择明确版本，建立更新分支并合并对应标签，解决冲突后运行 CI。只有升级任务才变更版本基线和数据库兼容性要求。推送时明确指定自己的分支，不执行 `git push --mirror` 或 `git push --tags`。

`scripts/install.sh --bootstrap-official` 是历史官方二进制初装工具，只允许全新实例，明确拒绝覆盖已初始化的定制程序；它不是本仓库源码构建程序的部署方式。已安装的服务器脚本不会因这里的源码更新而自动改变。

备份 timer 每天新加坡时间 03:30 运行，保留最近三份，备份时短暂停止应用。当前已安装备份脚本的 VERSION/发行校验仍指向官方包，且没有覆盖全部新加的出口保护与代理恢复材料；恢复定制程序时必须结合本仓库部署记录及独立保存的定制构建包，不能把旧备份视为整个运行框架的完整恢复包。本次只记录该差异，不更新线上备份脚本。

## 本地仓库校验

安装 PyYAML 后，可从仓库根目录执行 `python3 ops/scripts/check-repository.py`，检查完整历史、运行文件排除、脱敏模板、语法和文档链接。已有隔离测试可在 `ops/scripts/egress-guard/` 执行 `python3 -m unittest -v test_state.py test_install.py`，不会修改线上服务和防火墙。

## 只读运行检查

```bash
systemctl is-active sub2api nginx postgresql@16-main redis-server mihomo
curl -fsS http://127.0.0.1:8080/health
sudo mihomo-ctl status
ip rule show
sha256sum /home/pk/workspace/sub2api/bin/sub2api
```

健康检查只证明本机应用响应，不代替上游模型、流式、WebSocket、公网链路或整机重启验收。审计和验证中的历史时间保留原始时区，当前操作记录使用 Asia/Singapore。
