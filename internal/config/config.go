package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server    ServerConfig     `yaml:"server"`
	Database  DatabaseConfig   `yaml:"database"`
	PikPak    PikPakConfig     `yaml:"pikpak"`
	Aria2     Aria2Config      `yaml:"aria2"`
	Targets   []DownloadTarget `yaml:"targets"`
	Scheduler SchedulerConfig  `yaml:"scheduler"`
	Cleanup   CleanupConfig    `yaml:"cleanup"`
}

type ServerConfig struct {
	Listen string `yaml:"listen"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type PikPakConfig struct {
	SessionDir   string          `yaml:"session_dir"`
	QuotaRefresh string          `yaml:"quota_refresh"`
	StatusInterval string        `yaml:"status_interval"`
	MinFreeSpace string          `yaml:"min_free_space"`
	Accounts     []PikPakAccount `yaml:"accounts"`
}

type PikPakAccount struct {
	ID       string `yaml:"id"`
	Name     string `yaml:"name"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	MaxJobs  int    `yaml:"max_jobs"`
	Enabled  *bool  `yaml:"enabled,omitempty"`
}

type Aria2Config struct {
	StatusInterval string          `yaml:"status_interval"`
	Instances      []Aria2Instance `yaml:"instances"`
}

type Aria2Instance struct {
	ID        string `yaml:"id"`
	Name      string `yaml:"name"`
	URL       string `yaml:"url"`
	Secret    string `yaml:"secret,omitempty"`
	Enabled   *bool  `yaml:"enabled,omitempty"`
}

type DownloadTarget struct {
	ID              string `yaml:"id" json:"id"`
	Name            string `yaml:"name" json:"name"`
	Aria2InstanceID string `yaml:"aria2_instance" json:"aria2_instance"`
	Dir             string `yaml:"dir" json:"dir"`
	Default         bool   `yaml:"default,omitempty" json:"default,omitempty"`
	Enabled         *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty"`
}

type SchedulerConfig struct {
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
			SessionDir:    "/data/sessions",
			QuotaRefresh:  "5m",
			StatusInterval: "10s",
			MinFreeSpace:  "2GB",
		},
		Aria2: Aria2Config{
			StatusInterval: "5s",
		},
		Scheduler: SchedulerConfig{
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
	if strings.TrimSpace(cfg.Server.Listen) == "" {
		return errors.New("server.listen is required")
	}
	if strings.TrimSpace(cfg.Database.Path) == "" {
		return errors.New("database.path is required")
	}
	if cfg.Cleanup.Enabled && !cfg.Cleanup.Permanent {
		return errors.New("cleanup.enabled requires cleanup.permanent=true")
	}
	if len(cfg.PikPak.Accounts) == 0 {
		return errors.New("at least one PikPak account is required")
	}
	if len(cfg.Aria2.Instances) == 0 {
		return errors.New("at least one aria2 instance is required")
	}
	if len(cfg.Targets) == 0 {
		return errors.New("at least one download target is required")
	}

	accountIDs := make(map[string]struct{}, len(cfg.PikPak.Accounts))
	for i, account := range cfg.PikPak.Accounts {
		if strings.TrimSpace(account.ID) == "" || strings.TrimSpace(account.Name) == "" {
			return fmt.Errorf("pikpak.accounts[%d] requires id and name", i)
		}
		if strings.TrimSpace(account.Username) == "" || account.Password == "" {
			return fmt.Errorf("pikpak.accounts[%d] requires username and password", i)
		}
		if account.MaxJobs <= 0 {
			return fmt.Errorf("pikpak.accounts[%d].max_jobs must be greater than zero", i)
		}
		if _, exists := accountIDs[account.ID]; exists {
			return fmt.Errorf("duplicate PikPak account id %q", account.ID)
		}
		accountIDs[account.ID] = struct{}{}
	}

	instanceIDs := make(map[string]struct{}, len(cfg.Aria2.Instances))
	instanceEnabled := make(map[string]bool, len(cfg.Aria2.Instances))
	for i, instance := range cfg.Aria2.Instances {
		if strings.TrimSpace(instance.ID) == "" || strings.TrimSpace(instance.Name) == "" || strings.TrimSpace(instance.URL) == "" {
			return fmt.Errorf("aria2.instances[%d] requires id, name and url", i)
		}
		if _, exists := instanceIDs[instance.ID]; exists {
			return fmt.Errorf("duplicate aria2 instance id %q", instance.ID)
		}
		instanceIDs[instance.ID] = struct{}{}
		instanceEnabled[instance.ID] = Enabled(instance.Enabled)
	}

	targetIDs := make(map[string]struct{}, len(cfg.Targets))
	defaults := 0
	enabledTargets := 0
	for i, target := range cfg.Targets {
		if strings.TrimSpace(target.ID) == "" || strings.TrimSpace(target.Name) == "" {
			return fmt.Errorf("targets[%d] requires id and name", i)
		}
		if strings.TrimSpace(target.Aria2InstanceID) == "" || strings.TrimSpace(target.Dir) == "" {
			return fmt.Errorf("targets[%d] requires aria2_instance and dir", i)
		}
		if _, exists := instanceIDs[target.Aria2InstanceID]; !exists {
			return fmt.Errorf("targets[%d] references unknown aria2 instance %q", i, target.Aria2InstanceID)
		}
		if _, exists := targetIDs[target.ID]; exists {
			return fmt.Errorf("duplicate download target id %q", target.ID)
		}
		targetIDs[target.ID] = struct{}{}
		if Enabled(target.Enabled) {
			if !instanceEnabled[target.Aria2InstanceID] {
				return fmt.Errorf("enabled target %q references disabled aria2 instance %q", target.ID, target.Aria2InstanceID)
			}
			enabledTargets++
			if target.Default {
				defaults++
			}
		} else if target.Default {
			return fmt.Errorf("disabled target %q cannot be default", target.ID)
		}
	}
	if enabledTargets == 0 {
		return errors.New("at least one download target must be enabled")
	}
	if defaults > 1 {
		return errors.New("only one download target may be default")
	}
	return nil
}

func Enabled(value *bool) bool {
	return value == nil || *value
}
