package worker

import (
 "context"
 "errors"
 "fmt"
 "log/slog"
 "strings"
 "time"

 "github.com/nukewarrior/pikpak-bridge/internal/aria2"
 "github.com/nukewarrior/pikpak-bridge/internal/domain"
 "github.com/nukewarrior/pikpak-bridge/internal/pikpak"
)

type cancelStore interface {
 ListCancelWork(context.Context, time.Time, int) ([]domain.Task, error)
 GetTask(context.Context, string) (domain.Task, error)
 ListDownloads(context.Context, string) ([]domain.Download, error)
 SaveTask(context.Context, *domain.Task, string, string) error
 HasRetainedCache(context.Context, string, string) (bool, error)
 ListPendingHistoryDeletes(context.Context) ([]string,error)
 DeleteCompletedPendingHistory(context.Context,string) error
}

type CancelOptions struct {
 WorkerInterval time.Duration
 RetryInterval time.Duration
 Locks *TaskLocks
}

type Canceller struct {
 store cancelStore
 provider pikpak.Provider
 aria2 aria2.Backend
 options CancelOptions
 locks *TaskLocks
}

func NewCanceller(store cancelStore, provider pikpak.Provider, backend aria2.Backend, opts CancelOptions) *Canceller {
 if opts.WorkerInterval <= 0 { opts.WorkerInterval = 2*time.Second }
 if opts.RetryInterval <= 0 { opts.RetryInterval = 30*time.Second }
 if opts.Locks == nil { opts.Locks = NewTaskLocks() }
 return &Canceller{store: store, provider: provider, aria2: backend, options: opts, locks: opts.Locks}
}

func (w *Canceller) Run(ctx context.Context) {
 slog.Info("取消清理工作器已启动")
 defer slog.Info("取消清理工作器已停止")
 w.runOnce(ctx)
 ticker := time.NewTicker(w.options.WorkerInterval)
 defer ticker.Stop()
 for {
  select {
  case <-ctx.Done(): return
  case <-ticker.C: w.runOnce(ctx)
  }
 }
}

func (w *Canceller) runOnce(ctx context.Context) {
 if err := w.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
  slog.Error("取消清理循环失败", "error", err)
 }
}

func (w *Canceller) RunOnce(ctx context.Context) error {
 // Resume pending deletions after a crash between cancellation and DB cleanup.
 pending, err := w.store.ListPendingHistoryDeletes(ctx)
 if err!=nil {return err}
 for _,id:=range pending {
  if err:=w.store.DeleteCompletedPendingHistory(ctx,id);err!=nil {return err}
 }
 tasks, err := w.store.ListCancelWork(ctx, time.Now().UTC(), 100)
 if err != nil { return err }
 for _, candidate := range tasks {
  if err := ctx.Err(); err != nil { return err }
  unlock := w.locks.Lock(candidate.ID)
  task, err := w.store.GetTask(ctx, candidate.ID)
  if err == nil && task.Status == domain.TaskCancelling { err = w.cleanup(ctx, &task) }
  unlock()
  if err != nil { slog.Warn("任务取消清理待重试", "task_id", candidate.ID, "error", err) }
 }
 return nil
}

func (w *Canceller) cleanup(ctx context.Context, task *domain.Task) error {
 // Never announce cancellation success for an unconfirmed PikPak submission.
 if task.CancelPendingSubmission {
  return w.retry(ctx, task, errors.New("PikPak 提交结果未知：尚无远端任务 ID，不能确认取消完成；请核查远端残留任务"))
 }
 downloads, err := w.store.ListDownloads(ctx, task.ID)
 if err != nil { return w.retry(ctx, task, err) }
 // Stop aria2 before deleting the remote source.
 for _, download := range downloads {
  if download.Aria2GID == "" || download.Status == domain.DownloadComplete { continue }
  if err := w.stopAria2(ctx, download.Aria2InstanceID, download.Aria2GID); err != nil {
   return w.retry(ctx, task, fmt.Errorf("aria2 %s: %w", download.Aria2GID, err))
  }
 }
 if task.PikPakTaskID != "" {
  if task.PikPakAccountID == "" { return w.retry(ctx, task, errors.New("PikPak 任务 ID 存在但账号 ID 缺失")) }
  if err := w.provider.CancelOfflineTask(ctx, task.PikPakAccountID, task.PikPakTaskID); err != nil {
   return w.retry(ctx, task, fmt.Errorf("停止 PikPak 离线任务: %w", err))
  }
 }
 if task.PikPakRootFileID != "" {
  if task.PikPakAccountID == "" { return w.retry(ctx, task, errors.New("PikPak 根文件 ID 存在但账号 ID 缺失")) }
  retained, err := w.store.HasRetainedCache(ctx, task.PikPakAccountID, task.PikPakRootFileID)
  if err != nil { return w.retry(ctx, task, err) }
  if !retained {
   if err := w.provider.DeletePermanently(ctx, task.PikPakAccountID, task.PikPakRootFileID); err != nil {
    return w.retry(ctx, task, fmt.Errorf("删除 PikPak 任务根文件: %w", err))
   }
  }
 }
 now := time.Now().UTC()
 task.Status = domain.TaskCancelled
 task.CompletedAt = &now
 task.Error = ""
 task.RetryCount = 0
 task.NextAttemptAt = nil
 if err:=w.store.SaveTask(ctx, task, "task.cancelled", "远端清理完成，任务已取消");err!=nil {return err}
 return w.store.DeleteCompletedPendingHistory(ctx,task.ID)
}

func (w *Canceller) stopAria2(ctx context.Context, instanceID, gid string) error {
 status, err := w.aria2.TellStatus(ctx, instanceID, gid)
 if err != nil {
  if aria2NotFound(err) { return nil }
  return err
 }
 switch status.Status {
 case "complete", "error", "removed":
  return nil
 case "active", "waiting", "paused":
  if err := w.aria2.Remove(ctx, instanceID, gid); err != nil && !aria2NotFound(err) { return err }
  return nil
 default:
  return fmt.Errorf("aria2 GID %s 状态未知: %q", gid, status.Status)
 }
}

func aria2NotFound(err error) bool {
 msg := strings.ToLower(err.Error())
 if strings.Contains(msg, "http status") { return false }
 return strings.Contains(msg, "no such gid") || (strings.Contains(msg, "not found") && (strings.Contains(msg, "gid") || strings.Contains(msg, "download")))
}

func (w *Canceller) retry(ctx context.Context, task *domain.Task, cause error) error {
 same := task.Error == cause.Error()
 task.RetryCount++
 task.Error = cause.Error()
 delay := w.options.RetryInterval
 for i:=1; i<task.RetryCount && i<10; i++ {
  if delay >= 15*time.Minute { break }
  delay *= 2
 }
 if delay > 30*time.Minute { delay = 30*time.Minute }
 next := time.Now().UTC().Add(delay)
 task.NextAttemptAt = &next
 event := "task.cancel_retry"
 if task.CancelPendingSubmission { event = "task.cancel_blocked" }
 if same { event = "" }
 if err := w.store.SaveTask(ctx, task, event, task.Error); err != nil { return err }
 return cause
}
