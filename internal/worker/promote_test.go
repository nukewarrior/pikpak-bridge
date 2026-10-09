package worker

import (
 "context"
 "os"
 "path/filepath"
 "testing"

 "github.com/nukewarrior/pikpak-bridge/internal/domain"
)

func writeTestFile(t *testing.T, file, value string) {
 t.Helper()
 if err:=os.MkdirAll(filepath.Dir(file),0755);err!=nil {t.Fatal(err)}
 if err:=os.WriteFile(file,[]byte(value),0600);err!=nil {t.Fatal(err)}
}
func readTestFile(t *testing.T, file string) string {
 t.Helper()
 v,err:=os.ReadFile(file);if err!=nil {t.Fatal(err)};return string(v)
}
func TestPromoteLocalDownloadsPreservesOldUntilVerified(t *testing.T) {
 root:=t.TempDir();id:="repeat1";rel:="book/image.png"
 dest:=filepath.Join(root,rel)
 stage:=filepath.Join(root,".pikpak-bridge-staging",id,rel)
 writeTestFile(t,dest,"original")
 writeTestFile(t,stage,"new!")
 bad:=[]domain.Download{{RelativePath:rel,TotalLength:99}}
 if err:=PromoteLocalDownloads(root,id,bad);err==nil {t.Fatal("expected staged length mismatch")}
 if got:=readTestFile(t,dest);got!="original" {t.Fatalf("old file overwritten before verification: %q",got)}
 good:=[]domain.Download{{RelativePath:rel,TotalLength:4}}
 if err:=PromoteLocalDownloads(root,id,good);err!=nil {t.Fatal(err)}
 if got:=readTestFile(t,dest);got!="new!" {t.Fatalf("expected new file: %q",got)}
 if err:=PromoteLocalDownloads(root,id,good);err!=nil {t.Fatalf("promotion not idempotent: %v",err)}
}

func TestPromoteLocalDownloadsRecoversInterruptedMultiFileSwap(t *testing.T) {
 root:=t.TempDir();id:="repeat2"
 stageRoot:=filepath.Join(root,".pikpak-bridge-staging",id)
 backupRoot:=filepath.Join(root,".pikpak-bridge-backups",id)
 a,b:="comic/a.png","comic/b.png"
 writeTestFile(t,filepath.Join(root,a),"newa")
 writeTestFile(t,filepath.Join(backupRoot,a),"olda")
 writeTestFile(t,filepath.Join(stageRoot,b),"newb")
 writeTestFile(t,filepath.Join(root,b),"oldb")
 writeTestFile(t,filepath.Join(stageRoot,".bridge-promoting.json"),
  "[{\"relative\":\"comic/a.png\",\"had_old\":true},{\"relative\":\"comic/b.png\",\"had_old\":true}]")
 files:=[]domain.Download{{RelativePath:a,TotalLength:4},{RelativePath:b,TotalLength:4}}
 if err:=PromoteLocalDownloads(root,id,files);err!=nil {t.Fatal(err)}
 if got:=readTestFile(t,filepath.Join(root,a));got!="newa" {t.Fatalf("file a: %q",got)}
 if got:=readTestFile(t,filepath.Join(root,b));got!="newb" {t.Fatalf("file b: %q",got)}
}

func TestPromoteLocalDownloadsRefusesSymlinkAndMissingMount(t *testing.T) {
 root:=t.TempDir()
 if err:=PromoteLocalDownloads("", "id",nil);err==nil {t.Fatal("no mount must fail closed")}
 outside:=t.TempDir()
 if err:=os.Symlink(outside,filepath.Join(root,"comic"));err!=nil {t.Fatal(err)}
 stage:=filepath.Join(root,".pikpak-bridge-staging","id","comic","a.png")
 writeTestFile(t,stage,"new")
 if err:=PromoteLocalDownloads(root,"id",[]domain.Download{{RelativePath:"comic/a.png",TotalLength:3}});err==nil {
  t.Fatal("symlink target must fail closed")
 }
}

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
