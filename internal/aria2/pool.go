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
	Name      string
	Enabled   bool
	Healthy   bool
	Active    int
	Waiting   int
	MaxActive int
	Weight    float64
	Error     string
}

type Backend interface {
	Snapshots(ctx context.Context) []InstanceSnapshot
	Add(ctx context.Context, instance, uri, gid, relativePath string) (string, error)
	TellStatus(ctx context.Context, instance, gid string) (Status, error)
	Forget(ctx context.Context, instance, gid string) error
}

type poolInstance struct {
	name      string
	dir       string
	maxActive int
	weight    float64
	enabled   bool
	client    *Client
}

type Pool struct {
	instances map[string]*poolInstance
	order     []string
}

func NewPool(configs []config.Aria2Instance) *Pool {
	pool := &Pool{
		instances: make(map[string]*poolInstance, len(configs)),
		order:     make([]string, 0, len(configs)),
	}
	for _, cfg := range configs {
		enabled := cfg.Enabled == nil || *cfg.Enabled
		weight := cfg.Weight
		if weight <= 0 {
			weight = 1
		}
		instance := &poolInstance{
			name:      cfg.Name,
			dir:       cfg.Dir,
			maxActive: cfg.MaxActive,
			weight:    weight,
			enabled:   enabled,
			client:    New(cfg.URL, cfg.Secret),
		}
		pool.instances[cfg.Name] = instance
		pool.order = append(pool.order, cfg.Name)
	}
	return pool
}

func (p *Pool) Snapshots(ctx context.Context) []InstanceSnapshot {
	out := make([]InstanceSnapshot, len(p.order))
	var wg sync.WaitGroup
	for i, name := range p.order {
		i, name := i, name
		wg.Add(1)
		go func() {
			defer wg.Done()
			instance := p.instances[name]
			snapshot := InstanceSnapshot{
				Name:      instance.name,
				Enabled:   instance.enabled,
				MaxActive: instance.maxActive,
				Weight:    instance.weight,
			}
			if !instance.enabled {
				out[i] = snapshot
				return
			}
			stat, err := instance.client.GetGlobalStat(ctx)
			if err != nil {
				snapshot.Error = err.Error()
				out[i] = snapshot
				return
			}
			snapshot.Active = parseCount(stat.NumActive)
			snapshot.Waiting = parseCount(stat.NumWaiting)
			snapshot.Healthy = true
			out[i] = snapshot
		}()
	}
	wg.Wait()
	return out
}

func (p *Pool) Add(ctx context.Context, instanceName, uri, gid, relativePath string) (string, error) {
	instance, err := p.get(instanceName)
	if err != nil {
		return "", err
	}
	dir, out, err := destination(instance.dir, relativePath)
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
	if dir != "" {
		options["dir"] = dir
	}
	return instance.client.AddURIWithOptions(ctx, uri, options)
}

func (p *Pool) TellStatus(ctx context.Context, instanceName, gid string) (Status, error) {
	instance, err := p.get(instanceName)
	if err != nil {
		return Status{}, err
	}
	return instance.client.TellStatus(ctx, gid)
}

func (p *Pool) Forget(ctx context.Context, instanceName, gid string) error {
	instance, err := p.get(instanceName)
	if err != nil {
		return err
	}
	return instance.client.RemoveDownloadResult(ctx, gid)
}

func (p *Pool) get(name string) (*poolInstance, error) {
	instance, ok := p.instances[name]
	if !ok {
		return nil, fmt.Errorf("aria2 instance %q not configured", name)
	}
	if !instance.enabled {
		return nil, fmt.Errorf("aria2 instance %q is disabled", name)
	}
	return instance, nil
}

func destination(base, relative string) (string, string, error) {
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
