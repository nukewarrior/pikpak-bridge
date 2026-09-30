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
	PikPakActiveCounts(ctx context.Context) (map[string]int, error)
	PikPakKnownTaskIDs(ctx context.Context) (map[string]map[string]struct{}, error)
	SaveTask(ctx context.Context, task *domain.Task, eventType, message string) error
	ReplaceRemoteFiles(ctx context.Context, taskID string, files []domain.RemoteFile) error
}

type Options struct {
	AccountIDs              []string
	WorkerInterval          time.Duration
	QuotaRefresh            time.Duration
	StatusInterval          time.Duration
	StallTimeout            time.Duration
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

const allQuotaExhaustedError = "all enabled PikPak accounts have exhausted cloud download quota"

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
	if options.StallTimeout <= 0 {
		options.StallTimeout = 20 * time.Minute
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
	slog.Info("PikPak 工作者已启动",
		"accounts", len(w.options.AccountIDs),
		"worker_interval", w.options.WorkerInterval,
		"quota_refresh", w.options.QuotaRefresh,
		"status_interval", w.options.StatusInterval,
		"stall_timeout", w.options.StallTimeout,
		"retry_interval", w.options.RetryInterval,
		"min_free_space", w.options.MinFreeSpace,
	)
	defer slog.Info("PikPak 工作者已停止")
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
		slog.Error("PikPak 工作循环执行失败", "error", err)
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
			slog.Warn("PikPak 任务处理失败", "task_id", tasks[i].ID, "error", err)
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
	if len(w.options.AccountIDs) == 0 {
		slog.Warn("PikPak 任务等待：未配置账号", "task_id", task.ID)
		return w.waitForAccount(
			ctx,
			task,
			"no PikPak account configured",
			now.Add(w.options.RetryInterval),
			"pikpak.waiting_account",
		)
	}

	managedCounts, err := w.store.PikPakActiveCounts(ctx)
	if err != nil {
		return err
	}
	knownTaskIDs, err := w.store.PikPakKnownTaskIDs(ctx)
	if err != nil {
		return err
	}

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
				slog.Warn("跳过处于冷却期的 PikPak 账号",
					"task_id", task.ID,
					"account_id", id,
					"account_name", snapshot.Name,
					"cooldown_until", runtime.cooldown,
					"failures", runtime.failures,
				)
			} else {
				slog.Warn("跳过处于冷却期的 PikPak 账号",
					"task_id", task.ID,
					"account_id", id,
					"cooldown_until", runtime.cooldown,
					"failures", runtime.failures,
				)
			}
			continue
		}

		if runtime.has && now.Sub(runtime.refreshed) < w.options.QuotaRefresh {
			snapshot := runtime.snapshot
			snapshot.CooldownUntil = runtime.cooldown
			remoteActive := snapshot.ActiveJobs
			externalActive := unmanagedRemoteActive(snapshot, knownTaskIDs[id])
			snapshot.ActiveJobs = managedCounts[id] + externalActive + runtime.reserved
			snapshots = append(snapshots, snapshot)
			if remoteActive != snapshot.ActiveJobs {
				slog.Debug("PikPak 并发计数已剔除 Bridge 残留远端任务",
					"account_id", id,
					"remote_active_jobs", remoteActive,
					"managed_active_jobs", managedCounts[id]+runtime.reserved,
					"external_active_jobs", externalActive,
					"effective_active_jobs", snapshot.ActiveJobs,
				)
			}
			continue
		}

		snapshot, err := w.provider.RefreshAccount(ctx, id)
		if err != nil {
			w.noteAccountFailure(id, snapshot, err, now)
			failed := w.runtime(id)
			slog.Warn("PikPak 账号状态刷新失败",
				"task_id", task.ID,
				"account_id", id,
				"account_name", snapshot.Name,
				"error", err,
				"error_kind", pikpak.KindOf(err),
				"failures", failed.failures,
				"cooldown_until", failed.cooldown,
			)
			continue
		}
		w.noteAccountSuccess(id, snapshot, now)
		remoteActive := snapshot.ActiveJobs
		externalActive := unmanagedRemoteActive(snapshot, knownTaskIDs[id])
		snapshot.ActiveJobs = managedCounts[id] + externalActive + runtime.reserved
		snapshots = append(snapshots, snapshot)
		slog.Info("PikPak 账号状态已刷新",
			"account_id", snapshot.ID,
			"account_name", snapshot.Name,
			"enabled", snapshot.Enabled,
			"healthy", snapshot.Healthy,
			"state", snapshot.State,
			"quota_remaining", snapshot.QuotaRemaining,
			"quota_total", snapshot.QuotaTotal,
			"storage_free", snapshot.StorageFree,
			"remote_active_jobs", remoteActive,
			"managed_active_jobs", managedCounts[id]+runtime.reserved,
			"external_active_jobs", externalActive,
			"effective_active_jobs", snapshot.ActiveJobs,
			"max_jobs", snapshot.MaxJobs,
		)
	}

	selected, err := scheduler.SelectPikPakAccount(snapshots, w.options.MinFreeSpace, now)
	if err != nil {
		waitUntil := now.Add(w.options.RetryInterval)
		waitError := "no eligible PikPak account"
		waitEvent := "pikpak.waiting_account"
		if allEnabledAccountsQuotaExhausted(snapshots, len(w.options.AccountIDs)) {
			waitUntil = w.nextQuotaRefreshAt(now)
			waitError = allQuotaExhaustedError
			waitEvent = "pikpak.waiting_quota"
		}

		slog.Warn("PikPak 任务等待：没有可用账号",
			"task_id", task.ID,
			"configured_accounts", len(w.options.AccountIDs),
			"snapshots", len(snapshots),
			"required_free_space", w.options.MinFreeSpace,
			"reason", waitError,
			"retry_in", waitUntil.Sub(now),
			"next_attempt_at", waitUntil,
		)
		for _, snapshot := range snapshots {
			reasons := accountEligibilityReasons(snapshot, w.options.MinFreeSpace, now)
			slog.Warn("PikPak 账号不可用",
				"task_id", task.ID,
				"account_id", snapshot.ID,
				"account_name", snapshot.Name,
				"reasons", strings.Join(reasons, ","),
				"enabled", snapshot.Enabled,
				"healthy", snapshot.Healthy,
				"state", snapshot.State,
				"quota_remaining", snapshot.QuotaRemaining,
				"quota_total", snapshot.QuotaTotal,
				"storage_free", snapshot.StorageFree,
				"active_jobs", snapshot.ActiveJobs,
				"max_jobs", snapshot.MaxJobs,
				"cooldown_until", snapshot.CooldownUntil,
				"last_used_at", snapshot.LastUsedAt,
			)
		}
		return w.waitForAccount(ctx, task, waitError, waitUntil, waitEvent)
	}

	slog.Info("已选择 PikPak 账号",
		"task_id", task.ID,
		"account_id", selected.ID,
		"account_name", selected.Name,
		"quota_remaining", selected.QuotaRemaining,
		"storage_free", selected.StorageFree,
		"active_jobs", selected.ActiveJobs,
		"max_jobs", selected.MaxJobs,
	)

	runtime := w.runtime(selected.ID)
	runtime.reserved++

	task.PikPakAccountID = selected.ID
	task.PikPakTaskID = ""
	task.PikPakRootFileID = ""
	task.PikPakPhase = ""
	task.PikPakProgress = 0
	task.PikPakLastActivityAt = nil
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

	slog.Info("开始提交 PikPak 离线下载",
		"task_id", task.ID,
		"account_id", task.PikPakAccountID,
		"source_type", task.SourceType,
	)
	remote, err := w.provider.SubmitOffline(ctx, task.PikPakAccountID, task.Source)
	if err != nil {
		w.noteSubmitError(task.PikPakAccountID, err)
		slog.Warn("PikPak 离线下载提交失败",
			"task_id", task.ID,
			"account_id", task.PikPakAccountID,
			"error", err,
			"error_kind", pikpak.KindOf(err),
		)
		switch pikpak.KindOf(err) {
		case pikpak.ErrorKindQuota, pikpak.ErrorKindStorage, pikpak.ErrorKindAuth, pikpak.ErrorKindCaptcha:
			task.PikPakAccountID = ""
			task.PikPakTaskID = ""
			task.PikPakRootFileID = ""
			task.PikPakPhase = ""
			task.PikPakProgress = 0
			task.PikPakLastActivityAt = nil
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
	slog.Info("PikPak 离线下载提交成功",
		"task_id", task.ID,
		"account_id", task.PikPakAccountID,
		"pikpak_task_id", remote.ID,
		"phase", remote.Status,
		"root_file_id", remote.RootFileID,
	)
	now := time.Now().UTC()
	task.PikPakTaskID = remote.ID
	if remote.RootFileID != "" {
		task.PikPakRootFileID = remote.RootFileID
	}
	task.PikPakPhase = remote.Status
	task.PikPakProgress = remote.Progress
	task.PikPakLastActivityAt = timePtr(now)
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

	now := time.Now().UTC()
	activity := task.PikPakLastActivityAt == nil ||
		task.PikPakPhase != remote.Status ||
		task.PikPakProgress != remote.Progress ||
		(remote.RootFileID != "" && task.PikPakRootFileID != remote.RootFileID)
	if remote.RootFileID != "" {
		task.PikPakRootFileID = remote.RootFileID
	}
	task.PikPakPhase = remote.Status
	task.PikPakProgress = remote.Progress
	if activity {
		task.PikPakLastActivityAt = timePtr(now)
	}
	task.RetryCount = 0
	task.Error = ""

	switch remote.Status {
	case pikpak.PhaseComplete:
		slog.Info("PikPak 离线下载已完成",
			"task_id", task.ID,
			"account_id", task.PikPakAccountID,
			"pikpak_task_id", task.PikPakTaskID,
			"root_file_id", task.PikPakRootFileID,
			"progress", task.PikPakProgress,
		)
		w.invalidateAccount(task.PikPakAccountID)
		if task.PikPakRootFileID == "" {
			task.NextAttemptAt = timePtr(now.Add(w.options.StatusInterval))
			task.Error = "PikPak task complete but root file ID is not available yet"
		} else {
			task.Status = domain.TaskPikPakComplete
			task.NextAttemptAt = nil
		}
	case pikpak.PhaseError:
		slog.Warn("PikPak 离线下载失败",
			"task_id", task.ID,
			"account_id", task.PikPakAccountID,
			"pikpak_task_id", task.PikPakTaskID,
			"error", remote.Error,
		)
		w.invalidateAccount(task.PikPakAccountID)
		task.Status = domain.TaskPikPakFailed
		task.NextAttemptAt = nil
		task.Error = nonEmpty(remote.Error, "PikPak offline task failed")
	default:
		if task.PikPakLastActivityAt != nil && now.Sub(*task.PikPakLastActivityAt) >= w.options.StallTimeout {
			stalledFor := now.Sub(*task.PikPakLastActivityAt).Round(time.Second)
			cancelCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			cancelErr := w.provider.CancelOfflineTask(cancelCtx, task.PikPakAccountID, task.PikPakTaskID)
			cancel()
			w.invalidateAccount(task.PikPakAccountID)
			task.Status = domain.TaskPikPakFailed
			task.NextAttemptAt = nil
			task.Error = fmt.Sprintf("PikPak 云下载连续 %s 无进展（%d%%），已判定卡死", stalledFor, task.PikPakProgress)
			if cancelErr != nil {
				task.Error += "; 远端取消失败: " + cancelErr.Error()
			}
			slog.Warn("PikPak 离线任务卡死",
				"task_id", task.ID,
				"account_id", task.PikPakAccountID,
				"pikpak_task_id", task.PikPakTaskID,
				"progress", task.PikPakProgress,
				"stalled_for", stalledFor,
				"cancel_error", cancelErr,
			)
			return w.store.SaveTask(ctx, task, "pikpak.stalled", task.Error)
		}
		task.Status = domain.TaskPikPakRunning
		task.NextAttemptAt = timePtr(now.Add(w.options.StatusInterval))
	}

	return w.store.SaveTask(ctx, task, "", "")
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
	resolved, err := w.provider.ResolveFiles(ctx, task.PikPakAccountID, task.PikPakRootFileID)
	if err != nil {
		return w.retry(ctx, task, domain.TaskResolvingFiles, domain.TaskPikPakFailed, err)
	}
	files := resolved.Files

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
	if name := strings.TrimSpace(resolved.RootName); name != "" {
		task.Name = name
	}

	slog.Info("PikPak 文件解析完成",
		"task_id", task.ID,
		"account_id", task.PikPakAccountID,
		"root_file_id", task.PikPakRootFileID,
		"task_name", task.Name,
		"files", len(persisted),
		"downloadable", downloadable,
	)
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

func unmanagedRemoteActive(snapshot pikpak.AccountSnapshot, known map[string]struct{}) int {
	if len(snapshot.ActiveTaskIDs) == 0 {
		// Older/fake providers may only supply a count. Treat it as external rather than
		// silently exceeding remote activity we cannot attribute to Bridge.
		return snapshot.ActiveJobs
	}
	count := 0
	for _, taskID := range snapshot.ActiveTaskIDs {
		if _, owned := known[taskID]; !owned {
			count++
		}
	}
	return count
}

func (w *Worker) waitForAccount(
	ctx context.Context,
	task *domain.Task,
	reason string,
	nextAttempt time.Time,
	eventType string,
) error {
	stateChanged := task.Status != domain.TaskWaitingPikPakAccount || task.Error != reason
	task.Status = domain.TaskWaitingPikPakAccount
	task.Error = reason
	task.NextAttemptAt = timePtr(nextAttempt)
	if !stateChanged {
		eventType = ""
	}
	return w.store.SaveTask(ctx, task, eventType, reason)
}

func allEnabledAccountsQuotaExhausted(snapshots []pikpak.AccountSnapshot, configured int) bool {
	if configured == 0 || len(snapshots) != configured {
		return false
	}
	enabled := 0
	for _, snapshot := range snapshots {
		if !snapshot.Enabled {
			continue
		}
		enabled++
		if snapshot.QuotaRemaining > 0 {
			return false
		}
	}
	return enabled > 0
}

func (w *Worker) nextQuotaRefreshAt(now time.Time) time.Time {
	next := now.Add(w.options.QuotaRefresh)
	found := false
	for _, id := range w.options.AccountIDs {
		runtime := w.runtime(id)
		if !runtime.has || !runtime.snapshot.Enabled || runtime.snapshot.QuotaRemaining > 0 || runtime.refreshed.IsZero() {
			continue
		}
		candidate := runtime.refreshed.Add(w.options.QuotaRefresh)
		if runtime.cooldown.After(now) && runtime.cooldown.After(candidate) {
			candidate = runtime.cooldown
		}
		if !found || candidate.Before(next) {
			next = candidate
			found = true
		}
	}
	if !next.After(now) {
		return now.Add(w.options.WorkerInterval)
	}
	return next
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


func accountEligibilityReasons(account pikpak.AccountSnapshot, requiredBytes int64, now time.Time) []string {
	reasons := make([]string, 0, 6)
	if !account.Enabled {
		reasons = append(reasons, "已禁用")
	}
	if !account.Healthy {
		reasons = append(reasons, "状态异常")
	}
	if account.QuotaRemaining <= 0 {
		reasons = append(reasons, "云下载额度已用尽")
	}
	if !account.CooldownUntil.IsZero() && now.Before(account.CooldownUntil) {
		reasons = append(reasons, "冷却中")
	}
	if account.MaxJobs <= 0 {
		reasons = append(reasons, "最大并发配置无效")
	} else if account.ActiveJobs >= account.MaxJobs {
		reasons = append(reasons, "已达到并发上限")
	}
	if requiredBytes > 0 && account.StorageFree < requiredBytes {
		reasons = append(reasons, "剩余空间不足")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "未知原因")
	}
	return reasons
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
