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

var ErrAlreadyConfigured = errors.New("setup has already been completed")

type Runtime struct {
	mu         sync.RWMutex
	parent     context.Context
	db         *store.SQLite
	configPath string
	configured bool
	cfg        *config.Config
	provider   *pikpak.Manager
	ariaPool   *aria2.Pool
	cancel     context.CancelFunc
}

func NewRuntime(parent context.Context, db *store.SQLite, configPath string, cfg *config.Config, configured bool) (*Runtime, error) {
	r := &Runtime{
		parent:     parent,
		db:         db,
		configPath: configPath,
		cfg:        cfg,
		configured: configured,
	}
	if configured {
		if err := r.start(cfg); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *Runtime) Configured() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.configured
}

func (r *Runtime) ApplySetup(ctx context.Context, cfg *config.Config) error {
	if err := config.ValidateSetup(cfg); err != nil {
		return err
	}

	r.mu.Lock()
	if r.configured {
		r.mu.Unlock()
		return ErrAlreadyConfigured
	}
	r.mu.Unlock()

	// Parse all runtime settings before persisting the one-time setup.
	if _, err := buildWorkers(cfg, r.db, pikpak.NewManager(cfg.PikPak.Accounts, cfg.PikPak.SessionDir, cfg.PikPak.MaxJobsPerAccount), aria2.NewPool(cfg.Aria2.Instances)); err != nil {
		return err
	}
	if err := config.Save(r.configPath, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.configured {
		return ErrAlreadyConfigured
	}
	if err := r.startLocked(cfg); err != nil {
		return err
	}
	r.configured = true
	r.cfg = cfg
	return nil
}

func (r *Runtime) AccountNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.configured || r.cfg == nil {
		return nil
	}
	out := make([]string, 0, len(r.cfg.PikPak.Accounts))
	for _, account := range r.cfg.PikPak.Accounts {
		out = append(out, account.Name)
	}
	return out
}

func (r *Runtime) RefreshAccount(ctx context.Context, name string) (pikpak.AccountSnapshot, error) {
	r.mu.RLock()
	provider := r.provider
	r.mu.RUnlock()
	if provider == nil {
		return pikpak.AccountSnapshot{Name: name, State: "NOT_CONFIGURED"}, errors.New("PikPak is not configured")
	}
	return provider.RefreshAccount(ctx, name)
}

func (r *Runtime) Aria2Snapshots(ctx context.Context) []aria2.InstanceSnapshot {
	r.mu.RLock()
	pool := r.ariaPool
	r.mu.RUnlock()
	if pool == nil {
		return nil
	}
	return pool.Snapshots(ctx)
}

func (r *Runtime) start(cfg *config.Config) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.startLocked(cfg)
}

func (r *Runtime) startLocked(cfg *config.Config) error {
	provider := pikpak.NewManager(cfg.PikPak.Accounts, cfg.PikPak.SessionDir, cfg.PikPak.MaxJobsPerAccount)
	pool := aria2.NewPool(cfg.Aria2.Instances)
	workers, err := buildWorkers(cfg, r.db, provider, pool)
	if err != nil {
		return err
	}

	if r.cancel != nil {
		r.cancel()
	}
	workerCtx, cancel := context.WithCancel(r.parent)
	r.cancel = cancel
	r.provider = provider
	r.ariaPool = pool
	r.cfg = cfg

	go workers.pikpak.Run(workerCtx)
	go workers.aria2.Run(workerCtx)
	go workers.finalizer.Run(workerCtx)
	return nil
}

type workerSet struct {
	pikpak    *worker.Worker
	aria2     *worker.Aria2Worker
	finalizer *worker.Finalizer
}

func buildWorkers(cfg *config.Config, db *store.SQLite, provider pikpak.Provider, pool aria2.Backend) (workerSet, error) {
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

	accountNames := make([]string, 0, len(cfg.PikPak.Accounts))
	for _, account := range cfg.PikPak.Accounts {
		accountNames = append(accountNames, account.Name)
	}

	return workerSet{
		pikpak: worker.New(db, provider, worker.Options{
			AccountNames:            accountNames,
			WorkerInterval:          workerInterval,
			QuotaRefresh:            quotaRefresh,
			StatusInterval:          pikpakStatus,
			RetryInterval:           retryInterval,
			MaxRetry:                cfg.Scheduler.MaxRetry,
			MinFreeSpace:            minFreeSpace,
			AccountFailureThreshold: cfg.Scheduler.AccountFailureThreshold,
			AccountCooldown:         accountCooldown,
		}),
		aria2: worker.NewAria2(db, provider, pool, worker.Aria2Options{
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
