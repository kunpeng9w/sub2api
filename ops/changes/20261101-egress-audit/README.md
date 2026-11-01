# 请求出口日志补充

用户已于新加坡时间 2026-11-01 12:14:06 完成部署，基础与已观察到的 WebSocket 验收通过，当时发现的连接复用日志标记问题已在后续构建修正上线，见 [修正版本验收及使用说明](../20261101-ws-reuse-audit/POST-DEPLOY.md)。详见 [上线检查](POST-DEPLOY.md)。没有数据库迁移。手动部署与回滚操作保留在 [DEPLOY.md](DEPLOY.md)，部署前构建验收见 [validation.json](validation.json)。

每条网关上游请求日志记录 Sub2API 用户 ID、API Key ID（非密钥）、上游账号 ID/名称、代理 ID、IP 管理中的名称、代理地址、目标域名与请求关联标识。`proxy_name` 和 `proxy_endpoint_name` 均表示代理名称；走受管本机代理时为 `Mihomo`，没有应用层代理时为 `直连`。`proxy_endpoint` 单独保存去掉认证信息的协议、主机和端口。

例如（示意，IP 不是真实出口）：

```json
{"event":"upstream_egress_completed","request_id":"req-123","attempt_id":"attempt-456","user_id":1,"api_key_id":4,"account_id":7,"account_name":"模型账号A","proxy_id":2,"proxy_name":"美国住宅出口","proxy_endpoint_name":"美国住宅出口","proxy_endpoint":"socks5h://proxy.example:8022","target_host":"chatgpt.com","protocol":"http","exit_ip":"203.0.113.20","exit_ip_source":"probe","exit_ip_status":"fresh","exit_ip_checked_at":"2026-11-01T12:00:00+08:00","outcome":"completed"}
```

## 查看

发布并重启新程序后使用：

```bash
sudo journalctl -u sub2api -f -o cat | rg --line-buffered 'upstream_egress_'
sudo journalctl -u sub2api --since '30 minutes ago' -o cat | rg 'upstream_egress_'
```

- `upstream_egress_started`：一次上游调用开始，重试/换账号各有独立 `attempt_id`，同一入站调用用 `request_id` 关联。
- `upstream_egress_connection`：HTTP 连接的本地/对端地址及复用情况，通过 `attempt_id` 关联。对端可能只是代理，不能当作最终公网出口。自定义 TLS transport 是否触发标准 httptrace 取决于 transport 实现；没有回调时不伪造连接信息。
- `upstream_egress_response`：收到 HTTP 响应头及状态码。
- `upstream_egress_completed`：读到 EOF、读取失败或关闭响应体。提前关闭记 `body_closed`，不能等同于完整读取。HTTP 错误状态看此前同一 attempt 的 `status_code`。
- `upstream_egress_blocked`：HTTP/WS 连接池的账号出口校验拒绝，未发起上游调用，也不做 IP 探测。
- `upstream_egress_probe_completed`：异步出口探测完成，通过 `exit_ip_probe_id` 与请求行关联。即使请求先于探测完成也能找到随后结果。

WebSocket 连接池和 passthrough 在每次实际拨号前创建独立审计记录（`phase=dial`），包括连接池预建连接、超时、握手失败和重试；握手返回的状态码记录为 `upstream_egress_response`。拨号成功的 completed 仅表示连接阶段完成，后续请求轮次另有独立记录。取消终止事件 `response.cancelled` / `response.canceled` 会立即以 `canceled` 结束当前轮次。

WebSocket 连接池和 passthrough 都按轮次记录；复用同一连接不会省略后续轮次，`conn_id`/`turn_id` 用于关联。插件 HTTP 请求也记录相同的账号/代理信息。HTTP 请求和 SSE 共用传输入口；流式请求收到响应头时不提前标记结束。

## 公网出口 IP 的证据边界

`exit_ip_source=probe` 表示经该代理配置发出的独立 IP 查询所观察到的公网 IP，**不是模型服务确认的该条请求源 IP**。动态代理池、按目标分流、IPv4/IPv6、透明代理或 NAT 都可能造成差异；即使每条请求单独探测也无法保证完全相同。`proxy_endpoint` 的 IP 也不是最终公网出口的证据。

