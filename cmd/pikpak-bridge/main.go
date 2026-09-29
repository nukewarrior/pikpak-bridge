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

	if len(cfg.PikPak.Accounts) > 0 {
		pikpakWorker, err := buildPikPakWorker(cfg, db)
		if err != nil {
			slog.Error("configure PikPak worker", "error", err)
			os.Exit(1)
		}
		go pikpakWorker.Run(ctx)
	} else {
		slog.Warn("no PikPak accounts configured; queued tasks will not be processed")
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

func buildPikPakWorker(cfg *config.Config, db *store.SQLite) (*worker.Worker, error) {
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

	provider := pikpak.NewManager(
		cfg.PikPak.Accounts,
		cfg.PikPak.SessionDir,
		cfg.PikPak.MaxJobsPerAccount,
	)
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
