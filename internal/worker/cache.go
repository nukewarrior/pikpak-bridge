package worker

import (
    "context"
    "log/slog"
    "sync"
    "time"

    "github.com/nukewarrior/pikpak-bridge/internal/pikpak"
    "github.com/nukewarrior/pikpak-bridge/internal/store"
)

// CachePressure requests an eviction after PikPak reports a storage error
// even though the usual free-space threshold may not have been reached.
type CachePressure struct {
    mu sync.Mutex
    accounts map[string]bool
}
func NewCachePressure() *CachePressure { return &CachePressure{accounts:map[string]bool{}} }
func (p *CachePressure) Mark(id string) {
    if p==nil || id=="" {return}
    p.mu.Lock();p.accounts[id]=true;p.mu.Unlock()
}
func (p *CachePressure) Needed(id string) bool {
    if p==nil {return false}
    p.mu.Lock();defer p.mu.Unlock();return p.accounts[id]
}
func (p *CachePressure) Clear(id string) {
    if p==nil {return}
    p.mu.Lock();delete(p.accounts,id);p.mu.Unlock()
}

type cacheStore interface {
    ClaimOldestCache(context.Context,string) (*store.CacheEntry,error)
    FinishCacheReclaim(context.Context,store.CacheEntry,error) error
    HasPendingCacheReclaim(context.Context,string) (bool,error)
}
type CacheCleaner struct {
    store cacheStore
    provider pikpak.Provider
    accounts []string
    minFree int64
    pressure *CachePressure
}
func NewCacheCleaner(db cacheStore, p pikpak.Provider, accounts []string, minFree int64, pressure *CachePressure) *CacheCleaner {
    return &CacheCleaner{store:db,provider:p,accounts:accounts,minFree:minFree,pressure:pressure}
}
func (w *CacheCleaner) Run(ctx context.Context) {
    slog.Info("PikPak 空间自动回收器已启动", "min_free_space", w.minFree)
    ticker:=time.NewTicker(30*time.Second)
    defer ticker.Stop()
    w.RunOnce(ctx)
    for {
        select {
        case <-ctx.Done(): return
        case <-ticker.C: w.RunOnce(ctx)
        }
    }
}
func (w *CacheCleaner) RunOnce(ctx context.Context) {
    for _,id:=range w.accounts {
        if ctx.Err()!=nil {return}
        snapshot,err:=w.provider.RefreshAccount(ctx,id)
        if err!=nil || !snapshot.Enabled || !snapshot.Healthy {continue}
        pending,err:=w.store.HasPendingCacheReclaim(ctx,id)
        if err!=nil {slog.Warn("读取待清理缓存失败","account_id",id,"error",err);continue}
        pressure:=w.pressure.Needed(id)
        if snapshot.StorageFree>=w.minFree && !pressure && !pending {continue}
        for count:=0;count<50;count++ {
            if ctx.Err()!=nil {return}
            entry,err:=w.store.ClaimOldestCache(ctx,id)
            if err!=nil {slog.Warn("选择云端缓存失败","account_id",id,"error",err);break}
            if entry==nil {break}
            err=w.provider.DeletePermanently(ctx,id,entry.RootFileID)
            if saveErr:=w.store.FinishCacheReclaim(ctx,*entry,err);saveErr!=nil {
                slog.Error("持久化云端缓存删除结果失败","account_id",id,"root_file_id",entry.RootFileID,"error",saveErr)
                break
            }
            if err!=nil {
                slog.Warn("空间回收失败，稍后重试","account_id",id,"root_file_id",entry.RootFileID,"error",err)
                break
            }
            slog.Info("PikPak 空间回收：已删除最旧的已校验缓存",
                "account_id",id,"root_file_id",entry.RootFileID,"source_key",entry.SourceKey,
                "cached_at",entry.CachedAt)
            w.pressure.Clear(id)
            snapshot,err=w.provider.RefreshAccount(ctx,id)
            if err!=nil {break}
            if snapshot.StorageFree>=w.minFree {break}
        }
    }
}
