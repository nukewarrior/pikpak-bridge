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

	"github.com/nukewarrior/pikpak-bridge/internal/app"
	"github.com/nukewarrior/pikpak-bridge/internal/config"
	"github.com/nukewarrior/pikpak-bridge/internal/httpapi"
	"github.com/nukewarrior/pikpak-bridge/internal/store"
)

func main() {
	cfgPath := os.Getenv("PIKPAK_BRIDGE_CONFIG")
	if cfgPath == "" {
		cfgPath = "/data/config.yaml"
	}

	cfg, configured, err := config.LoadOptional(cfgPath)
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

	runtime, err := app.NewRuntime(ctx, db, cfgPath, cfg, configured)
	if err != nil {
		slog.Error("start runtime", "error", err)
		os.Exit(1)
	}

	if !configured {
		slog.Info("first-run setup required", "url", "http://0.0.0.0:8080/")
	}

	api := httpapi.New(db, httpapi.WithRuntime(runtime))
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
