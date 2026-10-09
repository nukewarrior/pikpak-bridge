package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nukewarrior/pikpak-bridge/internal/domain"
	"github.com/nukewarrior/pikpak-bridge/internal/pikpak"
)

type finalizeStore interface {
	ListFinalizeWork(ctx context.Context, now time.Time, limit int) ([]domain.Task, error)
	ListDownloads(ctx context.Context, taskID string) ([]domain.Download, error)
	SaveTask(ctx context.Context, task *domain.Task, eventType, message string) error
	GetTask(ctx context.Context, id string) (domain.Task, error)
	RecordRetainedCache(context.Context, *domain.Task, int64) error
}

type FinalizeOptions struct {
	WorkerInterval time.Duration
	RetryInterval  time.Duration
	MaxRetry       int
	VerifySize     bool
	CleanupEnabled bool
	CleanupDelay   time.Duration
	Locks          *TaskLocks
}

type Finalizer struct {
	store    finalizeStore
	provider pikpak.Provider
	options  FinalizeOptions
	locks *TaskLocks
}

func NewFinalizer(store finalizeStore, provider pikpak.Provider, options FinalizeOptions) *Finalizer {
	if options.WorkerInterval <= 0 {
		options.WorkerInterval = 2 * time.Second
	}
	if options.RetryInterval <= 0 {
		options.RetryInterval = 30 * time.Second
	}
	if options.MaxRetry <= 0 {
		options.MaxRetry = 10
	}
	if options.CleanupDelay < 0 {
		options.CleanupDelay = 0
	}
	if options.Locks == nil { options.Locks = NewTaskLocks() }
	return &Finalizer{store: store, provider: provider, options: options, locks: options.Locks}
}

func (w *Finalizer) Run(ctx context.Context) {
	slog.Info("收尾工作器已启动",
		"verify_size", w.options.VerifySize,
		"cleanup_enabled", w.options.CleanupEnabled,
		"cleanup_delay", w.options.CleanupDelay,
	)
	defer slog.Info("收尾工作器已停止")
	w.runOnceLogged(ctx)
	ticker := time.NewTicker(w.options.WorkerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.runOnceLogged(ctx)
		}
	}
}

func (w *Finalizer) runOnceLogged(ctx context.Context) {
	if err := w.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("收尾工作循环执行失败", "error", err)
	}
}

func (w *Finalizer) RunOnce(ctx context.Context) error {
	tasks, err := w.store.ListFinalizeWork(ctx, time.Now().UTC(), 100)
	if err != nil {
		return err
	}
	for i := range tasks {
		if err := ctx.Err(); err != nil {
			return err
		}
		unlock := w.locks.Lock(tasks[i].ID)
		current, loadErr := w.store.GetTask(ctx, tasks[i].ID)
		if loadErr == nil { loadErr = w.processTask(ctx, &current) }
		unlock()
		if loadErr != nil {
			slog.Warn("任务收尾处理失败", "task_id", tasks[i].ID, "error", loadErr)
		}
	}
	return nil
}

func (w *Finalizer) processTask(ctx context.Context, task *domain.Task) error {
	switch task.Status {
	case domain.TaskVerifying:
		return w.verify(ctx, task)
	case domain.TaskReadyToCleanup:
		task.Status = domain.TaskVerifying
		return w.store.SaveTask(ctx, task, "cleanup.policy_changed", "改为按空间管理 PikPak 缓存")
	case domain.TaskPikPakDeleting:
		return w.complete(ctx, task, "旧版清理中断，云端缓存状态未知")
	default:
		return nil
	}
}

