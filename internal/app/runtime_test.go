package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/nukewarrior/pikpak-bridge/internal/config"
	"github.com/nukewarrior/pikpak-bridge/internal/store"
)

func TestRuntimeApplyConfigPersistsAndHotReloads(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	runtime, err := NewRuntime(ctx, db, configPath, config.Default(), false)
	if err != nil {
		t.Fatal(err)
	}

	cfg := validRuntimeConfig()
	if err := runtime.ApplySetup(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if !runtime.Configured() {
		t.Fatal("runtime should be configured after setup")
	}

	next := runtime.CurrentConfig()
	next.PikPak.Accounts[0].Name = "Renamed Account"
	next.Targets[0].Dir = "/downloads/updated"
	if err := runtime.ApplyConfig(ctx, next); err != nil {
		t.Fatal(err)
	}

	target, err := runtime.ResolveTarget("movies")
	if err != nil {
		t.Fatal(err)
	}
	if target.Dir != "/downloads/updated" {
		t.Fatalf("want hot-reloaded target dir, got %q", target.Dir)
	}
	if got := runtime.CurrentConfig().PikPak.Accounts[0].Name; got != "Renamed Account" {
		t.Fatalf("want hot-reloaded account name, got %q", got)
	}

	persisted, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Targets[0].Dir != "/downloads/updated" {
		t.Fatalf("want persisted target dir, got %q", persisted.Targets[0].Dir)
	}
	if persisted.PikPak.Accounts[0].Password != "password" {
		t.Fatal("persisted config lost PikPak credentials")
	}
}

func validRuntimeConfig() *config.Config {
	cfg := config.Default()
	cfg.PikPak.Accounts = []config.PikPakAccount{{
		ID: "pp01", Name: "Primary", Username: "user", Password: "password", MaxJobs: 2,
	}}
	cfg.Aria2.Instances = []config.Aria2Instance{{
		ID: "nas", Name: "NAS", URL: "http://127.0.0.1:6800/jsonrpc",
	}}
	cfg.Targets = []config.DownloadTarget{{
		ID: "movies", Name: "Movies", Aria2InstanceID: "nas", Dir: "/downloads/movies", Default: true,
	}}
	return cfg
}
