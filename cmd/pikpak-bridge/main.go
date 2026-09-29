package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nukewarrior/pikpak-bridge/internal/aria2"
	"github.com/nukewarrior/pikpak-bridge/internal/config"
	"github.com/nukewarrior/pikpak-bridge/internal/httpapi"
	"github.com/nukewarrior/pikpak-bridge/internal/pikpak"
	"github.com/nukewarrior/pikpak-bridge/internal/store"
	"github.com/nukewarrior/pikpak-bridge/internal/worker"
)

func main() {
	cfgPath := os.Getenv("PIKPAK_BRIDGE_CONFIG")
	if cfgPath == "" {
		cfgPath = "config.yaml"
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	db, err := store.Open(cfg.Database.Path)
	if err != nil {
		slog.Error("open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	provider := pikpak.NewManager(
		cfg.PikPak.Accounts,
		cfg.PikPak.SessionDir,
		cfg.PikPak.MaxJobsPerAccount,
	)

	if len(cfg.PikPak.Accounts) > 0 {
		pikpakWorker, err := buildPikPakWorker(cfg, db, provider)
		if err != nil {
			slog.Error("configure PikPak worker", "error", err)
			os.Exit(1)
		}
		go pikpakWorker.Run(ctx)
	} else {
		slog.Warn("no PikPak accounts configured; queued tasks will not be processed")
	}

	if len(cfg.Aria2.Instances) > 0 && len(cfg.PikPak.Accounts) > 0 {
		aria2Worker, err := buildAria2Worker(cfg, db, provider)
		if err != nil {
			slog.Error("configure aria2 worker", "error", err)
			os.Exit(1)
		}
		go aria2Worker.Run(ctx)
	} else if len(cfg.Aria2.Instances) == 0 {
		slog.Warn("no aria2 instances configured; completed PikPak tasks will wait")
	}

	api := httpapi.New(db)
	server := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("pikpak-bridge listening", "addr", cfg.Server.Listen)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		slog.Error("http server stopped", "error", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown", "error", err)
	}
}

func buildPikPakWorker(cfg *config.Config, db *store.SQLite, provider pikpak.Provider) (*worker.Worker, error) {
	quotaRefresh, err := time.ParseDuration(cfg.PikPak.QuotaRefresh)
	if err != nil {
		return nil, err
	}
	statusInterval, err := time.ParseDuration(cfg.PikPak.StatusInterval)
	if err != nil {
		return nil, err
	}
	workerInterval, err := time.ParseDuration(cfg.Scheduler.WorkerInterval)
	if err != nil {
		return nil, err
	}
	retryInterval, err := time.ParseDuration(cfg.Scheduler.RetryInterval)
	if err != nil {
		return nil, err
	}
	accountCooldown, err := time.ParseDuration(cfg.Scheduler.AccountCooldown)
	if err != nil {
		return nil, err
	}
	minFreeSpace, err := worker.ParseByteSize(cfg.PikPak.MinFreeSpace)
	if err != nil {
		return nil, err
	}

	accountNames := make([]string, 0, len(cfg.PikPak.Accounts))
	for _, account := range cfg.PikPak.Accounts {
		accountNames = append(accountNames, account.Name)
	}

	return worker.New(db, provider, worker.Options{
		AccountNames:            accountNames,
		WorkerInterval:          workerInterval,
		QuotaRefresh:            quotaRefresh,
		StatusInterval:          statusInterval,
		RetryInterval:           retryInterval,
		MaxRetry:                cfg.Scheduler.MaxRetry,
		MinFreeSpace:            minFreeSpace,
		AccountFailureThreshold: cfg.Scheduler.AccountFailureThreshold,
		AccountCooldown:         accountCooldown,
	}), nil
}

func buildAria2Worker(cfg *config.Config, db *store.SQLite, provider pikpak.Provider) (*worker.Aria2Worker, error) {
	workerInterval, err := time.ParseDuration(cfg.Scheduler.WorkerInterval)
	if err != nil {
		return nil, err
	}
	statusInterval, err := time.ParseDuration(cfg.Aria2.StatusInterval)
	if err != nil {
		return nil, err
	}
	retryInterval, err := time.ParseDuration(cfg.Scheduler.RetryInterval)
	if err != nil {
		return nil, err
	}
	pool := aria2.NewPool(cfg.Aria2.Instances)
	return worker.NewAria2(db, provider, pool, worker.Aria2Options{
		WorkerInterval: workerInterval,
		StatusInterval: statusInterval,
		RetryInterval:  retryInterval,
		MaxRetry:       cfg.Scheduler.MaxRetry,
	}), nil
}
