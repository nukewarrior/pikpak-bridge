package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/nukewarrior/pikpak-bridge/internal/domain"
	"github.com/nukewarrior/pikpak-bridge/internal/pikpak"
	"github.com/nukewarrior/pikpak-bridge/internal/scheduler"
)

type pikpakStore interface {
	ListPikPakWork(ctx context.Context, now time.Time, limit int) ([]domain.Task, error)
	SaveTask(ctx context.Context, task *domain.Task, eventType, message string) error
	ReplaceRemoteFiles(ctx context.Context, taskID string, files []domain.RemoteFile) error
}

type Options struct {
	AccountIDs              []string
	WorkerInterval          time.Duration
	QuotaRefresh            time.Duration
	StatusInterval          time.Duration
	RetryInterval           time.Duration
	MaxRetry                int
	MinFreeSpace            int64
	AccountFailureThreshold int
	AccountCooldown         time.Duration
}

type Worker struct {
	store    pikpakStore
	provider pikpak.Provider
	options  Options
	accounts map[string]*accountRuntime
}

type accountRuntime struct {
	snapshot  pikpak.AccountSnapshot
	has       bool
	refreshed time.Time
	failures  int
	cooldown  time.Time
	reserved  int
}

func New(store pikpakStore, provider pikpak.Provider, options Options) *Worker {
	if options.WorkerInterval <= 0 {
		options.WorkerInterval = 2 * time.Second
	}
	if options.QuotaRefresh <= 0 {
		options.QuotaRefresh = 5 * time.Minute
	}
	if options.StatusInterval <= 0 {
		options.StatusInterval = 10 * time.Second
	}
	if options.RetryInterval <= 0 {
		options.RetryInterval = 30 * time.Second
	}
	if options.MaxRetry <= 0 {
		options.MaxRetry = 10
	}
	if options.AccountFailureThreshold <= 0 {
		options.AccountFailureThreshold = 3
	}
	if options.AccountCooldown <= 0 {
		options.AccountCooldown = 30 * time.Minute
	}

	return &Worker{
		store:    store,
		provider: provider,
		options:  options,
		accounts: make(map[string]*accountRuntime, len(options.AccountIDs)),
	}
}

func (w *Worker) Run(ctx context.Context) {
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

func (w *Worker) runOnceLogged(ctx context.Context) {
	if err := w.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("pikpak worker iteration failed", "error", err)
	}
}

func (w *Worker) RunOnce(ctx context.Context) error {
	tasks, err := w.store.ListPikPakWork(ctx, time.Now().UTC(), 100)
	if err != nil {
		return err
	}
	for i := range tasks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.processTask(ctx, &tasks[i]); err != nil {
			slog.Warn("pikpak task processing failed", "task_id", tasks[i].ID, "error", err)
		}
	}
	return nil
}

func (w *Worker) processTask(ctx context.Context, task *domain.Task) error {
	switch task.Status {
	case domain.TaskQueued, domain.TaskWaitingPikPakAccount:
		return w.selectAndSubmit(ctx, task)
	case domain.TaskPikPakSubmitting:
		return w.submit(ctx, task, false)
	case domain.TaskPikPakRunning:
		return w.poll(ctx, task)
	case domain.TaskPikPakComplete:
		return w.beginResolve(ctx, task)
	case domain.TaskResolvingFiles:
		return w.resolveFiles(ctx, task)
	default:
		return nil
	}
}

