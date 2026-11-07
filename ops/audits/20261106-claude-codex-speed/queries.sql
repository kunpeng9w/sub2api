-- 固定时间范围，Asia/Singapore；不返回身份、密钥或请求内容。
BEGIN READ ONLY;
SET LOCAL statement_timeout = '15s';
SET LOCAL TIME ZONE 'Asia/Singapore';
WITH valid AS (
  SELECT u.created_at, a.platform, u.model, u.output_tokens,
         u.duration_ms, u.first_token_ms,
         u.output_tokens * 1000.0 / u.duration_ms AS output_tps
  FROM usage_logs u JOIN accounts a ON a.id = u.account_id
  WHERE u.created_at >= '2026-11-05 19:00:00+08'
    AND u.created_at < '2026-11-06 23:50:00+08'
    AND u.output_tokens > 0 AND u.duration_ms > 0
    AND u.image_count = 0 AND u.image_output_tokens = 0
    AND COALESCE(u.billing_mode, '') <> 'image'
    AND u.request_type IN (1, 2, 3, 4)
)
SELECT date_bin('10 minutes', created_at, '2000-01-01 00:00:00+00') AS bucket,
       platform, count(*) AS n, avg(output_tps) AS avg_tps,
       count(*) FILTER (WHERE output_tps < 10) AS lt10_n,
       avg(first_token_ms) / 1000 AS ttft_avg_s,
       sum(output_tokens) FILTER (WHERE first_token_ms IS NOT NULL AND duration_ms > first_token_ms)
         * 1000.0 / NULLIF(sum(duration_ms - first_token_ms)
         FILTER (WHERE first_token_ms IS NOT NULL AND duration_ms > first_token_ms), 0)
         AS post_ttft_weighted_tps
FROM valid GROUP BY 1, 2 ORDER BY 1, 2;
COMMIT;
-- 网卡原始数据（只读系统历史）：
-- sadf -j /var/log/sysstat/sa05 -- -n DEV
-- sadf -j /var/log/sysstat/sa06 -- -n DEV
