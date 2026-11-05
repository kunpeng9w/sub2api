package service

import (
 "context"
 "fmt"
 "net/http"
 "sort"
 "time"

 infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
 "github.com/Wei-Shaw/sub2api/internal/pkg/monitorrequest"
 "github.com/google/uuid"
)

var (
 ErrChannelMonitorInvalidVisibility = infraerrors.BadRequest("CHANNEL_MONITOR_INVALID_VISIBILITY", "select a valid visibility and at least one existing viewer for selected visibility")
 ErrChannelMonitorEgress = infraerrors.New(http.StatusServiceUnavailable, "CHANNEL_MONITOR_EGRESS_UNAVAILABLE", "monitor proxy binding is unavailable")
 ErrChannelMonitorBusy = infraerrors.New(http.StatusConflict, "CHANNEL_MONITOR_BUSY", "monitor is already running or execution capacity is full")
 ErrChannelMonitorCoordination = infraerrors.New(http.StatusServiceUnavailable, "CHANNEL_MONITOR_COORDINATION_UNAVAILABLE", "monitor execution lock is unavailable")
 ErrChannelMonitorCooldown = infraerrors.New(http.StatusTooManyRequests, "CHANNEL_MONITOR_COOLDOWN", "retry after 60 seconds")
)

func monitorVisibleTo(m *ChannelMonitor, userID int64) bool {
 if m == nil || !m.Enabled || userID <= 0 { return false }
 if m.Visibility == "all" { return true }
 if m.Visibility == "selected" { for _, id := range m.AllowedUserIDs { if id == userID { return true } } }
 return false
}
func (s *ChannelMonitorService) AuthorizeUser(ctx context.Context, id, userID int64) error {
 m, err := s.repo.GetByID(ctx,id)
 if err != nil { return err }
 if !monitorVisibleTo(m,userID) { return ErrChannelMonitorNotFound }
 return nil
}
func (s *ChannelMonitorService) validateAccess(ctx context.Context, m *ChannelMonitor) error {
 if m.Visibility == "" { m.Visibility = "admin_only" }
 if m.Visibility != "all" && m.Visibility != "selected" && m.Visibility != "admin_only" { return ErrChannelMonitorInvalidVisibility }
 if m.Visibility != "selected" { m.AllowedUserIDs = nil; return nil }
 ids := make([]int64,0,len(m.AllowedUserIDs)); seen := map[int64]bool{}
 for _, id := range m.AllowedUserIDs { if id <= 0 { return ErrChannelMonitorInvalidVisibility }; if !seen[id] { seen[id]=true; ids=append(ids,id) } }
 if len(ids)==0 { return ErrChannelMonitorInvalidVisibility }
 sort.Slice(ids,func(i,j int)bool{return ids[i]<ids[j]}); m.AllowedUserIDs=ids
 validator,ok := s.repo.(interface{ ValidateMonitorUsers(context.Context, []int64) error })
 if !ok { return ErrChannelMonitorInvalidVisibility }
 return validator.ValidateMonitorUsers(ctx,ids)
}

// SetExecutionDependencies is called before the runner starts. Missing dependencies fail closed.
func (s *ChannelMonitorService) SetExecutionDependencies(proxies ProxyRepository, locks LeaderLockCache) {
 s.proxies=proxies; s.runLocks=locks
}
func (s *ChannelMonitorService) resolveMonitorRoute(ctx context.Context, m *ChannelMonitor) (string,error) {
 if m.AccountID != nil {
  if s.quotaFetcher == nil { return "",ErrChannelMonitorEgress }
  account,err:=s.quotaFetcher.LoadAccount(ctx,*m.AccountID)
  if err!=nil || account==nil || account.Platform!=m.Provider || account.ProxyID==nil { return "",ErrChannelMonitorEgress }
  route,err:=ResolveAccountProxyFromRepository(ctx,s.proxies,account)
  if err!=nil || route=="" { return "",ErrChannelMonitorEgress }; return route,nil
 }
 if m.ProxyID==nil { return "",ErrChannelMonitorEgress }
 route,err:=ResolveProxyBinding(ctx,s.proxies,m.ProxyID)
 if err!=nil || route=="" { return "",ErrChannelMonitorEgress }; return route,nil
}
func (s *ChannelMonitorService) monitorContext(ctx context.Context,m *ChannelMonitor)(context.Context,error){
 route,err:=s.resolveMonitorRoute(ctx,m); if err!=nil{return ctx,err}
 accountID:=int64(0); if m.AccountID!=nil{accountID=*m.AccountID}
 return monitorrequest.With(ctx,monitorrequest.Policy{Route:route,AccountID:accountID,Validate:func(checkCtx context.Context)error{
  current,err:=s.repo.GetByID(checkCtx,m.ID)
  if err!=nil || !current.Enabled { return ErrChannelMonitorEgress }
  fresh,err:=s.resolveMonitorRoute(checkCtx,current)
  if err!=nil || fresh!=route { return ErrChannelMonitorEgress }; return nil
 }}),nil
}
func (s *ChannelMonitorService) RunManual(ctx context.Context,id,userID int64,admin bool)([]*CheckResult,error){
 if !admin { if err:=s.AuthorizeUser(ctx,id,userID);err!=nil{return nil,err} }
 return s.runCheck(ctx,id,userID)
}
func (s *ChannelMonitorService) acquireRun(ctx context.Context,id,userID int64)(func(),error){
 if s.runLocks==nil{return nil,ErrChannelMonitorCoordination}
 owner:=uuid.NewString(); key:=fmt.Sprintf("monitor:run:%d",id)
 ok,err:=s.runLocks.TryAcquireLeaderLock(ctx,key,owner,2*time.Minute)
 if err!=nil{return nil,ErrChannelMonitorCoordination};if !ok{return nil,ErrChannelMonitorBusy}
 release:=func(){ c,cancel:=context.WithTimeout(context.Background(),2*time.Second);defer cancel();_ = s.runLocks.ReleaseLeaderLock(c,key,owner) }
 select{case s.runSlots<-struct{}{}:default:release();return nil,ErrChannelMonitorBusy}
 cleanup:=func(){<-s.runSlots;release()}
 if userID>0{
  ok,err=s.runLocks.TryAcquireLeaderLock(ctx,fmt.Sprintf("monitor:cooldown:%d:%d",userID,id),owner,time.Minute)
  if err!=nil{cleanup();return nil,ErrChannelMonitorCoordination};if !ok{cleanup();return nil,ErrChannelMonitorCooldown}
 }
 return cleanup,nil
}