func (w *Worker) selectAndSubmit(ctx context.Context, task *domain.Task) error {
	now := time.Now().UTC()
	snapshots := make([]pikpak.AccountSnapshot, 0, len(w.options.AccountIDs))
	for _, id := range w.options.AccountIDs {
		runtime := w.runtime(id)
		if now.Before(runtime.cooldown) {
			if runtime.has {
				snapshot := runtime.snapshot
				snapshot.Healthy = false
				snapshot.CooldownUntil = runtime.cooldown
				snapshot.ActiveJobs += runtime.reserved
				snapshots = append(snapshots, snapshot)
			}
			continue
		}

		if runtime.has && now.Sub(runtime.refreshed) < w.options.QuotaRefresh {
			snapshot := runtime.snapshot
			snapshot.CooldownUntil = runtime.cooldown
			snapshot.ActiveJobs += runtime.reserved
			snapshots = append(snapshots, snapshot)
			continue
		}

		snapshot, err := w.provider.RefreshAccount(ctx, id)
		if err != nil {
			w.noteAccountFailure(id, snapshot, err, now)
			continue
		}
		w.noteAccountSuccess(id, snapshot, now)
		snapshot.ActiveJobs += runtime.reserved
		snapshots = append(snapshots, snapshot)
	}

	selected, err := scheduler.SelectPikPakAccount(snapshots, w.options.MinFreeSpace, now)
	if err != nil {
		task.Status = domain.TaskWaitingPikPakAccount
		task.Error = "no eligible PikPak account"
		task.NextAttemptAt = timePtr(now.Add(w.options.RetryInterval))
		return w.store.SaveTask(ctx, task, "pikpak.waiting_account", task.Error)
	}

	runtime := w.runtime(selected.ID)
	runtime.reserved++

	task.PikPakAccountID = selected.ID
	task.PikPakTaskID = ""
	task.PikPakRootFileID = ""
	task.Status = domain.TaskPikPakSubmitting
	task.Error = ""
	task.RetryCount = 0
	task.NextAttemptAt = nil
	if err := w.store.SaveTask(ctx, task, "pikpak.account_selected", selected.ID); err != nil {
		runtime.reserved--
		return err
	}

	return w.submit(ctx, task, true)
}

func (w *Worker) submit(ctx context.Context, task *domain.Task, reserved bool) error {
	if task.PikPakAccountID == "" {
		task.Status = domain.TaskWaitingPikPakAccount
		task.NextAttemptAt = nil
		return w.store.SaveTask(ctx, task, "pikpak.account_missing", "reselect account")
	}
	if reserved {
		defer w.releaseReservation(task.PikPakAccountID)
	}

	remote, err := w.provider.SubmitOffline(ctx, task.PikPakAccountID, task.Source)
	if err != nil {
		w.noteSubmitError(task.PikPakAccountID, err)
		switch pikpak.KindOf(err) {
		case pikpak.ErrorKindQuota, pikpak.ErrorKindStorage, pikpak.ErrorKindAuth, pikpak.ErrorKindCaptcha:
			task.PikPakAccountID = ""
			task.PikPakTaskID = ""
			task.PikPakRootFileID = ""
			task.Status = domain.TaskWaitingPikPakAccount
			task.Error = err.Error()
			task.NextAttemptAt = timePtr(time.Now().UTC().Add(w.options.RetryInterval))
			return w.store.SaveTask(ctx, task, "pikpak.account_rejected", err.Error())
		default:
			return w.retry(ctx, task, domain.TaskPikPakSubmitting, domain.TaskPikPakFailed, err)
		}
	}

	if remote.ID == "" {
		return w.retry(ctx, task, domain.TaskPikPakSubmitting, domain.TaskPikPakFailed,
			errors.New("PikPak returned an empty offline task ID"))
	}

	w.noteSubmitSuccess(task.PikPakAccountID, remote.Status == pikpak.PhasePending || remote.Status == pikpak.PhaseRunning)
	task.PikPakTaskID = remote.ID
	if remote.RootFileID != "" {
		task.PikPakRootFileID = remote.RootFileID
	}
	task.RetryCount = 0
	task.Error = ""
	task.NextAttemptAt = nil

	switch remote.Status {
	case pikpak.PhaseError:
		task.Status = domain.TaskPikPakFailed
		task.Error = nonEmpty(remote.Error, "PikPak offline task failed")
	case pikpak.PhaseComplete:
		if task.PikPakRootFileID != "" {
			task.Status = domain.TaskPikPakComplete
		} else {
			task.Status = domain.TaskPikPakRunning
			task.NextAttemptAt = timePtr(time.Now().UTC().Add(w.options.StatusInterval))
		}
	default:
		task.Status = domain.TaskPikPakRunning
		task.NextAttemptAt = timePtr(time.Now().UTC().Add(w.options.StatusInterval))
	}

	return w.store.SaveTask(ctx, task, "pikpak.submitted", remote.ID)
}

