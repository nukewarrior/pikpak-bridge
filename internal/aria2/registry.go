package aria2

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/nukewarrior/pikpak-bridge/internal/config"
)

type InstanceSnapshot struct {
	ID        string
	Name      string
	Enabled   bool
	Healthy   bool
	Active    int
	Waiting   int
	Error     string
}

type Backend interface {
	Snapshots(ctx context.Context) []InstanceSnapshot
	Snapshot(ctx context.Context, instanceID string) (InstanceSnapshot, error)
	Add(ctx context.Context, instanceID, baseDir, uri, gid, relativePath string, overwrite bool) (string, error)
	TellStatus(ctx context.Context, instanceID, gid string) (Status, error)
	Remove(ctx context.Context, instanceID, gid string) error
	Forget(ctx context.Context, instanceID, gid string) error
}

type registryInstance struct {
	id        string
	name      string
	enabled   bool
	client    *Client
}

type Registry struct {
	instances map[string]*registryInstance
	order     []string
}

func NewRegistry(configs []config.Aria2Instance) *Registry {
	registry := &Registry{
		instances: make(map[string]*registryInstance, len(configs)),
		order:     make([]string, 0, len(configs)),
	}
	for _, cfg := range configs {
		instance := &registryInstance{
			id:        cfg.ID,
			name:      cfg.Name,
			enabled:   config.Enabled(cfg.Enabled),
			client:    New(cfg.URL, cfg.Secret),
		}
		registry.instances[cfg.ID] = instance
		registry.order = append(registry.order, cfg.ID)
	}
	return registry
}

func (r *Registry) Snapshots(ctx context.Context) []InstanceSnapshot {
	out := make([]InstanceSnapshot, len(r.order))
	var wg sync.WaitGroup
	for i, id := range r.order {
		i, id := i, id
		wg.Add(1)
		go func() {
			defer wg.Done()
			snapshot, err := r.Snapshot(ctx, id)
			if err != nil {
				snapshot.Error = err.Error()
			}
			out[i] = snapshot
		}()
	}
	wg.Wait()
	return out
}

func (r *Registry) Snapshot(ctx context.Context, instanceID string) (InstanceSnapshot, error) {
	instance, err := r.lookup(instanceID)
	if err != nil {
		return InstanceSnapshot{ID: instanceID}, err
	}
	snapshot := InstanceSnapshot{
		ID:        instance.id,
		Name:      instance.name,
		Enabled:   instance.enabled,
	}
	if !instance.enabled {
		return snapshot, nil
	}
	stat, err := instance.client.GetGlobalStat(ctx)
	if err != nil {
		snapshot.Error = err.Error()
		return snapshot, err
	}
	snapshot.Active = parseCount(stat.NumActive)
	snapshot.Waiting = parseCount(stat.NumWaiting)
	snapshot.Healthy = true
	return snapshot, nil
}

func (r *Registry) Add(ctx context.Context, instanceID, baseDir, uri, gid, relativePath string, overwrite bool) (string, error) {
	instance, err := r.enabled(instanceID)
	if err != nil {
		return "", err
	}
	dir, out, err := destination(baseDir, relativePath)
	if err != nil {
		return "", err
	}
	options := map[string]string{
		"gid":                gid,
		"out":                out,
		"continue":           "true",
		"auto-file-renaming": "false",
		"max-tries":          "1",
	}
    if overwrite {
        // Confirmed repeat downloads go straight to the final aria2 directory.
        options["continue"] = "false"
        options["allow-overwrite"] = "true"
    }
    if dir != "" {
        options["dir"] = dir
    }
    return instance.client.AddURIWithOptions(ctx, uri, options)
}

func (r *Registry) TellStatus(ctx context.Context, instanceID, gid string) (Status, error) {
	instance, err := r.enabled(instanceID)
	if err != nil {
		return Status{}, err
	}
	return instance.client.TellStatus(ctx, gid)
}

func (r *Registry) Remove(ctx context.Context, instanceID, gid string) error {
	instance, err := r.enabled(instanceID)
	if err != nil {
		return err
	}
	return instance.client.Remove(ctx, gid)
}

func (r *Registry) Forget(ctx context.Context, instanceID, gid string) error {
	instance, err := r.enabled(instanceID)
	if err != nil {
		return err
	}
	return instance.client.RemoveDownloadResult(ctx, gid)
}

func (r *Registry) lookup(id string) (*registryInstance, error) {
	instance, ok := r.instances[id]
	if !ok {
		return nil, fmt.Errorf("aria2 instance %q not configured", id)
	}
	return instance, nil
}

func (r *Registry) enabled(id string) (*registryInstance, error) {
	instance, err := r.lookup(id)
	if err != nil {
		return nil, err
	}
	if !instance.enabled {
		return nil, fmt.Errorf("aria2 instance %q is disabled", id)
	}
	return instance, nil
}

func destination(base, relative string) (string, string, error) {
	if strings.TrimSpace(base) == "" {
		return "", "", fmt.Errorf("empty download target directory")
	}
	if strings.TrimSpace(relative) == "" {
		return "", "", fmt.Errorf("empty relative download path")
	}
	if strings.HasPrefix(relative, "/") || strings.HasPrefix(relative, "\\") {
		return "", "", fmt.Errorf("absolute download path is not allowed: %q", relative)
	}
	normalized := strings.ReplaceAll(relative, "\\", "/")
	for _, segment := range strings.Split(normalized, "/") {
		if segment == ".." {
			return "", "", fmt.Errorf("parent path segment is not allowed: %q", relative)
		}
	}
	clean := path.Clean(normalized)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", "", fmt.Errorf("invalid relative download path: %q", relative)
	}

	dirPart := path.Dir(clean)
	if dirPart == "." {
		dirPart = ""
	}
	output := path.Base(clean)
	if strings.Contains(base, "\\") && !strings.Contains(base, "/") {
		dir := strings.TrimRight(base, "\\/")
		if dirPart != "" {
			dir += "\\" + strings.ReplaceAll(dirPart, "/", "\\")
		}
		return dir, output, nil
	}
	if dirPart == "" {
		return strings.TrimRight(base, "/"), output, nil
	}
	return path.Join(base, dirPart), output, nil
}

func parseCount(value string) int {
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
