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

// BatchBackend is optional so existing single-file backends remain compatible.
// Results have the same order as the requested files, including partial failures.
type AddRequest struct {
	URI          string
	GID          string
	RelativePath string
	Overwrite    bool
}
type AddResult struct {
	GID string
	Err error
}
type StatusResult struct {
	Status Status
	Err    error
}
type BatchBackend interface {
	AddBatch(context.Context, string, string, []AddRequest) ([]AddResult, error)
	TellStatusBatch(context.Context, string, []string) ([]StatusResult, error)
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
	return instance.client.AddURIWithOptions(ctx, uri, addOptions(dir, out, gid, overwrite))
}

// Options are identical for single-file and batch submissions.
func addOptions(dir, out, gid string, overwrite bool) map[string]string {
	options := map[string]string{
		"gid": gid, "out": out, "continue": "true",
		"auto-file-renaming": "false", "max-tries": "1",
		"remote-time": "false",
	}
	if overwrite {
		options["continue"] = "false"
		options["allow-overwrite"] = "true"
	}
	if dir != "" { options["dir"] = dir }
	return options
}

func (r *Registry) AddBatch(ctx context.Context, instanceID, baseDir string, requests []AddRequest) ([]AddResult, error) {
	instance, err := r.enabled(instanceID)
	if err != nil { return nil, err }
	results := make([]AddResult, len(requests))
	calls := make([]methodCall, 0, len(requests))
	indexes := make([]int, 0, len(requests))
	for i, request := range requests {
		dir, out, err := destination(baseDir, request.RelativePath)
		if err != nil {
			results[i].Err = err
			continue
		}
		calls = append(calls, methodCall{MethodName: "aria2.addUri", Params: []any{
			[]string{request.URI}, addOptions(dir, out, request.GID, request.Overwrite),
		}})
		indexes = append(indexes, i)
	}
	raw, err := instance.client.multiCall(ctx, calls)
	if err != nil { return nil, err }
	for i, response := range raw {
		index := indexes[i]
		results[index].Err = parseMultiResult(response, &results[index].GID)
	}
	return results, nil
}

func (r *Registry) TellStatusBatch(ctx context.Context, instanceID string, gids []string) ([]StatusResult, error) {
	instance, err := r.enabled(instanceID)
	if err != nil { return nil, err }
	calls := make([]methodCall, 0, len(gids))
	for _, gid := range gids {
		calls = append(calls, methodCall{MethodName: "aria2.tellStatus", Params: []any{gid}})
	}
	raw, err := instance.client.multiCall(ctx, calls)
	if err != nil { return nil, err }
	results := make([]StatusResult, len(raw))
	for i, response := range raw {
		results[i].Err = parseMultiResult(response, &results[i].Status)
	}
	return results, nil
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
