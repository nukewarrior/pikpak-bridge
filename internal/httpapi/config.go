package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/nukewarrior/pikpak-bridge/internal/config"
)

type configResponse struct {
	PikPakAccounts []configPikPakAccountView `json:"pikpak_accounts"`
	Aria2Instances []configAria2InstanceView `json:"aria2_instances"`
	Targets        []configTargetView        `json:"targets"`
}

type configPikPakAccountView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Username    string `json:"username"`
	MaxJobs     int    `json:"max_jobs"`
	Enabled     bool   `json:"enabled"`
	PasswordSet bool   `json:"password_set"`
}

type configAria2InstanceView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Enabled   bool   `json:"enabled"`
	SecretSet bool   `json:"secret_set"`
}

type configTargetView struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Aria2InstanceID string `json:"aria2_instance"`
	Dir             string `json:"dir"`
	LocalDir        string `json:"local_dir"`
	Default         bool   `json:"default"`
	Enabled         bool   `json:"enabled"`
}

func (s *Server) getConfig(w http.ResponseWriter, _ *http.Request) {
	if s.runtime == nil || !s.runtime.Configured() {
		writeError(w, http.StatusConflict, "setup has not been completed")
		return
	}
	cfg := s.runtime.CurrentConfig()
	if cfg == nil {
		writeError(w, http.StatusServiceUnavailable, "runtime configuration is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, configView(cfg))
}

func (s *Server) updateConfig(w http.ResponseWriter, r *http.Request) {
	if s.runtime == nil || !s.runtime.Configured() {
		writeError(w, http.StatusConflict, "setup has not been completed")
		return
	}

	var req setupRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(req.PikPakAccounts) > 50 || len(req.Aria2Instances) > 50 || len(req.Targets) > 100 {
		writeError(w, http.StatusBadRequest, "too many accounts, aria2 instances or targets")
		return
	}

	current := s.runtime.CurrentConfig()
	if current == nil {
		writeError(w, http.StatusServiceUnavailable, "runtime configuration is unavailable")
		return
	}
	next := mergeManagedConfig(current, req)
	if err := config.Validate(next); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	accountRefs, aria2Refs, err := s.store.ActiveResourceReferences(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "check active task references")
		return
	}
	if err := validateActiveResources(next, accountRefs, aria2Refs); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	if err := s.runtime.ApplyConfig(r.Context(), next); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, configView(next))
}

func configView(cfg *config.Config) configResponse {
	out := configResponse{
		PikPakAccounts: make([]configPikPakAccountView, 0, len(cfg.PikPak.Accounts)),
		Aria2Instances: make([]configAria2InstanceView, 0, len(cfg.Aria2.Instances)),
		Targets:        make([]configTargetView, 0, len(cfg.Targets)),
	}
	for _, account := range cfg.PikPak.Accounts {
		out.PikPakAccounts = append(out.PikPakAccounts, configPikPakAccountView{
			ID:          account.ID,
			Name:        account.Name,
			Username:    account.Username,
			MaxJobs:     account.MaxJobs,
			Enabled:     config.Enabled(account.Enabled),
			PasswordSet: account.Password != "",
		})
	}
	for _, instance := range cfg.Aria2.Instances {
		out.Aria2Instances = append(out.Aria2Instances, configAria2InstanceView{
			ID:        instance.ID,
			Name:      instance.Name,
			URL:       instance.URL,
			Enabled:   config.Enabled(instance.Enabled),
			SecretSet: instance.Secret != "",
		})
	}
	for _, target := range cfg.Targets {
		out.Targets = append(out.Targets, configTargetView{
			ID:              target.ID,
			Name:            target.Name,
			Aria2InstanceID: target.Aria2InstanceID,
			Dir:             target.Dir,
            LocalDir:        target.LocalDir,
			Default:         target.Default,
			Enabled:         config.Enabled(target.Enabled),
		})
	}
	return out
}

