# 出口审计网络分段计时

本次扩展既有出口审计，帮助区分获取连接、发送请求、等待响应和读取输出阶段。复用原请求，不新增主动网络探测，不修改账号代理策略。既有出口 IP 探测行为保持原样，监控请求仍禁止附加出口探测。没有数据库迁移或新管理页面，也没有部署、重启或改动线上日志配置。

## 使用方法

以 `request_id` 关联 `usage_logs` 与出口事件，再按 `attempt_id` 分离重试。HTTP 的 `upstream_egress_response` 表示拿到响应头；`upstream_egress_completed` 表示 EOF、读取出错或关闭响应体。WebSocket 拨号事件带 `phase=dial`，与业务每轮的 `conn_id` / `turn_id` 分开看；不要把拨号的 HTTP Upgrade 写入字节当作每轮模型请求大小。

新指标添加到既有 response/completed 事件，使用 `network_metrics_version=1`，不按 token/read 额外输出日志。客户端不支持或未触发相应 trace 时省略字段，缺失不代表耗时为 0。时间单位为毫秒，支持小数；`*_ms` 时间点相对于该 attempt 开始，而非客户端最初发起请求的时间。

| 字段 | 含义与边界 |
|---|---|
| `connection_acquire_ms` | HTTP 最近一次 GetConn→GotConn，包含等待连接及可能的建连/代理握手/TLS，不等于纯连接池排队 |
| `dns/connect/tls/proxy_tunnel/fingerprint_tls_attempts`、`*_completed`、`*_failures`、`*_total_ms` | trace 观测到的各阶段次数与已完成操作耗时之和；并发拨号按地址分别配对。并行/嵌套阶段不能直接相加得到总耗时 |
| `*_pending`、`*_pending_total_ms` | 采样/完成时仍没有对应 Done 的阶段及已等待时长；晚到的回调不会修改完成事件 |
| `connection_reused`、`connection_was_idle`、`connection_idle_ms` | HTTP 连接取得时的复用、空闲状态和空闲时长；复用连接不伪造 DNS/TLS=0 |
| `pool_queue_wait_ms`、`pool_connection_pick_ms` | WebSocket 租约取得时的既有指标，每轮重复附带同一租约数据，不能跨轮累加 |
| `request_content_length` | HTTP 原请求声明的 ContentLength，未知/流式时可能为 0 或 -1，不替代实际读到的字节数 |
| `request_body_bytes`、`request_body_bytes_kind` | HTTP=`http_body_read`：传输层从 Body/GetBody 读取的应用层字节，内部重试累计。WS=`websocket_payload_attempted`：尝试写入的编码后消息字节；池化 JSON 包括 Encoder 换行，透传帧保持原内容。失败发送不能证明这些字节全部送达 |
| `request_write_attempts`、`request_write_failures`、`request_write_total_ms` | HTTP WroteHeaders→WroteRequest 的已观测耗时总和；WS 业务写入调用耗时，包含序列化和可能的本机写锁等待。均不是对端确认接收耗时；HTTP 100-continue 等待也可能包含其中 |
| `request_write_pending`、`request_write_pending_ms` | 完成/响应事件发出时写入仍在进行，不能将这类样本当作完整发送耗时 |
| `request_written_ms` | 最近一次成功写入完成时间点。失败不会伪造成功写完 |
| `first_response_byte_ms` | HTTP trace 首响应字节，可能是 1xx 响应，不是首个可见 token |
| `response_headers_ms` | HTTP Do/RoundTrip 返回响应头的观测时间点 |
| `first_response_body_read_ms` | HTTP 首次读到非空响应体；WS 每轮读到首条非空消息。可能是元数据事件，不能替代 usage 中的 TTFT |
| `wait_after_request_written_ms` | HTTP 首响应字节减最近成功写完时间；WS 首条消息减写完时间。若响应先到、写入后结束，省略而不输出负数 |
| `response_body_bytes`、`response_read_chunks` | 调用方已消费的 HTTP 应用层响应字节/read 块数，或 WS 每轮消息字节/消息数。可能已解压，不是 IP/TLS/WS 帧字节；早关流只反映已读部分 |
| `response_body_observed_ms`、`response_read_max_gap_ms` | 首次至最近一次读到数据的跨度、相邻非空读取最大间隔。包括调用方处理和下游背压，不能单独归因上游断流；不包含首读前等待及末次读取后的静默 |
| `last_network_error_class`、`last_network_error_phase` | 最近观测到的固定分类，不记录错误原文；连接竞速或内部重试恢复后成功的请求也可能保留此项，是否最终失败看 outcome |

Claude 指纹连接另记 `fingerprint_tls_*`；指纹 SOCKS 路径另记 `proxy_tunnel_*`，该段包含代理 TCP 连接与 SOCKS 隧道协商，不能理解成纯 SOCKS 握手。标准 httptrace 不一定能单独观测 SOCKS 握手；其他自定义 transport 未触发回调时不伪造数据。HTTP 内部重试的阶段总和与最近时间点属于同一 attempt，不应把它们解释成单次写入的完整瀑布图。各子阶段回调自身失败会给出具体分类，只有整体失败时按最后可见进度作粗粒度归类，不据此断言根因。

池化 WebSocket 在现有编码阶段计数字节，避免对大上下文二次序列化；两条池化业务路径在物理消息读取后计数，拼接 JSON 拆分不会重复计算。透传 WS 延续现有单 active turn 审计模型，要求逐轮 response.create/terminal 交替；不将 session.update/control frames 当作独立模型请求。

## 排查顺序

1. 同一请求检查 `outcome` 和最后错误分类，再看连接阶段是否失败/耗时高；按真实代理 ID 对比，不能用当前账号代理绑定替代历史路由。
2. 看请求字节数与 `request_write_total_ms` 是否同时上升。对比同模型、相近输入大小、同协议和连接复用状态，避免把序列化、100-continue 或池等待误作带宽不足。
3. 写入快而 `wait_after_request_written_ms` 高，说明本机写入之后等待较长，仍无法区分代理后段、上游接收/排队、上下文处理与推理。结合 usage TTFT 检查真正的可见输出等待。
4. 读取间隔增大时，对齐系统/云侧秒级流量、RTT、重传。应用审计不提供 TCP 重传、对端 ACK 或代理内部队列。

客户端→Nginx 的上传与 Nginx 请求缓冲不在本次出口计时范围。没有改动用户已有未提交的 Nginx 配置，也没有添加 request_length 等入口日志字段。

## 验证与部署

所有测试、race 检查、Lint、编译和构建由 GitHub Actions 执行。本机仅源码编辑、格式整理和 git diff 轻量检查。复用 `ops-ci.yml` 的 egress-audit（组件 race + 完整服务/仓库集成）和 embedded-build，以及 `backend-ci.yml`。组件隔离脚本已纳入新源文件与回归测试；真实生产编码器/租约回归由完整服务测试执行，不由组件桩测试替代。

远程验证结果见 [VALIDATION.md](VALIDATION.md)。部署后才会出现新指标，旧请求不能补回分段数据。部署属于后续操作，源码推送及 CI 不会替换运行程序。
