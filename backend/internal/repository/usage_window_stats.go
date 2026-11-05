package repository

import (
 "context"
 "encoding/json"

 "github.com/Wei-Shaw/sub2api/internal/service"
 "github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

// GetUsageWindowStatsBatch performs one local query for all quota windows.
func(r *usageLogRepository)GetUsageWindowStatsBatch(ctx context.Context,windows []service.UsageWindowQuery)(map[string]*usagestats.AccountStats,error){
 out:=map[string]*usagestats.AccountStats{};if len(windows)==0{return out,nil}
 payload,err:=json.Marshal(windows);if err!=nil{return nil,err}
 rows,err:=r.sql.QueryContext(ctx,`
 WITH windows AS (SELECT * FROM jsonb_to_recordset($1::jsonb)
 AS w(key text, account_id bigint, start_at timestamptz, end_at timestamptz, family text))
 SELECT w.key,COUNT(u.id),COALESCE(SUM(u.input_tokens+u.output_tokens+u.cache_creation_tokens+u.cache_read_tokens),0),
 COALESCE(SUM(COALESCE(u.account_stats_cost,u.total_cost)*COALESCE(u.account_rate_multiplier,1)),0),
 COALESCE(SUM(u.total_cost),0),COALESCE(SUM(u.actual_cost),0)
 FROM windows w LEFT JOIN usage_logs u ON u.account_id=w.account_id
 AND u.created_at>=w.start_at AND u.created_at<=w.end_at
 AND (w.family='' OR LOWER(COALESCE(NULLIF(TRIM(u.upstream_model),''),u.model)) LIKE '%' || w.family || '%')
 GROUP BY w.key`,string(payload))
 if err!=nil{return nil,err};defer rows.Close()
 for rows.Next(){var key string;st:=&usagestats.AccountStats{};if err:=rows.Scan(&key,&st.Requests,&st.Tokens,&st.Cost,&st.StandardCost,&st.UserCost);err!=nil{return nil,err};out[key]=st}
 return out,rows.Err()
}
