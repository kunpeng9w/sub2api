# 网络状态

登录后的共同状态页，入口 `/network-status`，使用紧凑列表展示直连及所有被未删除账号绑定的代理。普通用户只读；完整管理员可以修改规则、异步发起一轮检测。页面刷新和历史查询不会发起测速。

## 独立运行边界

- `monitor.py` 只依赖 Python 3.12 标准库、curl（7.72+，支持 `%{json}`）和 PostgreSQL 客户端 `psql`；无需导入或运行 Sub2API 后端。
- 单进程、单检测线程，串行进行上传和下载，按次生成新连接，不自动重试或回退直连。
- PostgreSQL 只通过 `inventory.sql` 的只读视图读取代理连接配置及关联账号 ID/名称；不读取账号令牌、对话或业务内容。
- SQLite 独立保存规则、31 天内历史、账号关联快照、负载预算。代理地址及用户名/密码只在执行进程内存和 curl 标准输入中使用，不进入 SQLite、HTTP 响应、日志或命令参数。
- Sub2API 后端只有一个独立网关文件，以及用户/管理员路由各一行注册代码。前端单独的 API、页面和语言文件通过路由、侧栏接入，不依赖渠道状态内部实现。
- 主项目后续更新主要检查 `inventory.sql` 与少量路由/菜单接入点。独立目录可以整体移到其他仓库；现有 `ops/scripts/network-compare.*` 仅作参考，没有运行时依赖。

## 数据和测量口径

1. 每 60 秒同步代理及关联关系，按代理 ID 去重。每轮执行前再次同步；同步失败不会使用旧凭据继续检测。停用、过期、软删除但仍被绑定的代理保持可见，并显示不可检测原因。
2. 直连始终存在，关联 `proxy_id` 未设置或为 0 的账号。此关联仅表示数据库绑定，不表示账号调度策略允许直连。直连禁用 curl/环境变量代理，仍遵循系统 DNS、路由、防火墙和 TUN。
3. 仅使用 `https://speed.cloudflare.com/__down?bytes=N` 和 `https://speed.cloudflare.com/__up`。校验 TLS、HTTP 200 和负载字节数；不跟随跳转。SOCKS5 使用远端 DNS。
4. 速度是有效负载字节数 × 8 ÷ 请求总秒数，再转换为 Mbps。包含连接、TLS、传输与响应等待，使用成功样本的中位数；不是官网的测速算法或峰值带宽。耗时列为成功样本 TTFB 的中位数，含连接建立时间。
5. 全部方向无成功样本显示失败，部分失败/缺失方向/低于阈值显示性能下降。预算或整轮超时导致完全未执行的线路显示暂停，不将未执行请求计入检测成功率。超过两倍检测间隔（至少 10 分钟）或规则已改变，显示结果过期。无历史数据的时段为灰色。
6. 历史按当前代理连接配置过滤。代理连接配置变更后不会复用旧结果；账号改名/改绑定后，列表展示当前关系，检测详情保留当时的关系和规则。更换服务 Token 会改变配置指纹，使原 Token 下的结果不再出现在当前配置历史中。
7. 每日预算按 UTC 日累计、持久化，并在每次请求前预留配置的负载大小。失败及超时也消耗预算，避免重复失败绕过限制；不含 TCP/TLS/HTTP 开销，实际线路流量可更大。禁用自动检测仍允许管理员手动检测，手动检测遵守同一预算和 60 秒冷却时间。
8. 所有路径共用同一主机带宽，测试期间可能与业务争用；Cloudflare Anycast 可调度至不同机房。这些结果不能证明 AI 上游可用，也不等价于丢包率。

## 安装（以下为部署步骤，不会由源码自动执行）

建议与 Sub2API 在同一主机、同一网络环境运行，尤其要保证本地代理地址在两个进程中含义一致。系统服务与容器、不同容器之间的 `127.0.0.1` 并不互通；容器部署应共享相应网络命名空间或显式配置可达的私网地址。不要把检测进程绑定在公共入口上。

1. 安装运行依赖（Ubuntu）：`python3`、`curl`、`postgresql-client`。HTTP/2 规则要求 curl 支持 HTTP/2。构建镜像时由 GitHub Actions 安装依赖、构建，不在资源有限的生产机编译。
2. 以数据库所有者对目标库运行 `inventory.sql`。例如：

   ```sh
   sudo -u postgres psql -X -v ON_ERROR_STOP=1 -d sub2api -f ops/network-status/inventory.sql
   sudo -u postgres psql -X -d sub2api
   ```

   在 psql 中用 `\password sub2api_network_reader` 设置独立密码。为该角色配置适当的 `pg_hba.conf` 认证；不要赋予底层 `accounts`/`proxies` 表权限，也不要复用数据库超级用户。视图是外部适配器，不纳入主项目 Ent 迁移。

