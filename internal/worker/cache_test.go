package worker

import (
    "context"
    "testing"

    "github.com/nukewarrior/pikpak-bridge/internal/domain"
)

func TestCacheCleanerEvictsOnlyCompletedTask(t *testing.T) {
 db,task:=prepareVerifiedTask(t,10,10,10)
 defer db.Close()
 task.PikPakAccountID="account-a"
 task.PikPakRootFileID="first"
 if err:=db.SaveTask(context.Background(),&task,"","");err!=nil {t.Fatal(err)}
 if err:=db.RecordRetainedCache(context.Background(),&task,10);err!=nil {t.Fatal(err)}
 entry,err:=db.ClaimOldestCache(context.Background(),"account-a")
 if err!=nil {t.Fatal(err)}
 if entry!=nil {t.Fatal("active task cache was eligible for eviction")}
 task.Status=domain.TaskCompleted
 if err:=db.SaveTask(context.Background(),&task,"","");err!=nil {t.Fatal(err)}
 entry,err=db.ClaimOldestCache(context.Background(),"account-a")
 if err!=nil || entry==nil {t.Fatalf("expected reclaimable cache: %+v %v",entry,err)}
 if entry.RootFileID!="first" {t.Fatalf("wrong root %q",entry.RootFileID)}
 if err:=db.FinishCacheReclaim(context.Background(),*entry,nil);err!=nil {t.Fatal(err)}
 cache,err:=db.LatestRetainedCache(context.Background(),"url:test")
 if err!=nil || cache!=nil {t.Fatalf("reclaimed cache still reusable %+v %v",cache,err)}
}
