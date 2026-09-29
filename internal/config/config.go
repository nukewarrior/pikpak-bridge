package config

import (
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
	Token  string `yaml:"token"`
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
	Secret    string  `yaml:"secret"`
	Dir       string  `yaml:"dir"`
	MaxActive int     `yaml:"max_active"`
	Weight    float64 `yaml:"weight"`
	Enabled   *bool   `yaml:"enabled,omitempty"`
}

type SchedulerConfig struct {
	Aria2Affinity          string `yaml:"aria2_affinity"`
	WorkerInterval         string `yaml:"worker_interval"`
	RetryInterval          string `yaml:"retry_interval"`
	MaxRetry               int    `yaml:"max_retry"`
	AccountFailureThreshold int   `yaml:"account_failure_threshold"`
	AccountCooldown        string `yaml:"account_cooldown"`
}

type CleanupConfig struct {
	Enabled    bool   `yaml:"enabled"`
	Permanent  bool   `yaml:"permanent"`
	VerifySize bool   `yaml:"verify_size"`
	Delay      string `yaml:"delay"`
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	raw = []byte(os.ExpandEnv(string(raw)))

	cfg := &Config{}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, err
	}
	applyDefaults(cfg)
	if err := validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Server.Listen == "" {
		cfg.Server.Listen = "0.0.0.0:8080"
	}
	if cfg.Database.Path == "" {
		cfg.Database.Path = "/data/pikpak-bridge.db"
	}
	if cfg.PikPak.SessionDir == "" {
		cfg.PikPak.SessionDir = "/data/sessions"
	}
	if cfg.PikPak.QuotaRefresh == "" {
		cfg.PikPak.QuotaRefresh = "5m"
	}
	if cfg.PikPak.StatusInterval == "" {
		cfg.PikPak.StatusInterval = "10s"
	}
	if cfg.PikPak.MinFreeSpace == "" {
		cfg.PikPak.MinFreeSpace = "2GB"
	}
	if cfg.Aria2.StatusInterval == "" {
		cfg.Aria2.StatusInterval = "5s"
	}
	if cfg.Cleanup.Delay == "" {
		cfg.Cleanup.Delay = "60s"
	}
	if cfg.Scheduler.Aria2Affinity == "" {
		cfg.Scheduler.Aria2Affinity = "task"
	}
	if cfg.Scheduler.WorkerInterval == "" {
		cfg.Scheduler.WorkerInterval = "2s"
	}
	if cfg.Scheduler.RetryInterval == "" {
		cfg.Scheduler.RetryInterval = "30s"
	}
	if cfg.Scheduler.MaxRetry == 0 {
		cfg.Scheduler.MaxRetry = 10
	}
	if cfg.Scheduler.AccountFailureThreshold == 0 {
		cfg.Scheduler.AccountFailureThreshold = 3
	}
	if cfg.Scheduler.AccountCooldown == "" {
		cfg.Scheduler.AccountCooldown = "30m"
	}
	if cfg.PikPak.MaxJobsPerAccount == 0 {
		cfg.PikPak.MaxJobsPerAccount = 2
	}
}

func validate(cfg *Config) error {
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
