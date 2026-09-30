package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/nukewarrior/pikpak-bridge/internal/aria2"
	"github.com/nukewarrior/pikpak-bridge/internal/domain"
	"github.com/nukewarrior/pikpak-bridge/internal/pikpak"
)

type aria2Store interface {
	ListAria2Work(ctx context.Context, now time.Time, limit int) ([]domain.Task, error)
	SaveTask(ctx context.Context, task *domain.Task, eventType, message string) error
	EnsureDownloads(ctx context.Context, taskID, instanceID string, gidFor func(string, string) string) error
	ListDownloads(ctx context.Context, taskID string) ([]domain.Download, error)
	SaveDownload(ctx context.Context, download *domain.Download) error
}

type Aria2Options struct {
	WorkerInterval time.Duration
	StatusInterval time.Duration
	RetryInterval  time.Duration
	MaxRetry       int
}

type Aria2Worker struct {
	store   aria2Store
	pikpak  pikpak.Provider
	backend aria2.Backend
	options Aria2Options
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
	return &Aria2Worker{store: store, pikpak: provider, backend: backend, options: options}
}

func (w *Aria2Worker) Run(ctx context.Context) {
	slog.Info("aria2 worker started",
		"worker_interval", w.options.WorkerInterval,
		"status_interval", w.options.StatusInterval,
		"retry_interval", w.options.RetryInterval,
	)
	defer slog.Info("aria2 worker stopped")
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
		slog.Error("aria2 worker iteration failed", "error", err)
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
		if err := w.processTask(ctx, &tasks[i]); err != nil {
			slog.Warn("aria2 task processing failed", "task_id", tasks[i].ID, "error", err)
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
		if err := w.store.EnsureDownloads(ctx, task.ID, task.Aria2InstanceID, deterministicGID); err != nil {
			return w.retryTask(ctx, task, err)
		}
		slog.Info("aria2 dispatch started",
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
	slog.Warn("aria2 target not ready",
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
				slog.Warn("aria2 file submit failed", "task_id", task.ID, "file_id", download.PikPakFileID, "error", err)
			}
		case domain.DownloadError:
			if download.RetryCount >= w.options.MaxRetry {
				continue
			}
			if err := w.submitDownload(ctx, task, download); err != nil {
				slog.Warn("aria2 file submit failed", "task_id", task.ID, "file_id", download.PikPakFileID, "error", err)
			}
		default:
			if err := w.pollDownload(ctx, download); err != nil {
				slog.Warn("aria2 file poll failed", "task_id", task.ID, "file_id", download.PikPakFileID, "error", err)
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
		if download.Status == domain.DownloadError && download.RetryCount >= w.options.MaxRetry {
			failed = true
		}
	}
	if failed {
		return w.failTask(ctx, task, errors.New("one or more aria2 downloads exhausted retries"))
	}
	if allComplete {
		slog.Info("aria2 downloads complete",
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

func (w *Aria2Worker) submitDownload(ctx context.Context, task *domain.Task, download *domain.Download) error {
	if status, err := w.backend.TellStatus(ctx, download.Aria2InstanceID, download.Aria2GID); err == nil {
		return w.applyStatus(ctx, download, status)
	}

	uri, err := w.pikpak.GetDownloadURL(ctx, task.PikPakAccountID, download.PikPakFileID)
	if err != nil {
		return w.retryDownload(ctx, download, err)
	}
	gid, err := w.backend.Add(
		ctx,
		download.Aria2InstanceID,
		task.DownloadDir,
		uri,
		download.Aria2GID,
		download.RelativePath,
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
	slog.Info("aria2 download submitted",
		"task_id", task.ID,
		"aria2_instance_id", download.Aria2InstanceID,
		"gid", gid,
		"path", download.RelativePath,
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
		return w.retryPollDownload(ctx, download, err)
	}
	if status.Status == "error" {
		_ = w.backend.Forget(ctx, download.Aria2InstanceID, download.Aria2GID)
		download.Status = domain.DownloadPending
		message := status.ErrorMessage
		if message == "" {
			message = "aria2 error code " + status.ErrorCode
		}
		return w.retryDownload(ctx, download, errors.New(message))
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
		download.Status = domain.DownloadPending
		return w.retryDownload(ctx, download, errors.New(nonEmpty(status.ErrorMessage, "aria2 download failed")))
	default:
		download.Status = domain.DownloadSubmitted
		download.NextAttemptAt = timePtr(time.Now().UTC().Add(w.options.StatusInterval))
	}
	return w.store.SaveDownload(ctx, download)
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

func deterministicGID(taskID, fileID string) string {
	sum := sha256.Sum256([]byte(taskID + "\x00" + fileID))
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