成功结果缓存 5 分钟，失败冷却 30 秒；每个进程最多缓存 256 条、并行探测最多 2 个、超时 12 秒。缓存键包含完整代理认证配置的 SHA256，区分同一主机上的不同代理会话，键和认证内容不写日志。请求链路只读取缓存，不等待网络探测。默认探测使用 HTTPS ipify，也沿用已有 `security.proxy_probe.urls` 自定义列表；探测失败不回退直连。探测仅发送 IP 查询，不发送模型 Token 或用户请求内容。

状态：`pending` 尚在探测；`fresh` 五分钟内观测；`stale` 已过期、正在更新或等待资源；`failed` 探测失败；`unknown` 无探测器/资源不足；`unavailable` 当前路径不支持探测或已被出口校验拒绝。未获得 IP 时保留空值，未探测时 `exit_ip_checked_at=null`。

当前 Mihomo 只放行 Claude 官方域名，通用 IP 查询不符合规则，因此该路径显示 `Mihomo` 和 `unavailable`，不会把本地代理地址、节点入口 IP 或服务器公网 IP 冒充最终出口。若需要此路径的观测，后续应明确提供受允许的查询端点或代理侧遥测；本次不放宽在线 Mihomo 规则。

覆盖网关的公共 HTTP 上游入口、OpenAI WS 连接池/多轮入口/passthrough、OAuth 插件 HTTP。它不是进程级抓包审计；另走专用客户端的 OAuth 刷新、后台独立 SDK、DNS/数据库通信及外部插件内部隐藏的重试不能自动宣称逐条覆盖。已有 HTTP 入站访问日志仍可关联；认证拒绝且未选上游账号的请求没有出口链路。

日志不记录代理认证、Authorization、查询参数、请求正文、响应正文和原始错误文本。名称按管理员已配置的原文记录；名称本身不应填入密码或 Token。

## 验证

完整包专项测试和嵌入前端的 Linux amd64 构建通过 GitHub Actions 执行，本机不运行完整编译。`Source and operations verification` 新增审计集成检查，执行隔离组件 race 检查和 service/repository/middleware 专项回归；常规 CI 另行执行完整单元、集成、前端和 lint 检查。

新增测试使用本机假上游、假代理和注入的假 IP 探测器；现有出口策略测试禁用真实 IP 探测，避免改变假代理计数或访问外网。覆盖名称快照、日志脱敏、流式响应结束语义、换账号的独立尝试标识、缓存/会话区分/并发限制/刷新/失败冷却、WS 同连接多轮和校验失败不探测。上线前仍需标准 CI 和构建，再验证真实流式/WS 业务与日志；源码完成不代表线上已生效。

本机已完成新增组件的 7 项隔离测试及 race 竞争检测，均通过。隔离运行复制原始 audit 源文件和原始测试，只对 Account/Proxy/HTTPUpstream/探测接口使用最小类型替身，因此验证日志组件行为，不代表 service/repository/middleware 集成编译通过。可复现：

```bash
bash ops/changes/20261101-egress-audit/test-isolated.sh
bash ops/changes/20261101-egress-audit/test-isolated.sh -race
```

早期完整包测试因已有 Ent 包编译在本机持续换页而主动停止，没有把中止视为通过。本次验收将完整编译移至 GitHub Actions；最终候选 `742271e18840fdde7fab5c120ee827b122da080c` 的 124 项出口/传输专项回归（含 9 项新审计测试）、7 项组件 race 检查和嵌入前端构建已通过。完整常规 CI 的最终状态和下载产物摘要以 [validation.json](validation.json) 为准。仓库检查与差异空白检查通过，线上程序未替换。

## 验收时补充的依赖修复

首次 Actions 安全检查发现既有间接依赖的两个高危告警。仅更新 pnpm overrides 和锁文件：`decode-uri-component` 从 0.4.1 升级至 0.5.1，`follow-redirects` 从 1.16.1 升级至 1.16.2；前端业务源码不变，不新增安全例外。修复版本依据 [decode-uri-component 公告](https://github.com/advisories/GHSA-qr28-fpjm-wq7f) 和 [follow-redirects 公告](https://github.com/advisories/GHSA-mrwp-f3q8-3g9p)。最终交付需重新构建嵌入前端的程序并通过 CI。
