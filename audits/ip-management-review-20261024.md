# Sub2API IP 与流量机制检查

检查时间：2026-10-24 19:35–19:41（Asia/Singapore，UTC+8）。对象为本机实际部署的官方 v0.2.18，提交 ac1b8f1271be0d674fa6f074403c1410f30a3a81。

结论：当前配置有实际缺口。已复现客户端 IP 可被伪造，已观察到非业务流量被送入代理并反复失败；账号并发和运行模式也缺少总量保护。不能仅凭现有记录断言发生了盗用、严重刷量或上游风控。

本次未修改在线配置、数据库数据或服务状态，未执行模型调用或压力测试。仅通过本机 Nginx 发起两次公开设置 GET，验证 IP 解析；其余为配置、数据库、日志与对应版本源码的只读核对。证据见同目录 `ip-audit-evidence-20261024.json`。

## 实际请求路径

入站：访客 → Nginx 443 → 127.0.0.1:8080 → Sub2API。

出站：Sub2API 未绑定应用代理（账号 proxy_id 为空，代理表无记录）→ 系统 Mihomo TUN → PROXY 组 → US-Proxy → 上游。

PROXY 是 select 类型且只有一个 HTTP 代理节点，没有配置自动测速选路或负载均衡。配置没有主动轮换节点，但节点是否由供应商轮换出口 IP，当前证据无法确认。应用中的“无代理”也不代表实际公网直连，因为系统 TUN 仍会接管连接。

## 1. 高优先级：客户端 IP 可被伪造

实际设置 `api_key_acl_trust_forwarded_ip=true`，自定义 IP 头列表为空，未显式配置 `server.trusted_proxies`。

Nginx 正确覆盖了 `X-Real-IP` 和 `X-Forwarded-For`，但没有覆盖或删除 `CF-Connecting-IP`。应用的兼容解析优先读取 `CF-Connecting-IP`，且不先验证连接是否来自 Cloudflare。

实际验证：两次请求均访问 `/api/v1/settings/public`，均经过本机 Nginx、返回 200。

| 请求 | 应用日志中的 client_ip |
|---|---|
| 无额外 IP 头 | 127.0.0.1 |
| 增加 CF-Connecting-IP: 203.0.113.77 | 203.0.113.77 |

这已经证明 IP 归属记录可被伪造。源码进一步确认，API Key IP 黑白名单、登录等按 IP 限流、公开面板按 IP 限流和会话绑定共用这套安全 IP 解析。因此攻击者可以改变限流桶或污染审计来源；将来配置 Key 白名单时，也可能伪装成允许的 IP。当前四个有效 Key 的 IP 黑白名单均为空，本次未改变任何 Key 来测试实际 ACL 绕过。攻击者仍需有效 Key 才能正常调用模型，伪造 IP 本身不能替代 Key。

建议：先在 Nginx 清除未经验证的 `CF-Connecting-IP`；随后显式设置 `server.trusted_proxies: [127.0.0.1/32]`，通过后台关闭“信任转发 IP”兼容开关，使用可信代理链。必须配套实施：只关闭兼容开关而不设可信代理，会把所有公网访客识别为 127.0.0.1，引发共享限流桶、白名单误拒绝或公开面板跳过 IP 限流。若后续接入 CDN，应另行限制可信 CDN 来源并验证真实 IP 传递链。

源码：`backend/internal/pkg/ip/ip.go:114`、`backend/internal/server/middleware/session_binding.go:18`、`backend/internal/middleware/rate_limiter.go:116`、`backend/internal/server/middleware/api_key_auth.go:135`。

## 2. 已观察到异常：云监控和内网请求误走美国代理

Mihomo 使用全局 TUN，末尾是 `MATCH,PROXY`。直连例外只覆盖少数网段、元数据地址及 NTP，没有完整覆盖这台云主机实际使用的内网服务和监控端点。

2026-10-24 19:02 后约 35 分钟的日志中，存在以下现象：

- `100.100.100.10:80/443` 被送入代理，反复出现 `502 Bad Gateway` 和 `context deadline exceeded`。
- `metrichub-cms-cn-shanghai.aliyuncs.com`、`cms-cloudmonitor.aliyun.com` 等云监控请求也经 US-Proxy。
- 其他 `cmsproxy`、`metrichub` 端点存在反复失败记录。

因此“未调用模型但代理仍消耗流量”在这套部署中确实可能发生，且已有非业务代理连接和失败重试证据。这些日志条数不是唯一请求数，也不是流量字节数；无法据此声称浪费了多少 GB。

建议：按云平台资料和实际使用范围补齐内网、监控端点的直连策略；避免未经核实就把所有云服务域名或大网段一律放行。长期可选择只为 Sub2API 的上游请求配置显式代理，减少全局 TUN 对系统服务的影响，但迁移时要覆盖 OAuth 刷新、模型请求和 WebSocket，并验证未发生直连泄漏。

## 3. 高优先级：当前并发与限额不足以约束总量

数据库实际值如下，已经不同于早期 README 中的“全部并发 1”：

| 对象 | 当前值 |
|---|---|
| 唯一有效 OpenAI OAuth 上游账号 | 并发 10 |
| 管理员用户 | 并发 10，rpm_limit=0 |
| 测试用户 | 并发 5，rpm_limit=0 |
| 有效下游 Key | 4 个，均无 IP 黑白名单；总额度及 5h/1d/7d 金额窗口均为 0 |
| 运行模式 | simple |
| simple_mode_key_rate_limit_enabled | 未配置，源码默认 false；未发现对应环境覆盖 |

