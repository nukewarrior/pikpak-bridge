package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nukewarrior/pikpak-bridge/internal/aria2"
	"github.com/nukewarrior/pikpak-bridge/internal/domain"
	"github.com/nukewarrior/pikpak-bridge/internal/pikpak"
)

type aria2Store interface {
	ListAria2Work(ctx context.Context, now time.Time, limit int) ([]domain.Task, error)
	SaveTask(ctx context.Context, task *domain.Task, eventType, message string) error
	GetTask(ctx context.Context, id string) (domain.Task, error)
	EnsureDownloads(ctx context.Context, taskID, instanceID string, gidFor func(string, string) string) error
	ListDownloads(ctx context.Context, taskID string) ([]domain.Download, error)
	SaveDownload(ctx context.Context, download *domain.Download) error
	AddTaskEvent(ctx context.Context, taskID, eventType, message string) error
}

type Aria2Options struct {
	WorkerInterval time.Duration
	StatusInterval time.Duration
	RetryInterval  time.Duration
	MaxRetry       int
	Locks          *TaskLocks
}

type Aria2Worker struct {
	store   aria2Store
	pikpak  pikpak.Provider
	backend aria2.Backend
	options Aria2Options
	locks *TaskLocks
}

func NewAria2(store aria2Store, provider pikpak.Provider, backend aria2.Backend, options Aria2Options) *Aria2Worker {
	if options.WorkerInterval <= 0 {
		options.WorkerInterval = 2 * time.Second
	}
	if options.StatusInterval <= 0 {
		options.StatusInterval = 5 * time.Second
	}
	if options.RetryInterval <= 0 {
		options.RetryInterval = 30 * time.Second
	}
	if options.MaxRetry <= 0 {
		options.MaxRetry = 10
	}
	if options.Locks == nil { options.Locks = NewTaskLocks() }
	return &Aria2Worker{store: store, pikpak: provider, backend: backend, options: options, locks: options.Locks}
}

func (w *Aria2Worker) Run(ctx context.Context) {
	slog.Info("aria2 工作者已启动",
		"worker_interval", w.options.WorkerInterval,
		"status_interval", w.options.StatusInterval,
		"retry_interval", w.options.RetryInterval,
	)
	defer slog.Info("aria2 工作者已停止")
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

func (w *Aria2Worker) runOnceLogged(ctx context.Context) {
	if err := w.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("aria2 工作循环执行失败", "error", err)
	}
}

func (w *Aria2Worker) RunOnce(ctx context.Context) error {
	tasks, err := w.store.ListAria2Work(ctx, time.Now().UTC(), 100)
	if err != nil {
		return err
	}
	for i := range tasks {
		if err := ctx.Err(); err != nil {
			return err
		}
		unlock := w.locks.Lock(tasks[i].ID)
		current, loadErr := w.store.GetTask(ctx, tasks[i].ID)
		if loadErr == nil && (current.Status == domain.TaskWaitingAria2 || current.Status == domain.TaskAria2Downloading) { loadErr = w.processTask(ctx, &current) }
		unlock()
		if loadErr != nil {
			slog.Warn("aria2 任务处理失败", "task_id", tasks[i].ID, "error", loadErr)
		}
	}
	return nil
}

func (w *Aria2Worker) processTask(ctx context.Context, task *domain.Task) error {
	if task.Aria2InstanceID == "" || task.DownloadDir == "" || task.TargetID == "" {
		return w.failTask(ctx, task, errors.New("task is missing its download target snapshot"))
	}

	if task.Status == domain.TaskWaitingAria2 {
		ready, err := w.targetReady(ctx, task)
		if err != nil || !ready {
			return err
		}
		gidFor := func(taskID, fileID string) string {
			return deterministicGIDForAttempt(taskID, fileID, task.ManualRetryCount)
		}
		if err := w.store.EnsureDownloads(ctx, task.ID, task.Aria2InstanceID, gidFor); err != nil {
			return w.retryTask(ctx, task, err)
		}
		slog.Info("开始向 aria2 分发任务",
			"task_id", task.ID,
			"target_id", task.TargetID,
			"aria2_instance_id", task.Aria2InstanceID,
			"download_dir", task.DownloadDir,
		)
		task.Status = domain.TaskAria2Downloading
		task.RetryCount = 0
		task.Error = ""
		task.NextAttemptAt = nil
		if err := w.store.SaveTask(ctx, task, "aria2.dispatch_started", task.TargetID); err != nil {
			return err
		}
	}

	return w.processDownloads(ctx, task)
}

