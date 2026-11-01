# 请求出口审计使用说明

当前已部署 `5957d502d942d4cfebd9595e269beae5e397e45c`。该功能自动记录：继续使用原有客户端、API Key、账号和代理即可，不需要额外开启开关。通过服务器上的 systemd 日志查看。

## 1. 实时查看

SSH 登录服务器后执行，再在客户端正常发送请求：

```bash
sudo journalctl -u sub2api -n 0 -f -o cat | grep --line-buffered 'upstream_egress_'
```

`-n 0` 从新产生的日志开始显示，避免把旧版本历史记录混在一起。按 Ctrl+C 只停止查看，不会停止 Sub2API。

## 2. 查看历史和定位一次请求

查看最近 30 分钟：

```bash
sudo journalctl -u sub2api --since '30 minutes ago' --no-pager -o cat | grep 'upstream_egress_'
```

只看本次修正上线后的记录，时间使用新加坡时区：

```bash
sudo journalctl -u sub2api --since '2026-11-01 12:44:09 +08' --no-pager -o cat | grep 'upstream_egress_'
```

从一条日志复制 `request_id`，替换下面的占位文字，查看同一入站请求的上游尝试：

```bash
sudo journalctl -u sub2api --since '30 minutes ago' --no-pager -o cat | grep 'upstream_egress_' | grep -F -- '替换成日志中的request_id'
```

换账号或重试会产生不同的 `attempt_id`，仍可由 `request_id` 关联。要只追踪一次具体尝试，把最后的匹配文字换成对应 `attempt_id`。

按用户 ID 筛选，例如用户 123（请换成实际 ID）：

```bash
sudo journalctl -u sub2api --since '30 minutes ago' --no-pager -o cat | grep 'upstream_egress_' | grep -E '"user_id"[[:space:]]*:[[:space:]]*123([[:space:]]*[,}])'
```

## 3. 重点看哪些字段

| 字段 | 用途 |
| --- | --- |
| `user_id` / `api_key_id` | Sub2API 用户和 API Key 的数字 ID；`api_key_id` 不是密钥内容 |
| `account_id` / `account_name` | 本次实际使用的上游账号 |
| `proxy_id` / `proxy_name` | 应用层代理及 IP 管理中的名称 |
| `proxy_endpoint` | 去掉认证信息的代理协议、主机和端口，不能当作最终公网出口 |
| `target_host` / `protocol` | 上游目标和使用的 HTTP/WS 路径 |
| `request_id` / `attempt_id` | 入站请求及每次上游尝试的关联标识 |
| `conn_id` / `turn_id` | WebSocket 连接和请求轮次 |
| `connection_reused` | 当前轮次是否复用连接；新连接首轮 false，后续 true；从池中借到已有连接时首轮即可为 true |
| `outcome` / `status_code` | 传输结束原因和收到的 HTTP 响应状态 |
| `exit_ip` / `exit_ip_status` / `exit_ip_probe_id` | 独立出口探测的 IP、状态和探测关联标识 |

`proxy_name=直连` 表示没有显式应用层代理；底层仍可能有 TUN 或 NAT。连接池预建连接的 `phase=dial` 记录可能没有用户/API Key 字段，不应当成一轮用户模型请求。

## 4. 判断一轮请求是否结束

| 事件 | 含义 |
| --- | --- |
| `upstream_egress_started` | 开始一次尝试 |
| `upstream_egress_connection` | HTTP 本地/对端连接信息；对端可能只是代理 |
| `upstream_egress_response` | 收到 HTTP 响应头，查看 `status_code` |
| `upstream_egress_completed` | 当前传输或 WS 请求轮次结束 |
| `upstream_egress_blocked` | 出口校验拒绝，未发起上游调用 |
| `upstream_egress_probe_completed` | 异步出口探测完成，用 `exit_ip_probe_id` 关联 |

HTTP/SSE 收到 response 不代表响应体已读完；后续 completed 才表示传输结束。`outcome=completed` 表示正常读完/当前 WS 轮次成功结束；HTTP 业务是否成功还需结合之前的 `status_code` 和客户端结果。

`body_closed` 表示提前关闭响应体，不能等同于完整读完；`canceled` 常见于请求取消；`timeout`、`transport_error` 表示超时或传输错误。优先用同一 `attempt_id` 找前后记录，不要只看某一行。

`phase=dial` 的 completed 仅代表拨号成功，后面还应有请求轮次日志。同一 WS 连接连续请求时可看到同一 `conn_id`、递增的 `turn_id` 和独立的 `attempt_id`。连接换新后，即使 `turn_id` 大于 1，新连接首轮仍可为 false。

## 5. 如何理解出口 IP

- `fresh`：最近 5 分钟内的成功探测，后续请求可能使用缓存。
- `pending`：正在探测，稍后通过相同 `exit_ip_probe_id` 找 probe_completed。
- `stale`：旧观测已过期，正在更新或等待资源。
- `failed`：独立 IP 查询失败，失败冷却 30 秒；不等同于模型请求失败，也不会因此回退直连。
- `unknown`：没有可用探测结果或资源不足。
- `unavailable`：该路径不支持探测或被出口校验拒绝。

`exit_ip_source=probe` 是通过该代理配置发出的独立 IP 查询结果，不能证明某一条模型请求的实际公网源 IP。动态出口、按目标分流、IPv4/IPv6 和 NAT 都可能使两者不同。

现有 Mihomo 只放行 Claude 官方域名，因此通用 IP 查询不可用。该路径显示 `Mihomo` 和 `unavailable` 属于当前设计，不需要为了获取 IP 而放宽出口保护。

当前上线检查及验证边界见 [POST-DEPLOY.md](POST-DEPLOY.md)，完整审计范围见 [原功能说明](../20261101-egress-audit/README.md)。
