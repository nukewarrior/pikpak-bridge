package worker

import (
 "context"
 "errors"
 "strings"
 "sync"
 "testing"
 "time"

 "github.com/nukewarrior/pikpak-bridge/internal/aria2"
 "github.com/nukewarrior/pikpak-bridge/internal/domain"
 "github.com/nukewarrior/pikpak-bridge/internal/pikpak"
 "github.com/nukewarrior/pikpak-bridge/internal/store"
)

type blockingSubmitProvider struct {
 fakeProvider
 started chan struct{}
 release chan struct{}
 cancelled []string
 deleted []string
 mu sync.Mutex
}

func (p *blockingSubmitProvider) SubmitOffline(ctx context.Context, _, _ string) (pikpak.OfflineTask, error) {
 close(p.started)
 select {
 case <-p.release:
  return pikpak.OfflineTask{ID: "remote-after-cancel", Status: pikpak.PhaseRunning, RootFileID: "root-after-cancel"}, nil
 case <-ctx.Done():
  return pikpak.OfflineTask{}, ctx.Err()
 }
}

func (p *blockingSubmitProvider) CancelOfflineTask(_ context.Context, _, id string) error {
 p.mu.Lock(); defer p.mu.Unlock()
 p.cancelled = append(p.cancelled, id)
 return nil
}
func (p *blockingSubmitProvider) DeletePermanently(_ context.Context, _, id string) error {
 p.mu.Lock(); defer p.mu.Unlock()
 p.deleted = append(p.deleted, id)
 return nil
}

func createCancellationTask(t *testing.T, db *store.SQLite, id string) {
 t.Helper()
 now := time.Now().UTC()
 task := domain.Task{
  ID:id, Source:"https://example.invalid/"+id, SourceType:"https",
  SourceKey:"url:"+id, TargetID:"movies",TargetName:"电影",
  Aria2InstanceID:"a1", DownloadDir:"/downloads/movies",
  Status:domain.TaskQueued, CreatedAt:now,UpdatedAt:now,
 }
 if err := db.CreateTask(context.Background(), task); err != nil { t.Fatal(err) }
}

func TestCancellationWaitsForInFlightPikPakSubmission(t *testing.T) {
 db, err := store.Open(t.TempDir()+"/cancel.db")
 if err != nil { t.Fatal(err) }
 defer db.Close()
 createCancellationTask(t, db, "inflight")
 p := &blockingSubmitProvider{
  started:make(chan struct{}), release:make(chan struct{}),
  fakeProvider:fakeProvider{
   snapshot:pikpak.AccountSnapshot{ID:"pp1",Name:"pp1",Enabled:true,Healthy:true,QuotaRemaining:3,StorageFree:100000000000,MaxJobs:2},
  },
 }
 locks := NewTaskLocks()
 w := New(db,p,Options{AccountIDs:[]string{"pp1"},Locks:locks,StatusInterval:time.Millisecond})
 canceller := NewCanceller(db,p,&fakeAria2{},CancelOptions{Locks:locks,RetryInterval:time.Millisecond})
 done := make(chan error,1)
 go func(){ done<-w.RunOnce(context.Background()) }()
 select {
 case <-p.started:
 case <-time.After(5*time.Second): t.Fatal("submission did not start")
 }
 pending,err := db.RequestCancel(context.Background(),"inflight")
 if err != nil { t.Fatal(err) }
 if pending.Status != domain.TaskCancelling || !pending.CancelPendingSubmission {
  t.Fatalf("cancellation did not track in-flight submission: %#v",pending)
 }
 cancelDone := make(chan error,1)
 go func(){ cancelDone <- canceller.RunOnce(context.Background()) }()
 select {
 case <-cancelDone: t.Fatal("cancellation must wait for in-flight external operation")
 case <-time.After(20*time.Millisecond):
 }
 close(p.release)
 select {
 case <-done:
 case <-time.After(5*time.Second):t.Fatal("submit worker blocked")
 }
 select {
 case <-cancelDone:
 case <-time.After(5*time.Second):t.Fatal("cancel worker blocked")
 }
 got,err:=db.GetTask(context.Background(),"inflight")
 if err!=nil {t.Fatal(err)}
 if got.Status!=domain.TaskCancelled || got.PikPakTaskID!="remote-after-cancel" {
  t.Fatalf("remote task was lost or not cancelled: %#v",got)
 }
 if len(p.cancelled)!=1 || p.cancelled[0]!="remote-after-cancel" || len(p.deleted)!=1 || p.deleted[0]!="root-after-cancel" {
  t.Fatalf("exact remote resources not cancelled: task=%v root=%v",p.cancelled,p.deleted)
 }
}

type flakyCancellationProvider struct {
 fakeProvider
 fails int
 attempts int
 deleted int
}
func (p *flakyCancellationProvider) CancelOfflineTask(context.Context,string,string) error {
 p.attempts++
 if p.fails>0 {p.fails--;return errors.New("temporary PikPak outage")}
 return nil
}
func (p *flakyCancellationProvider) DeletePermanently(context.Context,string,string) error {
 p.deleted++
 return nil
}