func (w *Aria2Worker) targetReady(ctx context.Context, task *domain.Task) (bool, error) {
	snapshot, err := w.backend.Snapshot(ctx, task.Aria2InstanceID)
	if err != nil {
		return false, w.waitForTarget(ctx, task, err.Error())
	}
	if !snapshot.Enabled {
		return false, w.waitForTarget(ctx, task, "selected aria2 instance is disabled")
	}
	if !snapshot.Healthy {
		return false, w.waitForTarget(ctx, task, "selected aria2 instance is unavailable")
	}
	return true, nil
}

func (w *Aria2Worker) waitForTarget(ctx context.Context, task *domain.Task, reason string) error {
	slog.Warn("aria2 下载目标暂不可用",
		"task_id", task.ID,
		"target_id", task.TargetID,
		"aria2_instance_id", task.Aria2InstanceID,
		"download_dir", task.DownloadDir,
		"reason", reason,
		"retry_in", w.options.RetryInterval,
	)
	task.Status = domain.TaskWaitingAria2
	task.Error = reason
	task.NextAttemptAt = timePtr(time.Now().UTC().Add(w.options.RetryInterval))
	return w.store.SaveTask(ctx, task, "aria2.waiting_target", reason)
}

func (w *Aria2Worker) processDownloads(ctx context.Context, task *domain.Task) error {
	downloads, err := w.store.ListDownloads(ctx, task.ID)
	if err != nil {
		return w.retryTask(ctx, task, err)
	}
	if len(downloads) == 0 {
		return w.failTask(ctx, task, errors.New("no downloadable files persisted"))
	}

	now := time.Now().UTC()
	if batch, ok := w.backend.(aria2.BatchBackend); ok && len(downloads) > 1 {
		w.processDownloadsBatched(ctx, task, downloads, batch, now)
	} else {
		for i := range downloads {
			download := &downloads[i]
			if download.Status == domain.DownloadComplete {
				continue
			}
			if download.NextAttemptAt != nil && now.Before(*download.NextAttemptAt) {
				continue
			}
			switch download.Status {
			case domain.DownloadPending:
				if err := w.submitDownload(ctx, task, download); err != nil {
					slog.Warn("aria2 文件提交失败", "task_id", task.ID, "file_id", download.PikPakFileID, "error", err)
				}
			case domain.DownloadError:
				if downloadRetriesExhausted(download, w.options.MaxRetry) {
					continue
				}
				if err := w.submitDownload(ctx, task, download); err != nil {
					slog.Warn("aria2 文件提交失败", "task_id", task.ID, "file_id", download.PikPakFileID, "error", err)
				}
			default:
				if err := w.pollDownload(ctx, download); err != nil {
					slog.Warn("aria2 文件状态查询失败", "task_id", task.ID, "file_id", download.PikPakFileID, "error", err)
				}
			}
		}
		}

	downloads, err = w.store.ListDownloads(ctx, task.ID)
	if err != nil {
		return w.retryTask(ctx, task, err)
	}
	allComplete := len(downloads) > 0
	failed := false
	for _, download := range downloads {
		if download.Status != domain.DownloadComplete {
			allComplete = false
		}
		if download.Status == domain.DownloadError && downloadRetriesExhausted(&download, w.options.MaxRetry) {
			failed = true
		}
	}
	if failed {
		return w.failTask(ctx, task, errors.New("一个或多个 aria2 下载已耗尽重试次数"))
	}
	if allComplete {
		slog.Info("aria2 下载全部完成",
			"task_id", task.ID,
			"aria2_instance_id", task.Aria2InstanceID,
			"files", len(downloads),
		)
		task.Status = domain.TaskVerifying
		task.RetryCount = 0
		task.Error = ""
		task.NextAttemptAt = nil
		return w.store.SaveTask(ctx, task, "aria2.all_complete", fmt.Sprintf("%d files complete", len(downloads)))
	}

	task.Status = domain.TaskAria2Downloading
	task.NextAttemptAt = timePtr(time.Now().UTC().Add(w.options.StatusInterval))
	task.Error = ""
	return w.store.SaveTask(ctx, task, "", "")
}