func mergeManagedConfig(current *config.Config, req setupRequest) *config.Config {
	next := *current
	accountByID := make(map[string]config.PikPakAccount, len(current.PikPak.Accounts))
	for _, account := range current.PikPak.Accounts {
		accountByID[account.ID] = account
	}
	instanceByID := make(map[string]config.Aria2Instance, len(current.Aria2.Instances))
	for _, instance := range current.Aria2.Instances {
		instanceByID[instance.ID] = instance
	}
	targetByID := make(map[string]config.DownloadTarget, len(current.Targets))
	for _, target := range current.Targets {
		targetByID[target.ID] = target
	}

	next.PikPak.Accounts = make([]config.PikPakAccount, 0, len(req.PikPakAccounts))
	for _, item := range req.PikPakAccounts {
		id := strings.TrimSpace(item.ID)
		old, exists := accountByID[id]
		password := item.Password
		if password == "" && exists {
			password = old.Password
		}
		maxJobs := item.MaxJobs
		if maxJobs == 0 && exists {
			maxJobs = old.MaxJobs
		}
		if maxJobs == 0 {
			maxJobs = 2
		}
		next.PikPak.Accounts = append(next.PikPak.Accounts, config.PikPakAccount{
			ID:       id,
			Name:     strings.TrimSpace(item.Name),
			Username: strings.TrimSpace(item.Username),
			Password: password,
			MaxJobs:  maxJobs,
			Enabled:  mergeEnabled(item.Enabled, old.Enabled, exists),
		})
	}

	next.Aria2.Instances = make([]config.Aria2Instance, 0, len(req.Aria2Instances))
	for _, item := range req.Aria2Instances {
		id := strings.TrimSpace(item.ID)
		old, exists := instanceByID[id]
		secret := item.Secret
		if secret == "" && exists {
			secret = old.Secret
		}
		next.Aria2.Instances = append(next.Aria2.Instances, config.Aria2Instance{
			ID:      id,
			Name:    strings.TrimSpace(item.Name),
			URL:     strings.TrimSpace(item.URL),
			Secret:  secret,
			Enabled: mergeEnabled(item.Enabled, old.Enabled, exists),
		})
	}

	next.Targets = make([]config.DownloadTarget, 0, len(req.Targets))
	for _, item := range req.Targets {
		id := strings.TrimSpace(item.ID)
		old, exists := targetByID[id]
		next.Targets = append(next.Targets, config.DownloadTarget{
			ID:              id,
			Name:            strings.TrimSpace(item.Name),
			Aria2InstanceID: strings.TrimSpace(item.Aria2InstanceID),
			Dir:             strings.TrimSpace(item.Dir),
            LocalDir:        strings.TrimSpace(item.LocalDir),
			Default:         item.Default,
			Enabled:         mergeEnabled(item.Enabled, old.Enabled, exists),
		})
	}
	return &next
}

func mergeEnabled(requested, existing *bool, exists bool) *bool {
	if requested != nil {
		value := *requested
		return &value
	}
	if exists && existing != nil {
		value := *existing
		return &value
	}
	return nil
}

func validateActiveResources(cfg *config.Config, accountRefs, aria2Refs []string) error {
	accounts := make(map[string]bool, len(cfg.PikPak.Accounts))
	for _, account := range cfg.PikPak.Accounts {
		accounts[account.ID] = config.Enabled(account.Enabled)
	}
	for _, id := range accountRefs {
		enabled, exists := accounts[id]
		if !exists {
			return fmt.Errorf("PikPak account %q is still referenced by an active task and cannot be removed", id)
		}
		if !enabled {
			return fmt.Errorf("PikPak account %q is still referenced by an active task and cannot be disabled", id)
		}
	}

	instances := make(map[string]bool, len(cfg.Aria2.Instances))
	for _, instance := range cfg.Aria2.Instances {
		instances[instance.ID] = config.Enabled(instance.Enabled)
	}
	for _, id := range aria2Refs {
		enabled, exists := instances[id]
		if !exists {
			return fmt.Errorf("aria2 instance %q is still referenced by an active task and cannot be removed", id)
		}
		if !enabled {
			return fmt.Errorf("aria2 instance %q is still referenced by an active task and cannot be disabled", id)
		}
	}
	return nil
}
