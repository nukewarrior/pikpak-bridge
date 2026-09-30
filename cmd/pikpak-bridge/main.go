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
		slog.Error("加载配置失败", "error", err)
		os.Exit(1)
	}

	db, err := store.Open(cfg.Database.Path)
	if err != nil {
		slog.Error("打开数据库失败", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	runtime, err := app.NewRuntime(ctx, db, cfgPath, cfg, configured)
	if err != nil {
		slog.Error("启动运行时失败", "error", err)
		os.Exit(1)
	}

	if !configured {
		slog.Info("首次启动：资源尚未配置", "url", "http://0.0.0.0:8080/")
	}

	api := httpapi.New(db, httpapi.WithRuntime(runtime))
	server := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("pikpak-bridge 已启动并监听", "addr", cfg.Server.Listen)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		slog.Error("HTTP 服务异常停止", "error", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("服务优雅关闭失败", "error", err)
	}
}