// Keep the number of outstanding URLs bounded: PikPak signed links may expire
// while jobs wait in aria2's queue. Batch sizes also bound RPC response memory.
const (
	maxOutstandingDownloads = 32
	maxBatchStatusCalls = 50
	pikpakLinkConcurrency = 4
)

// Batched processing preserves each download's GID and DB record. If a batch
// RPC fails, fall back to the existing single-file recovery behavior.
func (w *Aria2Worker) processDownloadsBatched(ctx context.Context, task *domain.Task, downloads []domain.Download, batch aria2.BatchBackend, now time.Time) {
	// Reconcile existing aria2 jobs first, freeing slots as files complete.
	var polling []*domain.Download
	for i := range downloads {
		d := &downloads[i]
		if d.Status == domain.DownloadComplete || d.Status == domain.DownloadPending || d.Status == domain.DownloadError {
			continue
		}
		if d.NextAttemptAt == nil || !now.Before(*d.NextAttemptAt) {
			polling = append(polling, d)
		}
	}
	for start := 0; start < len(polling); start += maxBatchStatusCalls {
		end := start + maxBatchStatusCalls
		if end > len(polling) { end = len(polling) }
		part := polling[start:end]
		gids := make([]string, len(part))
		for i, d := range part { gids[i] = d.Aria2GID }
		results, err := batch.TellStatusBatch(ctx, task.Aria2InstanceID, gids)
		if err != nil || len(results) != len(part) {
			for _, d := range part {
				if e := w.pollDownload(ctx, d); e != nil { slog.Warn("aria2 状态查询失败", "task_id", task.ID, "file_id", d.PikPakFileID, "error", e) }
			}
			continue
		}
		for i, d := range part {
			var e error
			if results[i].Err != nil {
				// Transport succeeded but this GID failed: retain existing
				// EOF retry and transient polling semantics.
				e = w.pollDownload(ctx, d)
			} else if results[i].Status.Status == "error" {
				e = w.handleAria2Error(ctx, d, results[i].Status)
			} else {
				e = w.applyStatus(ctx, d, results[i].Status)
			}
			if e != nil { slog.Warn("aria2 状态处理失败", "task_id", task.ID, "file_id", d.PikPakFileID, "error", e) }
		}
	}
	// Refresh DB state after polling; terminal jobs no longer occupy slots.
	current, err := w.store.ListDownloads(ctx, task.ID)
	if err != nil {
		slog.Warn("aria2 批量分发刷新记录失败", "task_id", task.ID, "error", err)
		return
	}
	outstanding := 0
	for _, d := range current {
		switch d.Status {
		case domain.DownloadComplete, domain.DownloadPending, domain.DownloadError:
		default:
			outstanding++
		}
	}
	slots := maxOutstandingDownloads - outstanding
	if slots <= 0 { return }

	candidates := make([]*domain.Download, 0, slots)
	for i := range current {
		d := &current[i]
		if d.Status != domain.DownloadPending && d.Status != domain.DownloadError { continue }
		if d.Status == domain.DownloadError && downloadRetriesExhausted(d, w.options.MaxRetry) { continue }
		if d.NextAttemptAt != nil && now.Before(*d.NextAttemptAt) { continue }
		candidates = append(candidates, d)
		if len(candidates) == slots { break }
	}
	if len(candidates) == 0 { return }

	// Durable GID reconciliation is mandatory before any addUri: a process
	// can crash after aria2 accepts a job but before its DB state is saved.
	gids := make([]string, len(candidates))
	for i, d := range candidates { gids[i] = d.Aria2GID }
	existing, err := batch.TellStatusBatch(ctx, task.Aria2InstanceID, gids)
	if err != nil || len(existing) != len(candidates) {
		for _, d := range candidates {
			if e := w.submitDownload(ctx, task, d); e != nil {
				slog.Warn("aria2 文件回退提交失败", "task_id", task.ID, "file_id", d.PikPakFileID, "error", e)
			}
		}
		return
	}
	pending := make([]*domain.Download, 0, len(candidates))
	for i, d := range candidates {
		if existing[i].Err == nil {
			if e := w.applyStatus(ctx, d, existing[i].Status); e != nil {
				slog.Warn("aria2 已有 GID 状态恢复失败", "task_id", task.ID, "file_id", d.PikPakFileID, "error", e)
			}
		} else {
			pending = append(pending, d)
		}
	}
	if len(pending) == 0 { return }

	type preparedURL struct {
		url string
		err error
	}
	resolved := make([]preparedURL, len(pending))
	sem := make(chan struct{}, pikpakLinkConcurrency)
	var wg sync.WaitGroup
	started := time.Now()
	for i, d := range pending {
		wg.Add(1)
		go func(i int, fileID string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func(){ <-sem }()
			case <-ctx.Done():
				resolved[i].err = ctx.Err()
				return
			}
			resolved[i].url, resolved[i].err = w.pikpak.GetDownloadURL(ctx, task.PikPakAccountID, fileID)
		}(i, d.PikPakFileID)
	}
	wg.Wait()
	urlElapsed := time.Since(started)

	requests := make([]aria2.AddRequest, 0, len(pending))
	indexes := make([]int, 0, len(pending))
	for i, d := range pending {
		if resolved[i].err != nil {
			if e := w.retryDownload(ctx, d, resolved[i].err); e != nil { slog.Warn("PikPak 文件链接获取失败", "task_id", task.ID, "file_id", d.PikPakFileID, "error", e) }
			continue
		}
		requests = append(requests, aria2.AddRequest{
			URI: resolved[i].url, GID: d.Aria2GID, RelativePath: d.RelativePath,
			Overwrite: strings.Contains(task.SourceKey, "#"),
		})
		indexes = append(indexes, i)
	}
	if len(requests) == 0 { return }
	addStarted := time.Now()
	added, err := batch.AddBatch(ctx, task.Aria2InstanceID, task.DownloadDir, requests)
	addElapsed := time.Since(addStarted)
	if err != nil || len(added) != len(requests) {
		// A failed HTTP response can still mean aria2 accepted the batch.
		// The original single-file path checks each GID before resubmitting.
		for _, index := range indexes {
			d := pending[index]
			if e := w.submitDownload(ctx, task, d); e != nil { slog.Warn("aria2 单文件回退提交失败", "task_id", task.ID, "file_id", d.PikPakFileID, "error", e) }
		}
		return
	}
	for i, result := range added {
		d := pending[indexes[i]]
		if result.Err != nil || result.GID != d.Aria2GID {
			if status, statusErr := w.backend.TellStatus(ctx, d.Aria2InstanceID, d.Aria2GID); statusErr == nil {
				_ = w.applyStatus(ctx, d, status)
			} else {
				cause := result.Err
				if cause == nil { cause = fmt.Errorf("aria2 returned unexpected gid %q, want %q", result.GID, d.Aria2GID) }
				if e := w.retryDownload(ctx, d, cause); e != nil { slog.Warn("aria2 批量文件提交失败", "task_id", task.ID, "file_id", d.PikPakFileID, "error", e) }
			}
			continue
		}
		if e := w.markSubmitted(ctx, task, d, result.GID); e != nil {
			slog.Warn("aria2 批量文件保存失败", "task_id", task.ID, "file_id", d.PikPakFileID, "error", e)
		}
	}
	slog.Debug("aria2 批量提交完成", "task_id", task.ID, "files", len(requests),
		"url_ms", urlElapsed.Milliseconds(), "rpc_ms", addElapsed.Milliseconds())
}

