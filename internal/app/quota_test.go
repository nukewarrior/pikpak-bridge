package app

import (
 "context"
 "errors"
 "sync"
 "testing"
 "time"

 "github.com/nukewarrior/pikpak-bridge/internal/config"
)

type fakeQuotaReader struct {
 mu sync.Mutex
 values map[string]int64
 errs map[string]error
 calls map[string]int
}

func (f *fakeQuotaReader) CloudDownloadQuota(_ context.Context, id string) (int64, int64, error) {
 f.mu.Lock()
 defer f.mu.Unlock()
 f.calls[id]++
 return f.values[id], 100, f.errs[id]
}

func TestQuotaMonitorSummarizesEnabledAccounts(t *testing.T) {
 disabled := false
 reader := &fakeQuotaReader{values:map[string]int64{"a":20,"b":30,"c":90},errs:make(map[string]error),calls:make(map[string]int)}
 m := NewQuotaMonitor(reader, []config.PikPakAccount{{ID:"a"},{ID:"b"},{ID:"c",Enabled:&disabled}})
 m.RequestRefresh()
 m.runPending(context.Background())
 m.wg.Wait()
 got := m.Summary()
 if got.Remaining!=50 || got.EnabledAccounts!=2 || got.CountedAccounts!=2 || !got.Complete {t.Fatalf("unexpected summary: %+v",got)}
 if reader.calls["c"]!=0 {t.Fatal("disabled account queried")}
 reader.mu.Lock()
 reader.errs["b"]=errors.New("login failed")
 reader.mu.Unlock()
 m.RequestRefresh()
 m.runPending(context.Background())
 m.wg.Wait()
 got=m.Summary()
 if got.Remaining!=20 || got.CountedAccounts!=1 || got.Complete || got.StaleRemaining!=30 || got.StaleAccounts!=1 {t.Fatalf("unexpected partial summary: %+v",got)}
}

func TestQuotaMonitorSubmittedRefreshesSelectedAccount(t *testing.T) {
 reader:=&fakeQuotaReader{values:map[string]int64{"a":10,"b":30},errs:make(map[string]error),calls:make(map[string]int)}
 m:=NewQuotaMonitor(reader,[]config.PikPakAccount{{ID:"a"},{ID:"b"}})
 m.RequestRefresh()
 m.runPending(context.Background())
 m.wg.Wait()
 reader.mu.Lock(); reader.values["a"]=9; reader.mu.Unlock()
 m.Submitted("a")
 m.mu.Lock(); m.pending["a"]=time.Now().Add(-time.Second); m.mu.Unlock()
 m.runPending(context.Background())
 m.wg.Wait()
 if s:=m.Summary();s.Remaining!=39 {t.Fatalf("unexpected after submit: %+v",s)}
 if reader.calls["a"]!=2 || reader.calls["b"]!=1 {t.Fatalf("unexpected calls: %+v",reader.calls)}
}
