package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Database  DatabaseConfig  `yaml:"database"`
	PikPak    PikPakConfig    `yaml:"pikpak"`
	Aria2     Aria2Config     `yaml:"aria2"`
	Scheduler SchedulerConfig `yaml:"scheduler"`
	Cleanup   CleanupConfig   `yaml:"cleanup"`
}

type ServerConfig struct {
	Listen string `yaml:"listen"`
	Token  string `yaml:"token,omitempty"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type PikPakConfig struct {
	SessionDir        string          `yaml:"session_dir"`
	QuotaRefresh      string          `yaml:"quota_refresh"`
	StatusInterval    string          `yaml:"status_interval"`
	MinFreeSpace      string          `yaml:"min_free_space"`
	MaxJobsPerAccount int             `yaml:"max_jobs_per_account"`
	Accounts          []PikPakAccount `yaml:"accounts"`
}

type PikPakAccount struct {
	Name     string `yaml:"name"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Enabled  *bool  `yaml:"enabled,omitempty"`
}

type Aria2Config struct {
	StatusInterval string          `yaml:"status_interval"`
	Instances      []Aria2Instance `yaml:"instances"`
}

type Aria2Instance struct {
	Name      string  `yaml:"name"`
	URL       string  `yaml:"url"`
	Secret    string  `yaml:"secret,omitempty"`
	Dir       string  `yaml:"dir"`
	MaxActive int     `yaml:"max_active"`
	Weight    float64 `yaml:"weight"`
	Enabled   *bool   `yaml:"enabled,omitempty"`
}

type SchedulerConfig struct {
	Aria2Affinity           string `yaml:"aria2_affinity"`
	WorkerInterval          string `yaml:"worker_interval"`
	RetryInterval           string `yaml:"retry_interval"`
	MaxRetry                int    `yaml:"max_retry"`
	AccountFailureThreshold int    `yaml:"account_failure_threshold"`
	AccountCooldown         string `yaml:"account_cooldown"`
}

type CleanupConfig struct {
	Enabled    bool   `yaml:"enabled"`
	Permanent  bool   `yaml:"permanent"`
	VerifySize bool   `yaml:"verify_size"`
	Delay      string `yaml:"delay"`
}

func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Listen: "0.0.0.0:8080",
		},
		Database: DatabaseConfig{
			Path: "/data/pikpak-bridge.db",
		},
		PikPak: PikPakConfig{
			SessionDir:        "/data/sessions",
			QuotaRefresh:      "5m",
			StatusInterval:    "10s",
			MinFreeSpace:      "2GB",
			MaxJobsPerAccount: 2,
		},
		Aria2: Aria2Config{
			StatusInterval: "5s",
		},
		Scheduler: SchedulerConfig{
			Aria2Affinity:           "task",
			WorkerInterval:          "2s",
			RetryInterval:           "30s",
			MaxRetry:                10,
			AccountFailureThreshold: 3,
			AccountCooldown:         "30m",
		},
		Cleanup: CleanupConfig{
			Enabled:    true,
			Permanent:  true,
			VerifySize: true,
			Delay:      "60s",
		},
	}
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	raw = []byte(os.ExpandEnv(string(raw)))

	cfg := Default()
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, err
	}
	if err := Validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func LoadOptional(path string) (*Config, bool, error) {
	cfg, err := Load(path)
	if err == nil {
		return cfg, true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return Default(), false, nil
	}
	return nil, false, err
}

func Save(path string, cfg *Config) error {
	if err := Validate(cfg); err != nil {
		return err
	}
	body, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	clean := filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(clean), 0o700); err != nil {
		return err
	}
	tmp := clean + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, clean); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func Validate(cfg *Config) error {
	if cfg == nil {
		return errors.New("config is nil")
	}
	if cfg.Server.Listen == "" {
		return errors.New("server.listen is required")
	}
	if cfg.Database.Path == "" {
		return errors.New("database.path is required")
	}
	if cfg.Cleanup.Enabled && !cfg.Cleanup.Permanent {
		return fmt.Errorf("cleanup.enabled requires cleanup.permanent=true")
	}
	if cfg.Scheduler.Aria2Affinity != "task" && cfg.Scheduler.Aria2Affinity != "file" {
		return fmt.Errorf("scheduler.aria2_affinity must be task or file")
	}
	for i, account := range cfg.PikPak.Accounts {
		if account.Name == "" {
			return fmt.Errorf("pikpak.accounts[%d].name is required", i)
		}
	}
	for i, instance := range cfg.Aria2.Instances {
		if instance.Name == "" || instance.URL == "" {
			return fmt.Errorf("aria2.instances[%d] requires name and url", i)
		}
	}
	return nil
}

func ValidateSetup(cfg *Config) error {
	if err := Validate(cfg); err != nil {
		return err
	}
	if len(cfg.PikPak.Accounts) == 0 {
		return errors.New("at least one PikPak account is required")
	}
	if len(cfg.Aria2.Instances) == 0 {
		return errors.New("at least one aria2 instance is required")
	}
	seenAccounts := map[string]bool{}
	for i, account := range cfg.PikPak.Accounts {
		if account.Username == "" || account.Password == "" {
			return fmt.Errorf("pikpak.accounts[%d] requires username and password", i)
		}
		if seenAccounts[account.Name] {
			return fmt.Errorf("duplicate PikPak account name %q", account.Name)
		}
		seenAccounts[account.Name] = true
	}
	seenInstances := map[string]bool{}
	for i, instance := range cfg.Aria2.Instances {
		if instance.Dir == "" {
			return fmt.Errorf("aria2.instances[%d].dir is required", i)
		}
		if instance.MaxActive <= 0 {
			return fmt.Errorf("aria2.instances[%d].max_active must be greater than zero", i)
		}
		if instance.Weight <= 0 {
			return fmt.Errorf("aria2.instances[%d].weight must be greater than zero", i)
		}
		if seenInstances[instance.Name] {
			return fmt.Errorf("duplicate aria2 instance name %q", instance.Name)
		}
		seenInstances[instance.Name] = true
	}
	return nil
}