func TestCancellationRetriesAfterStoreRestart(t *testing.T) {
 filename:=t.TempDir()+"/restart.db"
 db,err:=store.Open(filename)
 if err!=nil {t.Fatal(err)}
 createCancellationTask(t,db,"restart")
 task,err:=db.GetTask(context.Background(),"restart")
 if err!=nil {t.Fatal(err)}
 task.Status=domain.TaskPikPakRunning
 task.PikPakAccountID="pp1"
 task.PikPakTaskID="remote-1"
 task.PikPakRootFileID="root-1"
 if err:=db.SaveTask(context.Background(),&task,"","");err!=nil {t.Fatal(err)}
 if _,err:=db.RequestCancel(context.Background(),"restart");err!=nil {t.Fatal(err)}
 p:=&flakyCancellationProvider{fails:1}
 first:=NewCanceller(db,p,&fakeAria2{},CancelOptions{RetryInterval:time.Millisecond})
 if err:=first.RunOnce(context.Background());err!=nil {t.Fatal(err)}
 waiting,err:=db.GetTask(context.Background(),"restart")
 if err!=nil {t.Fatal(err)}
 if waiting.Status!=domain.TaskCancelling || waiting.RetryCount!=1 || waiting.NextAttemptAt==nil {
  t.Fatalf("cleanup failure was not persisted: %#v",waiting)
 }
 if err:=db.Close();err!=nil {t.Fatal(err)}
 db,err=store.Open(filename)
 if err!=nil {t.Fatal(err)}
 defer db.Close()
 time.Sleep(5*time.Millisecond)
 restarted:=NewCanceller(db,p,&fakeAria2{},CancelOptions{RetryInterval:time.Millisecond})
 if err:=restarted.RunOnce(context.Background());err!=nil {t.Fatal(err)}
 completed,err:=db.GetTask(context.Background(),"restart")
 if err!=nil {t.Fatal(err)}
 if completed.Status!=domain.TaskCancelled || completed.CompletedAt==nil || p.attempts!=2 || p.deleted!=1 {
  t.Fatalf("restart did not resume exact cleanup: %#v, attempts=%d, deleted=%d",completed,p.attempts,p.deleted)
 }
}

func TestCancellationWithUnknownSubmissionNeverPretendsSuccess(t *testing.T) {
 db,err:=store.Open(t.TempDir()+"/unknown.db")
 if err!=nil {t.Fatal(err)}
 defer db.Close()
 createCancellationTask(t,db,"unknown")
 task,err:=db.GetTask(context.Background(),"unknown")
 if err!=nil {t.Fatal(err)}
 task.Status=domain.TaskPikPakSubmitting
 task.PikPakAccountID="pp1"
 if err:=db.SaveTask(context.Background(),&task,"","");err!=nil {t.Fatal(err)}
 if _,err:=db.RequestCancel(context.Background(),"unknown");err!=nil {t.Fatal(err)}
 c:=NewCanceller(db,&fakeProvider{},&fakeAria2{},CancelOptions{RetryInterval:time.Millisecond})
 if err:=c.RunOnce(context.Background());err!=nil {t.Fatal(err)}
 pending,err:=db.GetTask(context.Background(),"unknown")
 if err!=nil {t.Fatal(err)}
 if pending.Status!=domain.TaskCancelling || !pending.CancelPendingSubmission || !strings.Contains(pending.Error,"未知") {
  t.Fatalf("unknown remote task was incorrectly considered cleaned: %#v",pending)
 }
 if err:=db.RecordPikPakSubmission(context.Background(),"unknown","pp1","now-known","root-known");err!=nil {t.Fatal(err)}
 time.Sleep(5*time.Millisecond)
 if err:=c.RunOnce(context.Background());err!=nil {t.Fatal(err)}
 got,err:=db.GetTask(context.Background(),"unknown")
 if err!=nil {t.Fatal(err)}
 if got.Status!=domain.TaskCancelled {t.Fatalf("known remote task not cleaned: %#v",got)}
}

func TestCancellationRetriesAria2Failure(t *testing.T) {
 db,err:=store.Open(t.TempDir()+"/aria.db")
 if err!=nil {t.Fatal(err)}
 defer db.Close()
 createCancellationTask(t,db,"aria")
 task,err:=db.GetTask(context.Background(),"aria")
 if err!=nil {t.Fatal(err)}
 task.Status=domain.TaskAria2Downloading
 if err:=db.SaveTask(context.Background(),&task,"","");err!=nil {t.Fatal(err)}
 if err:=db.ReplaceRemoteFiles(context.Background(),"aria",[]domain.RemoteFile{{TaskID:"aria",PikPakFileID:"file",Name:"movie.mkv",RelativePath:"movie.mkv",Size:123}});err!=nil {t.Fatal(err)}
 if err:=db.EnsureDownloads(context.Background(),"aria","a1",func(string,string)string{return "1234567890abcdef"});err!=nil {t.Fatal(err)}
 if _,err:=db.RequestCancel(context.Background(),"aria");err!=nil {t.Fatal(err)}
 backend:=&fakeAria2{tellStatusErr:errors.New("aria2 temporarily offline")}
 c:=NewCanceller(db,&fakeProvider{},backend,CancelOptions{RetryInterval:time.Millisecond})
 if err:=c.RunOnce(context.Background());err!=nil {t.Fatal(err)}
 waiting,_:=db.GetTask(context.Background(),"aria")
 if waiting.Status!=domain.TaskCancelling {t.Fatalf("wanted retry, got %s",waiting.Status)}
 backend.tellStatusErr=nil
 backend.statusByGID=map[string]aria2.Status{"1234567890abcdef":{GID:"1234567890abcdef",Status:"active"}}
 time.Sleep(5*time.Millisecond)
 if err:=c.RunOnce(context.Background());err!=nil {t.Fatal(err)}
 got,_:=db.GetTask(context.Background(),"aria")
 if got.Status!=domain.TaskCancelled {t.Fatalf("wanted cancelled, got %s: %s",got.Status,got.Error)}
 if _,still:=backend.statusByGID["1234567890abcdef"];still {t.Fatal("aria2 task was not removed")}
}
