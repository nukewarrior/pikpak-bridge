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
	cfg.PikPak.Accounts = []PikPakAccount{{Name: "pp01", Username: "user", Password: "secret"}}
	cfg.Aria2.Instances = []Aria2Instance{{
		Name: "nas", URL: "http://aria2:6800/jsonrpc", Dir: "/downloads", MaxActive: 4, Weight: 10,
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
	if loaded.PikPak.Accounts[0].Password != "secret" || loaded.Aria2.Instances[0].Name != "nas" {
		t.Fatalf("round trip failed: %#v", loaded)
	}
}
