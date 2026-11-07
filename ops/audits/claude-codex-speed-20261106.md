# Claude / Codex 同期速度与公网带宽排查（2026-11-06）

结论：存在同期慢请求；现有证据不支持 Codex 持续占满本机公网带宽导致 Claude 变慢。不能据此排除秒级突发、代理节点限速/拥塞、客户端链路问题或上游处理等待。

## 数据范围与口径

- 时区统一为 Asia/Singapore（UTC+8）。查询快照范围：2026-11-05 19:00 至 2026-11-06 23:50（右端不含）。Claude 实际记录覆盖 11-05 19:30 至 11-06 22:57，合计 363 次；其中 Opus 346 次、Sonnet 17 次。
- 来源：本机 PostgreSQL `usage_logs`、`ops_system_metrics`、`ops_error_logs`，`/var/log/sysstat/sa05` 与 `sa06`，Nginx access/error 日志。仅只读查询及轻量统计；未运行编译、测试、压测，未改线上配置。
- 平均 TPS 与管理页面源码口径一致：先计算每次 `output_tokens * 1000 / duration_ms`，再取算术平均；只纳入 output_tokens > 0、duration_ms > 0 的文本请求。本次 Claude 均为流式请求。Codex 指账号平台为 openai 的有效文本请求；不同模型的 TPS 不宜直接比较。
- 以请求完成时间分桶，10 分钟一个窗口。Claude 至少 3 条记录才纳入主要比较，降低小样本干扰。42 个有 Claude 的窗口中有 32 个满足要求（347 次 Claude 请求）；26 个低于 80 TPS，6 个不低于 80 TPS。其余 10 个低样本窗口保留在 CSV。
- 为给“很慢 Codex”一个可复核标准，本报告定义为单请求低于 10 TPS，不代表服务承诺或模型应有速度。
- `first_token_ms` 是程序保存的首事件等待指标；Claude 路径可能以第一条非空 SSE data 事件计时，不保证是第一个可见文字 token。下文的扣除等待后速率为 `sum(output_tokens) / sum((duration_ms-first_token_ms)/1000)`，属于首事件后整体速率估计，不等于纯模型解码速率。
- sysstat 约每 10 分钟记录一次平均流量，sadf 时间为 UTC，已转换到 +08。网卡采样区间与整十分桶存在数秒偏移，按重叠时长加权对齐；Mbps 按 `kB/s * 1024 * 8 / 1e6` 换算。下面分组流量是各窗口均值的算术平均。
- 用户提供服务器峰值公网带宽为 200 Mbps；未通过云控制台独立验证突发/共享/实际限速规则。eth0 为整机流量，不能直接归因到 Codex，也不能把入向与出向相加当成出向占用。

## 主要比较

| 指标 | Claude <80 TPS 的窗口 | Claude ≥80 TPS 的窗口 |
|---|---:|---:|
| 10 分钟窗口数 | 26 | 6 |
| Claude 样本数 | 206 | 141 |
| Claude 平均 TPS | 47.98 | 83.74 |
| Claude 平均首事件等待 | 11.30 秒 | 1.65 秒 |
| Claude 扣除等待后整体速率 | 103.95 TPS | 111.13 TPS |
| 同期 Codex 样本数 | 391 | 252 |
| Codex 平均 TPS | 16.62 | 19.68 |
| Codex <10 TPS 的请求 | 155（39.6%） | 57（22.6%） |
| Codex 平均首事件等待 | 34.76 秒 | 7.19 秒 |
| 网卡平均接收流量 | 0.66 Mbps | 3.01 Mbps |
| 网卡平均发送流量 | 0.66 Mbps | 2.81 Mbps |
| 发送流量 / 200 Mbps | 0.33% | 1.41% |

26 个 Claude 慢窗口都有 Codex 请求，其中 25 个出现至少一个 <10 TPS 的 Codex 请求。正常组 6 个窗口中也有 4 个出现 <10 TPS 的 Codex，因此仅凭“同一窗口有慢 Codex”不足以确认共同原因；更有意义的是慢请求比例和首事件等待的上升。

按单请求估算执行区间（完成时间减 duration_ms），229 个 <80 TPS 的 Claude 请求中，157 个与至少一次有效 Codex 请求重叠，114 个与 <10 TPS 的 Codex 重叠。134 个 ≥80 TPS 的 Claude 请求中，对应为 104 个、31 个。这里的重叠只说明请求处理区间重叠，不证明当时在大量传输字节。

## 小时级例子

所有时间为 2026-11-06，区间长度 1 小时。

| 起始时间 | Claude 次数 | Claude 平均 TPS | Claude 首事件等待秒 | Codex 次数 | Codex 平均 TPS | Codex <10 TPS 次数 | 整机平均出向 Mbps |
|---|---:|---:|---:|---:|---:|---:|---:|
| 14:00 | 87 | 81.07 | 1.7 | 292 | 18.9 | 73 | 2.9 |
| 15:00 | 35 | 47.41 | 16.73 | 91 | 6.55 | 66 | 0.97 |
| 19:00 | 54 | 48.95 | 9.8 | 45 | 5.12 | 38 | 0.74 |
| 20:00 | 30 | 37.37 | 16.45 | 111 | 21.8 | 24 | 0.67 |
| 21:00 | 36 | 35.43 | 14.6 | 92 | 20.67 | 25 | 0.48 |
| 22:00 | 21 | 32.58 | 14.04 | 149 | 19.01 | 45 | 0.4 |

