package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOptionalMissingUsesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg, configured, err := LoadOptional(path)
	if err != nil {
		t.Fatal(err)
	}
	if configured {
		t.Fatal("missing config must not be configured")
	}
	if cfg.Server.Listen != "0.0.0.0:8080" || cfg.Database.Path != "/data/pikpak-bridge.db" {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
}

func TestSaveRoundTripAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := Default()
	cfg.PikPak.Accounts = []PikPakAccount{{
		ID: "pp01", Name: "主账号", Username: "user", Password: "secret", MaxJobs: 2,
	}}
	cfg.Aria2.Instances = []Aria2Instance{{
		ID: "nas", Name: "NAS", URL: "http://aria2:6800/jsonrpc",
	}}
	cfg.Targets = []DownloadTarget{{
		ID: "movies", Name: "电影", Aria2InstanceID: "nas", Dir: "/downloads/movies", Default: true,
	}}

	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("want 0600, got %o", info.Mode().Perm())
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PikPak.Accounts[0].Password != "secret" ||
		loaded.Aria2.Instances[0].ID != "nas" ||
		loaded.Targets[0].Aria2InstanceID != "nas" {
		t.Fatalf("round trip failed: %#v", loaded)
	}
}

func TestValidateRejectsUnknownTargetInstance(t *testing.T) {
	cfg := Default()
	cfg.PikPak.Accounts = []PikPakAccount{{
		ID: "pp01", Name: "主账号", Username: "user", Password: "secret", MaxJobs: 2,
	}}
	cfg.Aria2.Instances = []Aria2Instance{{
		ID: "nas", Name: "NAS", URL: "http://aria2:6800/jsonrpc",
	}}
	cfg.Targets = []DownloadTarget{{
		ID: "movies", Name: "电影", Aria2InstanceID: "missing", Dir: "/movies",
	}}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected unknown aria2 instance validation error")
	}
}


func TestValidateRejectsEnabledTargetOnDisabledInstance(t *testing.T) {
	disabled := false
	cfg := Default()
	cfg.PikPak.Accounts = []PikPakAccount{{
		ID: "pp01", Name: "主账号", Username: "user", Password: "secret", MaxJobs: 2,
	}}
	cfg.Aria2.Instances = []Aria2Instance{{
		ID: "nas", Name: "NAS", URL: "http://aria2:6800/jsonrpc", Enabled: &disabled,
	}}
	cfg.Targets = []DownloadTarget{{
		ID: "movies", Name: "电影", Aria2InstanceID: "nas", Dir: "/movies",
	}}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected disabled aria2 instance validation error")
	}
}