源码显示，简易模式默认提前返回，跳过余额、额度窗口和业务 RPM 检查。因此不能仅在后台填 RPM 或额度就认为这些限制生效；需要核对运行模式和实际准入路径。该版本可显式启用简易模式 Key 金额窗口，但这仍不等于 RPM 限流，也不自动启用总额度或余额控制。

用户和账号并发控制仍存在：Redis Lua 原子检查槽位，用户级限制按用户共享，多开同一用户的 Key 不会直接将其用户并发成倍增加；不同用户仍共同受上游账号的并发限制。当前主要缺口是“10 个同时运行”不能限制一小时累计调用量或 token 总量，也不能判断对上游额度是否合适。

建议：在没有长请求与并行验收前，先采用较低的账号并发（例如 1–2）作为保守起点，再结合真实负载提高；公网入口补充适合实际客户端的请求速率与连接数限制。业务总量保护应选择 standard 模式的实际限额机制，或明确启用并验证所需的简易模式 Key 金额窗口。不要把 Nginx 请求速率限制当作 token 配额，WebSocket 单条连接可能承载多轮请求。

源码：`backend/internal/service/billing_cache_service.go:744`、`backend/internal/service/gateway_usage_billing.go:94`、`backend/internal/repository/concurrency_cache.go:80`。

## 4. 高优先级：上游出口依赖系统 TUN，缺少应用层固定绑定

当前只有一个代理节点，正常日志中的 `chatgpt.com` 连接都命中 US-Proxy；没有看到配置层面的自动换节点。但账号未绑定代理，应用环境也没有 HTTP_PROXY/HTTPS_PROXY/ALL_PROXY。因而出口约束依赖系统路由与 TUN 的可用性。

TUN 停止或路由撤销后，主路由表仍有 eth0 默认路由，存在新连接改走服务器公网出口的条件。是否成功访问上游取决于网络和目标服务，不能保证故障时一定停流，也不能宣称目前已发生出口泄漏。供应商侧是否固定出口 IP 仍需独立验证。

建议：需要固定出口的账号绑定明确的代理，并保持故障时拒绝或报错，避免配置为失败后直连；必要时另加进程出站约束。账号授权刷新、普通请求和 WebSocket 必须使用一致出口。业务故障恢复不应靠频繁换 IP 来掩盖。

## 5. 中优先级：HTTPS 回程保护规则已不在内核中

`sub2api-web-routing.service` 显示 active/exited，但其脚本应安装的 priority 101/102、TCP 源端口 80/443 查 main 规则已经缺失；只有 SSH 的 priority 100 例外仍在。Mihomo 于 19:01:16 重新启动，路由服务没有与它建立重启后的重装关系。不能仅凭这个时间顺序确定规则由谁删除。

只读路由探测 `ip route get 1.1.1.1 ipproto tcp sport 443 dport 54321` 返回 mihomo/table 2022；同样的源端口 22 探测返回 eth0/main。这证明预期保护规则缺位。该探测不模拟现有连接的全部 conntrack 状态，因此不能据此断言所有 HTTPS 连接已损坏。

这会增加公网连接、重连或回程绕行的风险，客户端若自动重试，可能进一步增加请求量。建议将回程规则维护与 TUN 生命周期关联，并以真实规则检查和独立公网访问验收为准，不能仅检查 oneshot 服务的 active 状态。

## 6. 需要正确理解：客户端断开不等于上游停止

对应版本多条流式路径在客户端断开后继续读取上游，目的是收集最终 usage；当前日志也有 `ingress_ws_client_disconnected_drain`。因此关闭客户端后仍可能有短时间流量和额度消耗。

这有明确的实现原因，但客户端若立即重新提交任务，可能与旧任务形成重叠消耗。本次没有用长生成或重复调用测试其最坏上限，也不能把这类行为归因于 IP 异常。

## 已有流量能说明什么

2026-10-24 19:36 左右的数据库快照约 524 条 usage 记录，按完成时间分桶的分钟峰值约 17 条。持续请求期间数据仍在增长，各次查询数值不是单一事务快照。一次 WebSocket 连接可能对应多轮 usage，Nginx 请求条数不能直接等同模型调用次数；失败请求也不一定进入 usage。

现有证据没有证明业务侧出现失控刷量或账号被盗用，但也不能据此认定安全。已经明确存在的是：IP 身份可伪造、系统流量误入代理并重试、业务总量限制缺口，以及回程规则缺失。

建议修正顺序：清理 IP 头并建立可信代理链 → 修正系统分流和回程规则生命周期 → 建立有效的业务速率与金额保护 → 再提高并发。上线修正后应做少量、定向验证，包括伪造头不再改变 IP、超限返回 429、TUN 故障不直连、重启后回程规则仍存在。

## 对应版本官方源码

- [IP 解析](https://github.com/Wei-Shaw/sub2api/blob/v0.2.18/backend/internal/pkg/ip/ip.go)
- [可信代理初始化](https://github.com/Wei-Shaw/sub2api/blob/v0.2.18/backend/internal/server/http.go)
- [业务准入与简易模式](https://github.com/Wei-Shaw/sub2api/blob/v0.2.18/backend/internal/service/billing_cache_service.go)
- [原子并发控制](https://github.com/Wei-Shaw/sub2api/blob/v0.2.18/backend/internal/repository/concurrency_cache.go)
- [公开面板 IP 限流](https://github.com/Wei-Shaw/sub2api/blob/v0.2.18/backend/internal/server/middleware/panel_rate_limit.go)

本次以下载到临时目录的对应 tag 源码检查实现，并通过本机二进制版本和提交号核对；没有以其他版本的行为替代当前版本。
