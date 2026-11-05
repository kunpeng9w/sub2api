package repository

import (
 "context"
 "encoding/json"
 "testing"
 "time"

 "github.com/DATA-DOG/go-sqlmock"
 "github.com/Wei-Shaw/sub2api/internal/service"
 "github.com/stretchr/testify/require"
)
func TestUsageWindowStatsBatchUsesSampleCutoffAndUpstreamFamily(t *testing.T){
 db,mock,err:=sqlmock.New();require.NoError(t,err);defer db.Close()
 repo:=&usageLogRepository{sql:db};now:=time.Now().UTC()
 windows:=[]service.UsageWindowQuery{{Key:"7d-sonnet",AccountID:3,Start:now.Add(-24*time.Hour),End:now,Family:"sonnet"}}
 payload,err:=json.Marshal(windows);require.NoError(t,err)
 mock.ExpectQuery(`(?s)WITH windows.*u.created_at>=w.start_at AND u.created_at<=w.end_at.*COALESCE\(NULLIF\(TRIM\(u.upstream_model\),''\),u.model\).*GROUP BY w.key`).WithArgs(string(payload)).WillReturnRows(sqlmock.NewRows([]string{"key","requests","tokens","cost","standard_cost","user_cost"}).AddRow("7d-sonnet",2,100,12,10,20))
 result,err:=repo.GetUsageWindowStatsBatch(context.Background(),windows);require.NoError(t,err);require.Equal(t,12.0,result["7d-sonnet"].Cost);require.NoError(t,mock.ExpectationsWereMet())
}
