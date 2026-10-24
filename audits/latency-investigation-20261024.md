# 首字延迟只读检查（2026-10-24）

结论：截图中的 46.337 秒属于已取得并复用 WS 连接后的等待，不支持归因于 WS 建连或连接池排队。更可能涉及上游模型推理、上下文处理或调度等待；现有日志不能进一步量化三者。

## 对应记录

数据库 usage_logs.id=663；记录时间 2026-10-24 22:24:47.766263（Asia/Singapore）。

- 模型：gpt-6.1-sol；请求与实际推理强度均为 high。
- 输入未缓存 token：969；缓存读取 token：166912；合计 167881。
- 输出 token：5574；首字：46337 ms；总耗时：89073 ms。
- account_id=2；request_type=3；openai_ws_mode=true；账号 WS 模式 ctx_pool。
- 面板 TPS 为 5574 / 89.073 = 62.58 tok/s，按总耗时计算，输出计数可能包含推理 token。不能视为可见文本生成速度，也不能将全部输出 token 除以首字后的时间来求正文速度。

## 源码与日志证据

核对官方 v0.2.18 源码：

- frontend/src/utils/latencyHealth.ts 的 formatUsageOutputRate：output_tokens * 1000 / duration_ms。
- backend/internal/service/openai_ws_forwarder_ingress.go 的 sendAndRelay：已取得 lease 后设置 turnStart，再写入请求；收到 isOpenAIWSTokenEvent 为真的事件时记录 firstTokenMs。因此该路径的首字时间不包含前面的连接获取等待。
- backend/internal/service/openai_ws_forwarder_support.go 的 isOpenAIWSTokenEvent：排除 response.created、response.in_progress、output_item.added/done；接受 delta 等输出事件，可能包括推理摘要或工具调用参数。首字并不保证等于第一段正文，也不是首个网络字节。
- 系统日志：oa_ws_2_59 于 22:05:12.881 建立；22:23:18.687 开始 turn=20，后续 turn=21 以前述截图响应为 previous_response_id，连接 ID 保持一致。匹配连接日志中未找到重连、恢复或失败记录。
- 截图请求对应时间窗内，account_id=2 的 ops_error_logs 无记录。
- 当前 Nginx 配置 proxy_buffering off；对于此 WS ingress 请求，HTTP/SSE 响应缓冲不是首字计时的解释。

## 同期样本

在 22:29 左右查询最近两小时、first_token_ms 非空的记录：

| 模型 / 推理强度 | WS | 样本数 | 首字 P50 | 首字 P95 |
|---|---|---:|---:|---:|
| gpt-5.6-luna / low | 是 | 52 | 1.942 s | 3.160 s |
| gpt-6.1-sol / high | 是 | 52 | 6.319 s | 40.542 s |
| gpt-6-luna / low | 是 | 3 | 1.971 s | 2.138 s |

这些是观察性样本，模型、输入长度和任务不同，不能当作 WS/HTTP 或 high/low 的受控对照。它们说明 WS 下也能有较短首字，不支持 WS 必然产生几十秒延迟。

## 限制与下一步

未保存该次响应的逐事件接收时间和 reasoning_tokens 明细，无法确定 46.337 秒中推理、上游排队、请求上传和网络传输各占多少。166.9K 缓存读取是大上下文的证据，但不能据此证明缓存处理导致了本次延迟。

如需进一步定位，应先记录请求写入完成、首个响应事件、首个 delta、首个正文 delta 和完成事件时间，仅保留事件类型、时间与 token 计数；再用同一模型、同一输入、相近时间做 HTTP/WS 与不同推理强度的重复对照。只读检查没有修改配置、重启服务或发起额外模型调用。

参考：
- https://github.com/Wei-Shaw/sub2api/blob/v0.2.18/frontend/src/utils/latencyHealth.ts
- https://github.com/Wei-Shaw/sub2api/blob/v0.2.18/backend/internal/service/openai_ws_forwarder_ingress.go
- https://github.com/Wei-Shaw/sub2api/blob/v0.2.18/backend/internal/service/openai_ws_forwarder_support.go
- https://developers.openai.com/api/docs/guides/reasoning
- https://developers.openai.com/api/docs/guides/websocket-mode