func (w *Aria2Worker) submitDownload(ctx context.Context, task *domain.Task, download *domain.Download) error {
	if status, err := w.backend.TellStatus(ctx, download.Aria2InstanceID, download.Aria2GID); err == nil {
		return w.applyStatus(ctx, download, status)
	}

	uri, err := w.pikpak.GetDownloadURL(ctx, task.PikPakAccountID, download.PikPakFileID)
	if err != nil {
		return w.retryDownload(ctx, download, err)
	}
    overwrite := strings.Contains(task.SourceKey, "#")
    gid, err := w.backend.Add(
        ctx, download.Aria2InstanceID, task.DownloadDir, uri,
        download.Aria2GID, download.RelativePath, overwrite,
    )
	if err != nil {
		if status, checkErr := w.backend.TellStatus(ctx, download.Aria2InstanceID, download.Aria2GID); checkErr == nil {
			return w.applyStatus(ctx, download, status)
		}
		return w.retryDownload(ctx, download, err)
	}
	if gid != download.Aria2GID {
		return w.retryDownload(ctx, download, fmt.Errorf("aria2 returned unexpected gid %q, want %q", gid, download.Aria2GID))
	}
	return w.markSubmitted(ctx, task, download, gid)
}

func (w *Aria2Worker) markSubmitted(ctx context.Context, task *domain.Task, download *domain.Download, gid string) error {
	slog.Info("aria2 下载已提交",
		"task_id", task.ID,
		"aria2_instance_id", download.Aria2InstanceID,
		"gid", gid,
		"path", download.RelativePath,
		"overwrite", strings.Contains(task.SourceKey, "#"),
	)
	download.Status = domain.DownloadSubmitted
	download.RetryCount = 0
	download.LastError = ""
	download.NextAttemptAt = timePtr(time.Now().UTC().Add(w.options.StatusInterval))
	return w.store.SaveDownload(ctx, download)
}

