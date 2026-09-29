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
	"github.com/nukewarrior/pikpak-bridge/internal/scheduler"
)

type aria2Store interface {
	ListAria2Work(ctx context.Context, now time.Time, limit int) ([]domain.Task, error)
	SaveTask(ctx context.Context, task *domain.Task, eventType, message string) error
	EnsureDownloads(ctx context.Context, taskID, instance string, gidFor func(string, string) string) error
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
	store    aria2Store
	pikpak   pikpak.Provider
	backend  aria2.Backend
	options  Aria2Options
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
	if task.Aria2Instance == "" {
		if err := w.selectInstance(ctx, task); err != nil {
			return err
		}
		if task.Aria2Instance == "" {
			return nil
		}
	}
	if err := w.store.EnsureDownloads(ctx, task.ID, task.Aria2Instance, deterministicGID); err != nil {
		return w.retryTask(ctx, task, err)
	}
	if task.Status == domain.TaskWaitingAria2 {
		task.Status = domain.TaskAria2Downloading
		task.RetryCount = 0
		task.Error = ""
		task.NextAttemptAt = nil
		if err := w.store.SaveTask(ctx, task, "aria2.dispatch_started", task.Aria2Instance); err != nil {
			return err
		}
	}
	return w.processDownloads(ctx, task)
}

func (w *Aria2Worker) selectInstance(ctx context.Context, task *domain.Task) error {
	snapshots := w.backend.Snapshots(ctx)
	candidates := make([]scheduler.Aria2Snapshot, 0, len(snapshots))
	for _, snapshot := range snapshots {
		candidates = append(candidates, scheduler.Aria2Snapshot{
			Name:      snapshot.Name,
			Enabled:   snapshot.Enabled,
			Healthy:   snapshot.Healthy,
			Active:    snapshot.Active,
			Waiting:   snapshot.Waiting,
			MaxActive: snapshot.MaxActive,
			Weight:    snapshot.Weight,
		})
	}
	selected, err := scheduler.SelectAria2Instance(candidates)
	if err != nil {
		task.Status = domain.TaskWaitingAria2
		task.Error = "no eligible aria2 instance"
		task.NextAttemptAt = timePtr(time.Now().UTC().Add(w.options.RetryInterval))
		return w.store.SaveTask(ctx, task, "aria2.waiting_instance", task.Error)
	}
	task.Aria2Instance = selected.Name
	task.Error = ""
	task.RetryCount = 0
	task.NextAttemptAt = nil
	return w.store.SaveTask(ctx, task, "aria2.instance_selected", selected.Name)
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
	allComplete := true
	for i := range downloads {
		download := &downloads[i]
		if download.Status == domain.DownloadComplete {
			continue
		}
		allComplete = false
		if download.NextAttemptAt != nil && now.Before(*download.NextAttemptAt) {
			continue
		}
		switch download.Status {
		case domain.DownloadPending, domain.DownloadError:
			if err := w.submitDownload(ctx, task, download); err != nil {
				slog.Warn("aria2 file submit failed", "task_id", task.ID, "file_id", download.PikPakFileID, "error", err)
			}
		default:
			if err := w.pollDownload(ctx, task, download); err != nil {
				slog.Warn("aria2 file poll failed", "task_id", task.ID, "file_id", download.PikPakFileID, "error", err)
			}
		}
	}

	downloads, err = w.store.ListDownloads(ctx, task.ID)
	if err != nil {
		return w.retryTask(ctx, task, err)
	}
	allComplete = len(downloads) > 0
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
	if status, err := w.backend.TellStatus(ctx, download.Aria2Instance, download.Aria2GID); err == nil {
		return w.applyStatus(ctx, download, status)
	}

	uri, err := w.pikpak.GetDownloadURL(ctx, task.PikPakAccount, download.PikPakFileID)
	if err != nil {
		return w.retryDownload(ctx, download, err)
	}
	gid, err := w.backend.Add(ctx, download.Aria2Instance, uri, download.Aria2GID, download.RelativePath)
	if err != nil {
		// The add may have succeeded remotely and the response may have been
		// lost. Re-check the deterministic GID before scheduling a retry.
		if status, checkErr := w.backend.TellStatus(ctx, download.Aria2Instance, download.Aria2GID); checkErr == nil {
			return w.applyStatus(ctx, download, status)
		}
		return w.retryDownload(ctx, download, err)
	}
	if gid != download.Aria2GID {
		return w.retryDownload(ctx, download, fmt.Errorf("aria2 returned unexpected gid %q, want %q", gid, download.Aria2GID))
	}
	download.Status = domain.DownloadSubmitted
	download.RetryCount = 0
	download.LastError = ""
	download.NextAttemptAt = timePtr(time.Now().UTC().Add(w.options.StatusInterval))
	return w.store.SaveDownload(ctx, download)
}

func (w *Aria2Worker) pollDownload(ctx context.Context, task *domain.Task, download *domain.Download) error {
	status, err := w.backend.TellStatus(ctx, download.Aria2Instance, download.Aria2GID)
	if err != nil {
		// A missing result can also happen after aria2 was restarted. Reuse the
		// same deterministic GID and fetch a fresh PikPak URL on retry.
		download.Status = domain.DownloadPending
		return w.retryDownload(ctx, download, err)
	}
	if status.Status == "error" {
		_ = w.backend.Forget(ctx, download.Aria2Instance, download.Aria2GID)
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
