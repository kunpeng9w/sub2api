//go:build unit

package service

import(
 "context"
 "math"
 "testing"
 "time"

 "github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
 "github.com/stretchr/testify/require"
)
func TestQuotaEstimateFormulaAndInvalidInputs(t *testing.T){
 now:=time.Date(2026,11,5,12,0,0,0,time.UTC);start:=now.Add(-time.Hour);end:=now.Add(time.Hour)
 e:=EstimateQuota(12,40,start,end,now,now);require.Equal(t,30.0,*e.TotalCost);require.InDelta(t,18.0,*e.RemainingCost,1e-9)
 e=EstimateQuota(12,120,start,end,now,now);require.Equal(t,0.0,*e.RemainingCost)
 for _,values:=range [][2]float64{{0,40},{12,0},{12,-1},{math.NaN(),40},{12,math.Inf(1)}}{e:=EstimateQuota(values[0],values[1],start,end,now,now);require.Nil(t,e.TotalCost);require.NotEmpty(t,e.UnavailableReason)}
 for _,sample:=range []time.Time{{},start.Add(-time.Second),now.Add(time.Second),end}{require.Nil(t,EstimateQuota(12,40,start,end,sample,now).TotalCost)}
 require.Nil(t,EstimateQuota(12,40,start,now,now.Add(-time.Second),now).TotalCost)
}
type estimateWindowRepo struct{UsageLogRepository;queries []UsageWindowQuery;calls int}
func(r *estimateWindowRepo)GetUsageWindowStatsBatch(_ context.Context,q []UsageWindowQuery)(map[string]*usagestats.AccountStats,error){r.calls++;r.queries=q;out:=map[string]*usagestats.AccountStats{};for _,w:=range q{out[w.Key]=&usagestats.AccountStats{Cost:12}};return out,nil}
func TestQuotaEstimateAllWindowsAndSampleCutoff(t *testing.T){
 for _,platform:=range []string{PlatformAnthropic,PlatformOpenAI}{t.Run(platform,func(t *testing.T){
  sample:=time.Now().Add(-time.Minute);end5:=sample.Add(time.Hour);end7:=sample.Add(24*time.Hour)
  repo:=&estimateWindowRepo{};svc:=&AccountUsageService{usageLogRepo:repo,cache:NewUsageCache()};account:=&Account{ID:12,Platform:platform}
  usage:=&UsageInfo{UpdatedAt:&sample,FiveHour:&UsageProgress{Utilization:40,ResetsAt:&end5},SevenDay:&UsageProgress{Utilization:40,ResetsAt:&end7}}
  if platform==PlatformAnthropic{usage.SevenDaySonnet=&UsageProgress{Utilization:40,ResetsAt:&end7};usage.SevenDayFable=&UsageProgress{Utilization:40,ResetsAt:&end7}}
  result:=svc.attachQuotaEstimates(context.Background(),account,usage)
  require.Equal(t,30.0,*result.FiveHour.Estimate.TotalCost);require.Nil(t,usage.FiveHour.Estimate,"caller snapshot must not mutate")
  want:=2;if platform==PlatformAnthropic{want=4};require.Len(t,repo.queries,want)
  for _,q:=range repo.queries{require.Equal(t,sample,q.End);require.True(t,q.Start.Before(sample))}
  if platform==PlatformAnthropic{require.Equal(t,"sonnet",repo.queries[2].Family);require.Equal(t,"fable",repo.queries[3].Family)}
  svc.attachQuotaEstimates(context.Background(),account,usage);require.Equal(t,1,repo.calls,"cached local stats reused")
 })}
}
