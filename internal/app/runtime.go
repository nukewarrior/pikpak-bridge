package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nukewarrior/pikpak-bridge/internal/aria2"
	"github.com/nukewarrior/pikpak-bridge/internal/config"
	"github.com/nukewarrior/pikpak-bridge/internal/pikpak"
	"github.com/nukewarrior/pikpak-bridge/internal/store"
	"github.com/nukewarrior/pikpak-bridge/internal/worker"
)

var (
	ErrAlreadyConfigured = errors.New("setup has already been completed")
	ErrNotConfigured     = errors.New("runtime is not configured")
)

type Runtime struct {
	mu         sync.RWMutex
	applyMu    sync.Mutex
	parent     context.Context
	db         *store.SQLite
	configPath string
	configured bool
	cfg        *config.Config
	provider   *pikpak.Manager
	registry   *aria2.Registry
	cancel     context.CancelFunc
	workersWG  *sync.WaitGroup
}

func NewRuntime(parent context.Context, db *store.SQLite, configPath string, cfg *config.Config, configured bool) (*Runtime, error) {
	r := &Runtime{
		parent:     parent,
		db:         db,
		configPath: configPath,
		cfg:        cloneConfig(cfg),
		configured: configured,
	}
	if configured {
		provider, registry, workers, err := r.prepare(cfg)
		if err != nil {
			return nil, err
		}
		r.mu.Lock()
		r.activateLocked(cfg, provider, registry, workers)
		r.mu.Unlock()
	}
	return r, nil
}

func (r *Runtime) Configured() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.configured
}

func (r *Runtime) CurrentConfig() *config.Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.cfg == nil {
		return nil
	}
	return cloneConfig(r.cfg)
}

func (r *Runtime) ApplySetup(ctx context.Context, cfg *config.Config) error {
	r.applyMu.Lock()
	defer r.applyMu.Unlock()

	r.mu.RLock()
	configured := r.configured
	r.mu.RUnlock()
	if configured {
		return ErrAlreadyConfigured
	}
	return r.apply(ctx, cfg, false)
}

func (r *Runtime) ApplyConfig(ctx context.Context, cfg *config.Config) error {
	r.applyMu.Lock()
	defer r.applyMu.Unlock()

	r.mu.RLock()
	configured := r.configured
	r.mu.RUnlock()
	if !configured {
		return ErrNotConfigured
	}
	return r.apply(ctx, cfg, true)
}

func (r *Runtime) apply(ctx context.Context, cfg *config.Config, reload bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}

	next := cloneConfig(cfg)
	provider, registry, workers, err := r.prepare(next)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := config.Save(r.configPath, next); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	if reload {
		r.stopWorkers()
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.activateLocked(next, provider, registry, workers)
	r.configured = true
	return nil
}

func (r *Runtime) stopWorkers() {
	r.mu.RLock()
	cancel := r.cancel
	wg := r.workersWG
	r.mu.RUnlock()

	if cancel != nil {
		cancel()
	}
	if wg != nil {
		wg.Wait()
	}
}

func (r *Runtime) AccountIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.configured || r.cfg == nil {
		return nil
	}
	out := make([]string, 0, len(r.cfg.PikPak.Accounts))
	for _, account := range r.cfg.PikPak.Accounts {
		out = append(out, account.ID)
	}
	return out
}

func (r *Runtime) RefreshAccount(ctx context.Context, id string) (pikpak.AccountSnapshot, error) {
	r.mu.RLock()
	provider := r.provider
	r.mu.RUnlock()
	if provider == nil {
		return pikpak.AccountSnapshot{ID: id, State: "NOT_CONFIGURED"}, errors.New("PikPak is not configured")
	}
	return provider.RefreshAccount(ctx, id)
}

func (r *Runtime) Aria2Snapshots(ctx context.Context) []aria2.InstanceSnapshot {
	r.mu.RLock()
	registry := r.registry
	r.mu.RUnlock()
	if registry == nil {
		return nil
	}
	return registry.Snapshots(ctx)
}

func (r *Runtime) DownloadTargets() []config.DownloadTarget {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.configured || r.cfg == nil {
		return nil
	}
	out := make([]config.DownloadTarget, 0, len(r.cfg.Targets))
	for _, target := range r.cfg.Targets {
		if config.Enabled(target.Enabled) {
			out = append(out, target)
		}
	}
	return out
}

func (r *Runtime) ResolveTarget(id string) (config.DownloadTarget, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.configured || r.cfg == nil {
		return config.DownloadTarget{}, ErrNotConfigured
	}

	var enabled []config.DownloadTarget
	for _, target := range r.cfg.Targets {
		if !config.Enabled(target.Enabled) {
			continue
		}
		enabled = append(enabled, target)
		if id != "" && target.ID == id {
			return target, nil
		}
	}
	if id != "" {
		return config.DownloadTarget{}, fmt.Errorf("download target %q not found or disabled", id)
	}
	for _, target := range enabled {
		if target.Default {
			return target, nil
		}
	}
	if len(enabled) == 1 {
		return enabled[0], nil
	}
	return config.DownloadTarget{}, errors.New("download target is required")
}

func (r *Runtime) prepare(cfg *config.Config) (*pikpak.Manager, *aria2.Registry, workerSet, error) {
	provider := pikpak.NewManager(cfg.PikPak.Accounts, cfg.PikPak.SessionDir)
	registry := aria2.NewRegistry(cfg.Aria2.Instances)
	workers, err := buildWorkers(cfg, r.db, provider, registry)
	if err != nil {
		return nil, nil, workerSet{}, err
	}
	return provider, registry, workers, nil
}