func (w *Aria2Worker) pollDownload(ctx context.Context, download *domain.Download) error {
	status, err := w.backend.TellStatus(ctx, download.Aria2InstanceID, download.Aria2GID)
	if err != nil {
		if isPikPakEOFFailure(err.Error()) {
			return w.retryPikPakEOF(ctx, download, err.Error())
		}
		return w.retryPollDownload(ctx, download, err)
	}
	if status.Status == "error" {
		return w.handleAria2Error(ctx, download, status)
	}
	return w.applyStatus(ctx, download, status)
}

func (w *Aria2Worker) applyStatus(ctx context.Context, download *domain.Download, status aria2.Status) error {
	total, err := parseAria2Length(status.TotalLength)
	if err != nil {
		return w.retryDownload(ctx, download, err)
	}
	completed, err := parseAria2Length(status.CompletedLength)
	if err != nil {
		return w.retryDownload(ctx, download, err)
	}
	download.TotalLength = total
	download.CompletedLength = completed
	download.LastError = ""
	download.RetryCount = 0
	switch status.Status {
	case "complete":
		download.Status = domain.DownloadComplete
		download.NextAttemptAt = nil
	case "active":
		download.Status = domain.DownloadActive
		download.NextAttemptAt = timePtr(time.Now().UTC().Add(w.options.StatusInterval))
	case "waiting":
		download.Status = domain.DownloadWaiting
		download.NextAttemptAt = timePtr(time.Now().UTC().Add(w.options.StatusInterval))
	case "paused":
		download.Status = domain.DownloadPaused
		download.NextAttemptAt = timePtr(time.Now().UTC().Add(w.options.StatusInterval))
	case "error":
		return w.handleAria2Error(ctx, download, status)
	default:
		download.Status = domain.DownloadSubmitted
		download.NextAttemptAt = timePtr(time.Now().UTC().Add(w.options.StatusInterval))
	}
	return w.store.SaveDownload(ctx, download)
}