3. 创建服务用户并安装文件：

   ```sh
   sudo useradd --system --user-group --no-create-home --shell /usr/sbin/nologin sub2api-network
   sudo install -d -m 755 /opt/sub2api-network
   sudo install -d -m 700 /etc/sub2api-network
   sudo install -m 644 ops/network-status/monitor.py /opt/sub2api-network/monitor.py
   sudo install -m 600 ops/network-status/worker.env.example /etc/sub2api-network/worker.env
   sudo install -m 600 ops/network-status/gateway.env.example /etc/sub2api-network/gateway.env
   sudo install -m 644 ops/network-status/rules.example.json /etc/sub2api-network/rules.json
   sudo chown sub2api-network:sub2api-network /etc/sub2api-network/rules.json
   sudo chmod 711 /etc/sub2api-network
   sudo install -m 644 ops/network-status/sub2api-network.service /etc/systemd/system/sub2api-network.service
   ```

   编辑 `worker.env` 填入只读数据库凭据；使用 `openssl rand -hex 32` 生成服务 Token，同时填入两个 env 文件。env 文件保持 root 所有、0600；systemd 在降权前读取。请勿将实际文件、SQLite 或凭据提交到仓库。

4. 为实际 Sub2API systemd 服务添加 drop-in（服务名以实际为准）：

   ```ini
   [Service]
   EnvironmentFile=/etc/sub2api-network/gateway.env
   ```

   容器部署则将这两个后端变量写入容器环境；无需添加 `VITE_` 变量，不把服务 Token 发送给浏览器。`NETWORK_STATUS_URL` 必须是固定的可信私网 HTTP(S) 地址，没有用户名、查询参数或跳转。

5. `systemctl daemon-reload` 后启动 `sub2api-network`，并在安装对应 Actions 构建产物后重启 Sub2API。未配置服务时，页面明确显示服务未配置/不可用，不展示模拟数据。
6. 管理员登录 `/network-status`，先确认同步线路与账号名称，再设置规则并保存。默认自动检测关闭，保存不会立即测速；可点击“立即检测”或显式启用自动检测。

## 规则与接口

`rules.example.json` 提供所有规则字段；`--rules` 仅在首次创建 SQLite 时作为初始配置，后续以页面保存的持久化规则为准。启用周期检测前请按线路数核算预算，例如 4 条线路 × 上下行各 1 MB × 3 次 = 每轮 24 MB，不含额外协议开销。

| 规则 | 范围 |
| --- | --- |
| 检测间隔 | 300–86400 秒 |
| 上传/下载负载 | 各 1000–100000000 字节 |
| 每方向次数 | 1–5 |
| 连接/请求超时 | 1–60 / 1–120 秒；连接不大于请求 |
| 整轮时间上限 | 30–3600 秒 |
| 每日负载预算 | 1000000–10000000000 字节 |
| 最低上传/下载速度 | 0–100000 Mbps |
| 最低成功比例 | 0–1；两方向都须有成功样本 |
| IP / HTTP | ipv4 / ipv6 / auto；http1.1 / http2 |

浏览器访问的 Sub2API API（均需要登录）：

- `GET /api/v1/network-status/snapshot?days=1|7|30`
- `GET /api/v1/network-status/routes/:id/history?days=1|7|30`（最多最近 20 轮）
- `GET /api/v1/network-status/rules`
- `PUT /api/v1/admin/network-status/rules`（完整管理员）
- `POST /api/v1/admin/network-status/run`（完整管理员、异步 202）

独立进程对应 `/snapshot`、`/history?id=N`、`/rules` 和 `/run`，全部要求 `Authorization: Bearer <service token>`。Sub2API 不向它转发用户 JWT、Cookie 或 API Key；未认证用户不能读取任何线路、关联账号名称或检测结果。配置服务不可用时网关只返回脱敏的 503。

## 验证与维护

检查、测试和构建统一由 `.github/workflows/ops-ci.yml` 与 `backend-ci.yml` 执行。独立组件测试包含 PostgreSQL 真实只读视图、账号去重、凭据边界、配置变化、预算并发、过期状态、私有 API 认证，以及 curl 到本地 HTTP 代理夹具的 CONNECT 请求。CI 不调用公共测速站。

停用时禁用独立 systemd 服务并删除 Sub2API 的两个环境变量即可；无需变更账号、代理绑定或主项目业务数据。