func (w *Worker) poll(ctx context.Context, task *domain.Task) error {
	if task.PikPakTaskID == "" {
		task.Status = domain.TaskPikPakSubmitting
		task.NextAttemptAt = nil
		return w.store.SaveTask(ctx, task, "pikpak.recover_submit", "missing persisted remote task ID")
	}

	remote, err := w.provider.GetOfflineTask(ctx, task.PikPakAccountID, task.PikPakTaskID)
	if err != nil {
		return w.retry(ctx, task, domain.TaskPikPakRunning, domain.TaskPikPakFailed, err)
	}

	if remote.RootFileID != "" {
		task.PikPakRootFileID = remote.RootFileID
	}
	task.RetryCount = 0
	task.Error = ""

	switch remote.Status {
	case pikpak.PhaseComplete:
		w.invalidateAccount(task.PikPakAccountID)
		if task.PikPakRootFileID == "" {
			task.NextAttemptAt = timePtr(time.Now().UTC().Add(w.options.StatusInterval))
			task.Error = "PikPak task complete but root file ID is not available yet"
		} else {
			task.Status = domain.TaskPikPakComplete
			task.NextAttemptAt = nil
		}
	case pikpak.PhaseError:
		w.invalidateAccount(task.PikPakAccountID)
		task.Status = domain.TaskPikPakFailed
		task.NextAttemptAt = nil
		task.Error = nonEmpty(remote.Error, "PikPak offline task failed")
	default:
		task.Status = domain.TaskPikPakRunning
		task.NextAttemptAt = timePtr(time.Now().UTC().Add(w.options.StatusInterval))
	}

	return w.store.SaveTask(ctx, task, "pikpak.polled", string(task.Status))
}

func (w *Worker) beginResolve(ctx context.Context, task *domain.Task) error {
	if task.PikPakRootFileID == "" {
		task.Status = domain.TaskPikPakRunning
		task.NextAttemptAt = timePtr(time.Now().UTC().Add(w.options.StatusInterval))
		task.Error = "missing PikPak root file ID"
		return w.store.SaveTask(ctx, task, "pikpak.root_missing", task.Error)
	}
	task.Status = domain.TaskResolvingFiles
	task.NextAttemptAt = nil
	task.Error = ""
	task.RetryCount = 0
	return w.store.SaveTask(ctx, task, "pikpak.resolve_started", task.PikPakRootFileID)
}

func (w *Worker) resolveFiles(ctx context.Context, task *domain.Task) error {
	files, err := w.provider.ListFiles(ctx, task.PikPakAccountID, task.PikPakRootFileID)
	if err != nil {
		return w.retry(ctx, task, domain.TaskResolvingFiles, domain.TaskPikPakFailed, err)
	}

	persisted := make([]domain.RemoteFile, 0, len(files))
	downloadable := 0
	for _, file := range files {
		persisted = append(persisted, domain.RemoteFile{
			TaskID:       task.ID,
			PikPakFileID: file.ID,
			ParentFileID: file.ParentID,
			Name:         file.Name,
			RelativePath: file.RelativePath,
			Size:         file.Size,
			IsFolder:     file.IsFolder,
		})
		if !file.IsFolder {
			downloadable++
		}
	}
	if downloadable == 0 {
		return w.retry(ctx, task, domain.TaskResolvingFiles, domain.TaskPikPakFailed,
			errors.New("PikPak result contains no downloadable files"))
	}
	if err := w.store.ReplaceRemoteFiles(ctx, task.ID, persisted); err != nil {
		return w.retry(ctx, task, domain.TaskResolvingFiles, domain.TaskPikPakFailed, err)
	}

	task.Status = domain.TaskWaitingAria2
	task.NextAttemptAt = nil
	task.RetryCount = 0
	task.Error = ""
	return w.store.SaveTask(ctx, task, "pikpak.files_resolved",
		fmt.Sprintf("%d files (%d downloadable)", len(persisted), downloadable))
}