func (w *Aria2Worker) handleAria2Error(ctx context.Context, download *domain.Download, status aria2.Status) error {
	message := strings.TrimSpace(status.ErrorMessage)
	if isPikPakEOFFailure(message) {
		return w.retryPikPakEOF(ctx, download, message)
	}
	if message == "" {
		if status.ErrorCode != "" {
			message = "aria2 错误码 " + status.ErrorCode
		} else {
			message = "aria2 下载失败"
		}
	}

	oldGID := download.Aria2GID
	newGID := retryGID(oldGID)
	download.Aria2GID = newGID
	download.Status = domain.DownloadPending

	slog.Warn("aria2 下载失败，保留失败记录并准备重试",
		"task_id", download.TaskID,
		"file_id", download.PikPakFileID,
		"old_gid", oldGID,
		"next_gid", newGID,
		"error_code", status.ErrorCode,
		"error_message", status.ErrorMessage,
		"retry_in", w.options.RetryInterval,
	)

	return w.retryDownload(ctx, download, errors.New(message))
}

func isPikPakEOFFailure(message string) bool {
	return strings.Contains(strings.ToLower(message), "eof was received")
}

func (w *Aria2Worker) retryPikPakEOF(ctx context.Context, download *domain.Download, message string) error {
	if err := w.backend.Forget(ctx, download.Aria2InstanceID, download.Aria2GID); err != nil {
		slog.Warn("检测到 PikPak 下载 EOF，但清理 aria2 失败记录失败",
			"task_id", download.TaskID,
			"file_id", download.PikPakFileID,
			"gid", download.Aria2GID,
			"error", err,
			"retry_in", w.options.RetryInterval,
		)
		return w.retryPollDownload(ctx, download, errors.New(message))
	}

	download.LastError = message
	if download.EOFRetryCount >= w.options.MaxRetry {
		download.Status = domain.DownloadError
		download.NextAttemptAt = nil
		if err := w.store.SaveDownload(ctx, download); err != nil {
			return err
		}
		eventMessage := fmt.Sprintf("PikPak 下载 EOF，已完成 %d/%d 次重试仍失败：%s",
			download.EOFRetryCount, w.options.MaxRetry, message)
		w.recordTaskEvent(ctx, download.TaskID, "aria2.eof_exhausted", eventMessage)
		slog.Warn("PikPak 下载 EOF 已耗尽重试次数",
			"task_id", download.TaskID,
			"file_id", download.PikPakFileID,
			"gid", download.Aria2GID,
			"eof_retry_count", download.EOFRetryCount,
			"max_retry", w.options.MaxRetry,
			"error", message,
		)
		return errors.New(eventMessage)
	}

	download.EOFRetryCount++
	attempt := download.EOFRetryCount
	download.Status = domain.DownloadPending
	download.NextAttemptAt = timePtr(time.Now().UTC().Add(w.options.RetryInterval))
	if err := w.store.SaveDownload(ctx, download); err != nil {
		return err
	}
	eventMessage := fmt.Sprintf("PikPak 下载 EOF，准备第 %d/%d 次重试：%s", attempt, w.options.MaxRetry, message)
	w.recordTaskEvent(ctx, download.TaskID, "aria2.eof_retry", eventMessage)
	slog.Warn("检测到 PikPak 下载 EOF，已删除 aria2 失败记录并准备重新下载",
		"task_id", download.TaskID,
		"file_id", download.PikPakFileID,
		"gid", download.Aria2GID,
		"eof_retry_count", attempt,
		"max_retry", w.options.MaxRetry,
		"error", message,
		"retry_in", w.options.RetryInterval,
	)
	return errors.New(message)
}

