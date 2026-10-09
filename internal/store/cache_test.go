package store

import (
 "context"
 "database/sql"
 "errors"
 "path/filepath"
 "testing"
 "time"

 "github.com/nukewarrior/pikpak-bridge/internal/domain"
)
func cacheTestTask(id,source string) domain.Task {
 now:=time.Now().UTC()
 return domain.Task{
 ID:id,Source:source,SourceType:"magnet",SourceKey:"btih:"+source,
 Name:"book",TargetID:"target",TargetName:"NAS",
 Aria2InstanceID:"aria2",DownloadDir:"/downloads",Status:domain.TaskQueued,
 CreatedAt:now,UpdatedAt:now}
}
func TestRepeatSourceConflictAndCacheReuse(t *testing.T) {
 ctx:=context.Background()
 db,err:=Open(filepath.Join(t.TempDir(),"state.db"));if err!=nil {t.Fatal(err)}
 defer db.Close()
 original:=cacheTestTask("original","AAA")
 if existing,err:=db.CreateTaskFromSource(ctx,&original,false);err!=nil || existing!=nil {t.Fatalf("create: %+v %v",existing,err)}
 same:=cacheTestTask("second","AAA")
 if existing,err:=db.CreateTaskFromSource(ctx,&same,true);err!=nil || existing==nil {
  t.Fatalf("running task must block forced repeat: %+v %v",existing,err)
 }
 original.Status=domain.TaskCompleted
 original.PikPakAccountID="account1";original.PikPakRootFileID="root"
 if err:=db.SaveTask(ctx,&original,"","");err!=nil {t.Fatal(err)}
 if err:=db.RecordRetainedCache(ctx,&original,100);err!=nil {t.Fatal(err)}
 withoutForce:=cacheTestTask("third","AAA")
 if existing,err:=db.CreateTaskFromSource(ctx,&withoutForce,false);err!=nil || existing==nil {t.Fatal("must request confirmation")}
 withForce:=cacheTestTask("fourth","AAA")
 if existing,err:=db.CreateTaskFromSource(ctx,&withForce,true);err!=nil || existing!=nil {t.Fatalf("repeat: %+v %v",existing,err)}
 if withForce.Status!=domain.TaskPikPakComplete || withForce.PikPakRootFileID!="root" {
  t.Fatalf("retained cache not reused: %+v",withForce)
 }
 if withForce.SourceKey==original.SourceKey {t.Fatal("repeat must use a distinct row key")}
 // A retained cache cannot be reclaimed while the repeat transfer is active.
 claimed,err:=db.ClaimOldestCache(ctx,"account1")
 if err!=nil || claimed!=nil {t.Fatalf("active cache reclaimed: %+v %v",claimed,err)}
}
func TestDeleteHistoryRetainsCacheAndReleasesSource(t *testing.T) {
 ctx:=context.Background()
 db,err:=Open(filepath.Join(t.TempDir(),"state.db"));if err!=nil {t.Fatal(err)}
 defer db.Close()
 first:=cacheTestTask("first","BBBB")
 if err:=db.CreateTask(ctx,first);err!=nil {t.Fatal(err)}
 first.Status=domain.TaskCompleted
 first.PikPakAccountID="account1";first.PikPakRootFileID="root1"
 if err:=db.SaveTask(ctx,&first,"","");err!=nil {t.Fatal(err)}
 if err:=db.RecordRetainedCache(ctx,&first,100);err!=nil {t.Fatal(err)}
 if err:=db.DeleteHistoryTask(ctx,first.ID);err!=nil {t.Fatal(err)}
 if _,err:=db.GetTask(ctx,first.ID);!errors.Is(err,sql.ErrNoRows){t.Fatalf("not deleted: %v",err)}
 cache,err:=db.LatestRetainedCache(ctx,first.SourceKey)
 if err!=nil || cache==nil {t.Fatalf("cache lost when history deleted: %+v %v",cache,err)}
}
func TestFailedHistoryDeletionSchedulesRecoverableCleanup(t *testing.T) {
 ctx:=context.Background()
 db,err:=Open(filepath.Join(t.TempDir(),"state.db"));if err!=nil {t.Fatal(err)}
 defer db.Close()
 task:=cacheTestTask("failed","CCCC")
 if err:=db.CreateTask(ctx,task);err!=nil {t.Fatal(err)}
 task.Status=domain.TaskAria2Failed
 task.PikPakAccountID="acc";task.PikPakRootFileID="root"
 if err:=db.SaveTask(ctx,&task,"","");err!=nil {t.Fatal(err)}
 if err:=db.DeleteHistoryTask(ctx,task.ID);!errors.Is(err,ErrCleanupScheduled){t.Fatalf("want pending cleanup: %v",err)}
 latest,err:=db.GetTask(ctx,task.ID)
 if err!=nil || latest.Status!=domain.TaskCancelling {t.Fatalf("state: %+v %v",latest,err)}
 latest.Status=domain.TaskCancelled
 if err:=db.SaveTask(ctx,&latest,"","");err!=nil {t.Fatal(err)}
 pending,err:=db.ListPendingHistoryDeletes(ctx)
 if err!=nil || len(pending)!=1 {t.Fatalf("missing durable deletion: %v %v",pending,err)}
 if err:=db.DeleteCompletedPendingHistory(ctx,task.ID);err!=nil {t.Fatal(err)}
 if _,err:=db.GetTask(ctx,task.ID);!errors.Is(err,sql.ErrNoRows){t.Fatalf("failed to purge: %v",err)}
}

func TestHistoryDeletedStillRequiresConfirmationAndReusesCache(t *testing.T) {
    ctx:=context.Background()
    db,err:=Open(filepath.Join(t.TempDir(),"state.db"))
    if err!=nil {t.Fatal(err)}
    defer db.Close()
    old:=cacheTestTask("old","DDDD")
    if err:=db.CreateTask(ctx,old);err!=nil {t.Fatal(err)}
    old.Status=domain.TaskCompleted
    old.PikPakAccountID="acc1"; old.PikPakRootFileID="cached-root"
    if err:=db.SaveTask(ctx,&old,"","");err!=nil {t.Fatal(err)}
    if err:=db.RecordRetainedCache(ctx,&old,800);err!=nil {t.Fatal(err)}
    if err:=db.DeleteHistoryTask(ctx,old.ID);err!=nil {t.Fatal(err)}

    unconfirmed:=cacheTestTask("unconfirmed","DDDD")
    oldTask,err:=db.CreateTaskFromSource(ctx,&unconfirmed,false)
    if err!=nil || oldTask==nil || oldTask.Status!=domain.TaskCompleted {
        t.Fatalf("expected download warning from cache tombstone: %+v %v",oldTask,err)
    }

    again:=cacheTestTask("again","DDDD")
    duplicate,err:=db.CreateTaskFromSource(ctx,&again,true)
    if err!=nil || duplicate!=nil {t.Fatalf("force repeat: %+v %v",duplicate,err)}
    if again.Status!=domain.TaskPikPakComplete || again.PikPakRootFileID!="cached-root" {
        t.Fatalf("expected cached root: %+v",again)
    }
    if again.SourceKey=="btih:DDDD" {t.Fatal("repeat without history must stage safely")}
    got,err:=db.GetTask(ctx,again.ID)
    if err!=nil || got.DownloadDir!="/downloads" {t.Fatalf("download dir changed: %+v %v",got,err)}
}