func (w *Finalizer) verify(ctx context.Context, task *domain.Task) error {
	downloads, err := w.store.ListDownloads(ctx, task.ID)
	if err != nil {
		return w.failVerify(ctx, task, err)
	}
	if len(downloads) == 0 {
		return w.failVerify(ctx, task, errors.New("no aria2 downloads recorded"))
	}

	var total int64
	for _, download := range downloads {
		if download.Status != domain.DownloadComplete {
			task.Status = domain.TaskAria2Downloading
			task.Error = "download became non-complete during verification"
			task.NextAttemptAt = timePtr(time.Now().UTC().Add(w.options.RetryInterval))
			return w.store.SaveTask(ctx, task, "verify.download_incomplete", download.RelativePath)
		}
		if download.CompletedLength != download.TotalLength {
			return w.failVerify(ctx, task, fmt.Errorf(
				"length mismatch for %s: completed=%d total=%d",
				download.RelativePath,
				download.CompletedLength,
				download.TotalLength,
			))
		}
		if w.options.VerifySize && download.ExpectedSize != download.TotalLength {
			return w.failVerify(ctx, task, fmt.Errorf(
				"PikPak/aria2 size mismatch for %s: pikpak=%d aria2=%d",
				download.RelativePath,
				download.ExpectedSize,
				download.TotalLength,
			))
		}
		total += download.TotalLength
	}

	task.RetryCount = 0
	task.Error = ""
	if strings.Contains(task.SourceKey, "#") {
		if err := PromoteLocalDownloads(task.LocalDir, task.ID, downloads); err != nil {
			return w.failVerify(ctx, task, fmt.Errorf("安全替换 NAS 文件失败: %w", err))
		}
	}
	if err := w.store.RecordRetainedCache(ctx, task, total); err != nil {
		return w.failVerify(ctx, task, fmt.Errorf("保存 PikPak 云端缓存引用失败: %w", err))
	}
	slog.Info("任务校验完成，PikPak 云端文件已保留", "task_id", task.ID,
		"files", len(downloads), "bytes", total)
	return w.complete(ctx, task, fmt.Sprintf("verified %d files, %d bytes; cloud cache retained", len(downloads), total))
}

func (w *Finalizer) beginCleanup(ctx context.Context, task *domain.Task) error {
	if task.PikPakAccountID == "" || task.PikPakRootFileID == "" {
		task.Status = domain.TaskCleanupFailed
		task.Error = "refusing cleanup without exact PikPak account/root file ID"
		task.NextAttemptAt = nil
		return w.store.SaveTask(ctx, task, "cleanup.failed", task.Error)
	}
	slog.Info("开始清理 PikPak 文件",
		"task_id", task.ID,
		"account_id", task.PikPakAccountID,
		"root_file_id", task.PikPakRootFileID,
	)
	task.Status = domain.TaskPikPakDeleting
	task.Error = ""
	task.NextAttemptAt = nil
	return w.store.SaveTask(ctx, task, "cleanup.started", task.PikPakRootFileID)
}

func (w *Finalizer) cleanup(ctx context.Context, task *domain.Task) error {
	if err := w.provider.DeletePermanently(ctx, task.PikPakAccountID, task.PikPakRootFileID); err != nil {
		slog.Warn("PikPak 文件清理失败",
			"task_id", task.ID,
			"account_id", task.PikPakAccountID,
			"root_file_id", task.PikPakRootFileID,
			"error", err,
		)
		task.RetryCount++
		task.Error = err.Error()
		if task.RetryCount >= w.options.MaxRetry {
			task.Status = domain.TaskCleanupFailed
			task.NextAttemptAt = nil
			return w.store.SaveTask(ctx, task, "cleanup.failed", err.Error())
		}
		task.Status = domain.TaskPikPakDeleting
		task.NextAttemptAt = timePtr(time.Now().UTC().Add(w.options.RetryInterval))
		if saveErr := w.store.SaveTask(ctx, task, "cleanup.retry", err.Error()); saveErr != nil {
			return saveErr
		}
		return err
	}
	slog.Info("PikPak 文件清理完成",
		"task_id", task.ID,
		"account_id", task.PikPakAccountID,
		"root_file_id", task.PikPakRootFileID,
	)
	return w.complete(ctx, task, "verified downloads and permanently deleted recorded PikPak root")
}

func (w *Finalizer) complete(ctx context.Context, task *domain.Task, message string) error {
	now := time.Now().UTC()
	task.Status = domain.TaskCompleted
	task.RetryCount = 0
	task.Error = ""
	task.NextAttemptAt = nil
	task.CompletedAt = &now
	return w.store.SaveTask(ctx, task, "task.completed", message)
}

func (w *Finalizer) failVerify(ctx context.Context, task *domain.Task, cause error) error {
	task.Status = domain.TaskVerifyFailed
	task.Error = cause.Error()
	task.NextAttemptAt = nil
	return w.store.SaveTask(ctx, task, "verify.failed", cause.Error())
}