func retryGID(previous string) string {
	sum := sha256.Sum256([]byte(previous + "\x00retry"))
	return hex.EncodeToString(sum[:8])
}

func (w *Aria2Worker) retryPollDownload(ctx context.Context, download *domain.Download, cause error) error {
	download.RetryCount++
	download.LastError = cause.Error()
	if download.RetryCount >= w.options.MaxRetry {
		download.Status = domain.DownloadError
		download.NextAttemptAt = nil
	} else {
		download.NextAttemptAt = timePtr(time.Now().UTC().Add(w.options.RetryInterval))
	}
	if err := w.store.SaveDownload(ctx, download); err != nil {
		return err
	}
	w.recordTaskEvent(ctx, download.TaskID, "aria2.poll_retry",
		fmt.Sprintf("aria2 状态查询失败，第 %d/%d 次重试：%s", download.RetryCount, w.options.MaxRetry, cause.Error()))
	return cause
}

func (w *Aria2Worker) retryDownload(ctx context.Context, download *domain.Download, cause error) error {
	download.RetryCount++
	download.LastError = cause.Error()
	if download.RetryCount >= w.options.MaxRetry {
		download.Status = domain.DownloadError
		download.NextAttemptAt = nil
	} else {
		download.Status = domain.DownloadPending
		download.NextAttemptAt = timePtr(time.Now().UTC().Add(w.options.RetryInterval))
	}
	if err := w.store.SaveDownload(ctx, download); err != nil {
		return err
	}
	w.recordTaskEvent(ctx, download.TaskID, "aria2.file_retry",
		fmt.Sprintf("aria2 文件下载失败，第 %d/%d 次重试：%s", download.RetryCount, w.options.MaxRetry, cause.Error()))
	return cause
}

func (w *Aria2Worker) retryTask(ctx context.Context, task *domain.Task, cause error) error {
	task.RetryCount++
	task.Error = cause.Error()
	if task.RetryCount >= w.options.MaxRetry {
		task.Status = domain.TaskAria2Failed
		task.NextAttemptAt = nil
		return w.store.SaveTask(ctx, task, "aria2.failed", cause.Error())
	}
	task.Status = domain.TaskAria2Downloading
	task.NextAttemptAt = timePtr(time.Now().UTC().Add(w.options.RetryInterval))
	return w.store.SaveTask(ctx, task, "aria2.retry", cause.Error())
}

func (w *Aria2Worker) failTask(ctx context.Context, task *domain.Task, cause error) error {
	task.Status = domain.TaskAria2Failed
	task.Error = cause.Error()
	task.NextAttemptAt = nil
	return w.store.SaveTask(ctx, task, "aria2.failed", cause.Error())
}

func (w *Aria2Worker) recordTaskEvent(ctx context.Context, taskID, eventType, message string) {
	if err := w.store.AddTaskEvent(ctx, taskID, eventType, message); err != nil {
		slog.Warn("记录任务错误历史失败", "task_id", taskID, "event_type", eventType, "error", err)
	}
}

func downloadRetriesExhausted(download *domain.Download, maxRetry int) bool {
	return download.RetryCount >= maxRetry || download.EOFRetryCount >= maxRetry
}

func deterministicGID(taskID, fileID string) string {
	return deterministicGIDForAttempt(taskID, fileID, 0)
}

func deterministicGIDForAttempt(taskID, fileID string, manualRetry int) string {
	value := taskID + "\x00" + fileID
	if manualRetry > 0 {
		value += "\x00manual:" + strconv.Itoa(manualRetry)
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

func parseAria2Length(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid aria2 length %q", value)
	}
	return n, nil
}