func (w *Worker) retry(
	ctx context.Context,
	task *domain.Task,
	retryStatus domain.TaskStatus,
	failedStatus domain.TaskStatus,
	cause error,
) error {
	task.RetryCount++
	task.Error = cause.Error()
	if task.RetryCount >= w.options.MaxRetry {
		task.Status = failedStatus
		task.NextAttemptAt = nil
		return w.store.SaveTask(ctx, task, "pikpak.failed", cause.Error())
	}
	task.Status = retryStatus
	task.NextAttemptAt = timePtr(time.Now().UTC().Add(w.options.RetryInterval))
	return w.store.SaveTask(ctx, task, "pikpak.retry", cause.Error())
}

func (w *Worker) runtime(id string) *accountRuntime {
	runtime, ok := w.accounts[id]
	if !ok {
		runtime = &accountRuntime{}
		w.accounts[id] = runtime
	}
	return runtime
}

func (w *Worker) releaseReservation(id string) {
	runtime := w.runtime(id)
	if runtime.reserved > 0 {
		runtime.reserved--
	}
}

func (w *Worker) noteAccountSuccess(id string, snapshot pikpak.AccountSnapshot, now time.Time) {
	runtime := w.runtime(id)
	runtime.snapshot = snapshot
	runtime.snapshot.ID = id
	runtime.has = true
	runtime.refreshed = now
	runtime.failures = 0
	runtime.cooldown = time.Time{}
}

func (w *Worker) noteAccountFailure(id string, snapshot pikpak.AccountSnapshot, err error, now time.Time) {
	runtime := w.runtime(id)
	runtime.snapshot = snapshot
	runtime.snapshot.ID = id
	runtime.snapshot.Healthy = false
	runtime.has = true
	runtime.refreshed = now
	runtime.failures++

	if pikpak.KindOf(err) == pikpak.ErrorKindCaptcha || runtime.failures >= w.options.AccountFailureThreshold {
		runtime.cooldown = now.Add(w.options.AccountCooldown)
		runtime.snapshot.CooldownUntil = runtime.cooldown
	}
}

func (w *Worker) noteSubmitError(id string, err error) {
	runtime := w.runtime(id)
	switch pikpak.KindOf(err) {
	case pikpak.ErrorKindQuota:
		runtime.snapshot.QuotaRemaining = 0
	case pikpak.ErrorKindStorage:
		runtime.snapshot.StorageFree = 0
	case pikpak.ErrorKindCaptcha:
		runtime.snapshot.Healthy = false
		runtime.cooldown = time.Now().UTC().Add(w.options.AccountCooldown)
	default:
		runtime.failures++
		if runtime.failures >= w.options.AccountFailureThreshold {
			runtime.snapshot.Healthy = false
			runtime.cooldown = time.Now().UTC().Add(w.options.AccountCooldown)
		}
	}
}

func (w *Worker) noteSubmitSuccess(id string, active bool) {
	runtime := w.runtime(id)
	if runtime.snapshot.QuotaRemaining > 0 {
		runtime.snapshot.QuotaRemaining--
	}
	if active {
		runtime.snapshot.ActiveJobs++
	}
	runtime.snapshot.LastUsedAt = time.Now().UTC()
	runtime.failures = 0
}

func (w *Worker) invalidateAccount(id string) {
	w.runtime(id).refreshed = time.Time{}
}

func ParseByteSize(input string) (int64, error) {
	value := strings.TrimSpace(strings.ToUpper(input))
	if value == "" {
		return 0, nil
	}

	units := []struct {
		suffix string
		factor float64
	}{
		{"TIB", math.Pow(1024, 4)},
		{"GIB", math.Pow(1024, 3)},
		{"MIB", math.Pow(1024, 2)},
		{"KIB", 1024},
		{"TB", math.Pow(1000, 4)},
		{"GB", math.Pow(1000, 3)},
		{"MB", math.Pow(1000, 2)},
		{"KB", 1000},
		{"B", 1},
	}
	for _, unit := range units {
		if strings.HasSuffix(value, unit.suffix) {
			number := strings.TrimSpace(strings.TrimSuffix(value, unit.suffix))
			parsed, err := strconv.ParseFloat(number, 64)
			if err != nil || parsed < 0 {
				return 0, fmt.Errorf("invalid byte size %q", input)
			}
			return int64(parsed * unit.factor), nil
		}
	}

	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("invalid byte size %q", input)
	}
	return parsed, nil
}

func timePtr(value time.Time) *time.Time {
	return &value
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