func (r *Runtime) activateLocked(cfg *config.Config, provider *pikpak.Manager, registry *aria2.Registry, workers workerSet) {
	workerCtx, cancel := context.WithCancel(r.parent)
	wg := &sync.WaitGroup{}
	wg.Add(3)

	r.cancel = cancel
	r.workersWG = wg
	r.provider = provider
	r.registry = registry
	r.cfg = cloneConfig(cfg)

	go func() {
		defer wg.Done()
		workers.pikpak.Run(workerCtx)
	}()
	go func() {
		defer wg.Done()
		workers.aria2.Run(workerCtx)
	}()
	go func() {
		defer wg.Done()
		workers.finalizer.Run(workerCtx)
	}()
}

type workerSet struct {
	pikpak    *worker.Worker
	aria2     *worker.Aria2Worker
	finalizer *worker.Finalizer
}

func buildWorkers(cfg *config.Config, db *store.SQLite, provider pikpak.Provider, backend aria2.Backend) (workerSet, error) {
	quotaRefresh, err := time.ParseDuration(cfg.PikPak.QuotaRefresh)
	if err != nil {
		return workerSet{}, fmt.Errorf("pikpak.quota_refresh: %w", err)
	}
	pikpakStatus, err := time.ParseDuration(cfg.PikPak.StatusInterval)
	if err != nil {
		return workerSet{}, fmt.Errorf("pikpak.status_interval: %w", err)
	}
	aria2Status, err := time.ParseDuration(cfg.Aria2.StatusInterval)
	if err != nil {
		return workerSet{}, fmt.Errorf("aria2.status_interval: %w", err)
	}
	workerInterval, err := time.ParseDuration(cfg.Scheduler.WorkerInterval)
	if err != nil {
		return workerSet{}, fmt.Errorf("scheduler.worker_interval: %w", err)
	}
	retryInterval, err := time.ParseDuration(cfg.Scheduler.RetryInterval)
	if err != nil {
		return workerSet{}, fmt.Errorf("scheduler.retry_interval: %w", err)
	}
	accountCooldown, err := time.ParseDuration(cfg.Scheduler.AccountCooldown)
	if err != nil {
		return workerSet{}, fmt.Errorf("scheduler.account_cooldown: %w", err)
	}
	cleanupDelay, err := time.ParseDuration(cfg.Cleanup.Delay)
	if err != nil {
		return workerSet{}, fmt.Errorf("cleanup.delay: %w", err)
	}
	minFreeSpace, err := worker.ParseByteSize(cfg.PikPak.MinFreeSpace)
	if err != nil {
		return workerSet{}, fmt.Errorf("pikpak.min_free_space: %w", err)
	}

	accountIDs := make([]string, 0, len(cfg.PikPak.Accounts))
	for _, account := range cfg.PikPak.Accounts {
		accountIDs = append(accountIDs, account.ID)
	}

	return workerSet{
		pikpak: worker.New(db, provider, worker.Options{
			AccountIDs:              accountIDs,
			WorkerInterval:          workerInterval,
			QuotaRefresh:            quotaRefresh,
			StatusInterval:          pikpakStatus,
			RetryInterval:           retryInterval,
			MaxRetry:                cfg.Scheduler.MaxRetry,
			MinFreeSpace:            minFreeSpace,
			AccountFailureThreshold: cfg.Scheduler.AccountFailureThreshold,
			AccountCooldown:         accountCooldown,
		}),
		aria2: worker.NewAria2(db, provider, backend, worker.Aria2Options{
			WorkerInterval: workerInterval,
			StatusInterval: aria2Status,
			RetryInterval:  retryInterval,
			MaxRetry:       cfg.Scheduler.MaxRetry,
		}),
		finalizer: worker.NewFinalizer(db, provider, worker.FinalizeOptions{
			WorkerInterval: workerInterval,
			RetryInterval:  retryInterval,
			MaxRetry:       cfg.Scheduler.MaxRetry,
			VerifySize:     cfg.Cleanup.VerifySize,
			CleanupEnabled: cfg.Cleanup.Enabled,
			CleanupDelay:   cleanupDelay,
		}),
	}, nil
}

func cloneConfig(src *config.Config) *config.Config {
	if src == nil {
		return nil
	}
	dst := *src
	dst.PikPak.Accounts = append([]config.PikPakAccount(nil), src.PikPak.Accounts...)
	dst.Aria2.Instances = append([]config.Aria2Instance(nil), src.Aria2.Instances...)
	dst.Targets = append([]config.DownloadTarget(nil), src.Targets...)
	for i := range dst.PikPak.Accounts {
		dst.PikPak.Accounts[i].Enabled = cloneBool(src.PikPak.Accounts[i].Enabled)
	}
	for i := range dst.Aria2.Instances {
		dst.Aria2.Instances[i].Enabled = cloneBool(src.Aria2.Instances[i].Enabled)
	}
	for i := range dst.Targets {
		dst.Targets[i].Enabled = cloneBool(src.Targets[i].Enabled)
	}
	return &dst
}

func cloneBool(src *bool) *bool {
	if src == nil {
		return nil
	}
	value := *src
	return &value
}