更直接的反例：14:40–14:50，Claude 14 次、平均 85.45 TPS，同时 Codex 66 次，其中 26 次 <10 TPS；对齐后的整机平均出向 7.52 Mbps（实际 sysstat 14:40:12–14:50:04 样本为 7.58 Mbps），Claude 仍正常。15:40–15:50，出向仅 1.04 Mbps，Claude 却降到 42.99 TPS，8 次 Codex 全部 <10 TPS。持续出向占用更高并没有对应更慢 Claude。

查询期间最高 10 分钟平均出向为 8.71 Mbps（11-06 16:10–16:20，约为 200 Mbps 的 4.36%），该区间没有 Claude 请求，不能用来评价当时 Claude 速度。平均流量不能界定秒级峰值。

## 更值得追查的线索

1. 首事件前等待解释了大量 TPS 下降。限定为同一个 Claude Opus 模型后，慢窗口 198 次、47.70 TPS、首事件等待 11.47 秒、扣除等待后 103.48 TPS；正常窗口 137 次、84.04 TPS、1.54 秒、110.25 TPS。模型混用不能解释这组变化，但上下文、输出长度、上游负载等仍是混杂因素。15 点 Claude 平均上下文（input + cache_read + cache_creation）约 25.5 万 token，14 点约 15.1 万，也可能影响首事件等待。
2. Codex 也存在明显模型差异：慢窗口 gpt-6.1-terra 83 次、4.26 TPS、首事件等待 80.64 秒；正常窗口 114 次、17.77 TPS、7.05 秒。gpt-6.1-sol 分别 20.21 与 21.32 TPS，变化小得多。两组 Codex 总均值差异部分受模型构成影响，不能将所有 Codex 低 TPS 一律视为网络故障。
3. 15:00–16:00 的 ops_error_logs 有 12 条 upstream_error 和 1 条 api_error。其中 4 条涉及 HTTP/2 connection lost，7 条涉及 HTTP/2 PROTOCOL_ERROR，1 条 TLS handshake timeout；api_error 为上游响应流中断。它们支持存在上游连接异常，但不能定位是本机到代理、代理到上游，还是上游服务本身，也不能证明 Claude 的等待由同一原因导致。
4. 当前账号配置显示 Claude 与一组 Codex 都使用 proxy_id=1；另有 Codex 使用 proxy_id=2/4，这三个代理配置的 host 不同。慢窗口 proxy_id=1 的 Codex 平均 9.37 TPS，proxy_id=2/4 为 18.43/18.90 TPS。代理绑定取自当前 accounts，不是每次请求保存的历史出口，不能当作历史路径证明。15 点 proxy_id=2/4 的 Codex 也明显变慢，不能简单归因于单个节点。
5. 慢小时 CPU 平均约 4–5%，分钟采样并发等待队列最大值 0。没有整机持续 CPU 或排队饱和证据；这不排除采样间的短时停顿。当前 tc qdisc 累计 dropped/overlimits 为 0，但不是历史云侧限速证据。

## 限制与后续取证

- Claude 数据只跨约两天，正常窗口只有 6 个；时段、上下文、模型、输出长度未随机控制，结论属于观察性证据。
- usage_logs 主要代表记入用量的请求，不能完整覆盖失败和未完成请求。快照时正在执行或尚未写入数据库的请求未纳入。
- Nginx 可见响应字节，但 WebSocket 日志在连接关闭时才写入，一条连接可能包含多轮调用；不能用完成时刻的 body_bytes_sent 当作那一秒的流量。本报告没有将这些字节伪装成逐请求瞬时带宽。
- 缺少历史秒级网卡吞吐、云侧丢包/限速、TCP 重传、逐代理流量、请求上传字节与分段耗时；无法彻底排除短突发或代理限速。
- 若继续定位，优先对齐云监控的出入带宽/丢包与应用请求时间，并按代理记录连接、TLS、请求上传、首响应及输出阶段耗时。当前证据不足以支持仅因本问题升级公网带宽；先定位首响应前的耗时更有价值。

## 可复核查询

下面查询只返回聚合信息；建议在只读事务和 statement_timeout 下执行。

```sql
BEGIN READ ONLY;
SET LOCAL statement_timeout = '15s';
SET LOCAL TIME ZONE 'Asia/Singapore';
WITH r AS (
  SELECT date_bin('10 minutes', u.created_at, timestamptz '2000-01-01 00:00+08') AS bucket,
         a.platform, u.output_tokens * 1000.0 / u.duration_ms AS tps,
         u.output_tokens, u.duration_ms, u.first_token_ms
  FROM usage_logs u JOIN accounts a ON a.id = u.account_id
  WHERE u.created_at >= timestamptz '2026-11-05 19:00+08'
    AND u.created_at < timestamptz '2026-11-06 23:50+08'
    AND u.output_tokens > 0 AND u.duration_ms > 0
    AND u.model <> 'gpt-image-2'
)
SELECT bucket, platform, count(*) AS samples,
       round(avg(tps), 2) AS avg_tps,
       count(*) FILTER (WHERE tps < 10) AS lt10,
       round(avg(first_token_ms) / 1000, 2) AS avg_first_event_s
FROM r GROUP BY bucket, platform ORDER BY bucket, platform;
ROLLBACK;
```

网卡原始数据可用 `sadf -j /var/log/sysstat/sa05 -- -n DEV` 和 `sa06` 同命令读取，注意 timestamp.utc=1 和采样 interval。逐窗口聚合证据见 [CSV](claude-codex-speed-20261106-windows.csv)。CSV 不包含用户身份、IP、令牌、请求正文或代理凭据。
