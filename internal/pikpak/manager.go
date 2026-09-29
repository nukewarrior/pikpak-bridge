package pikpak

import (
	"context"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/nukewarrior/pikpak-bridge/internal/config"
)

type managedAccount struct {
	name    string
	enabled bool
	client  *Client
}

type Manager struct {
	mu       sync.RWMutex
	accounts map[string]*managedAccount
	lastUsed map[string]time.Time
	maxJobs  int
}

func NewManager(accounts []config.PikPakAccount, sessionDir string, maxJobs int) *Manager {
	if maxJobs <= 0 {
		maxJobs = 2
	}
	m := &Manager{
		accounts: make(map[string]*managedAccount, len(accounts)),
		lastUsed: make(map[string]time.Time, len(accounts)),
		maxJobs:  maxJobs,
	}
	for _, account := range accounts {
		enabled := account.Enabled == nil || *account.Enabled
		enabled = enabled &&
			strings.TrimSpace(account.Username) != "" &&
			strings.TrimSpace(account.Password) != ""
		m.accounts[account.Name] = &managedAccount{
			name:    account.Name,
			enabled: enabled,
			client:  NewClient(account.Username, account.Password, sessionDir),
		}
	}
	return m
}

func (m *Manager) RefreshAccount(ctx context.Context, account string) (AccountSnapshot, error) {
	entry, err := m.get(account)
	if err != nil {
		return AccountSnapshot{}, err
	}
	if !entry.enabled {
		return AccountSnapshot{Name: account, Enabled: false, State: "DISABLED"}, nil
	}
	if err := entry.client.Login(ctx); err != nil {
		return AccountSnapshot{
			Name:    account,
			Enabled: true,
			Healthy: false,
			State:   stateForError(err),
		}, err
	}

	q, err := entry.client.Quota(ctx)
	if err != nil {
		return AccountSnapshot{
			Name:    account,
			Enabled: true,
			Healthy: false,
			State:   stateForError(err),
		}, err
	}
	tasks, err := entry.client.OfflineTasks(ctx)
	if err != nil {
		return AccountSnapshot{
			Name:    account,
			Enabled: true,
			Healthy: false,
			State:   stateForError(err),
		}, err
	}

	active := 0
	for _, task := range tasks {
		if task.Phase == PhasePending || task.Phase == PhaseRunning {
			active++
		}
	}

	quotaTotal := int64(q.Quotas.CloudDownload.Limit)
	quotaUsage := int64(q.Quotas.CloudDownload.Usage)
	quotaRemaining := quotaTotal - quotaUsage
	if quotaRemaining < 0 {
		quotaRemaining = 0
	}
	storageTotal := int64(q.Quota.Limit)
	storageUsage := int64(q.Quota.Usage)
	storageFree := storageTotal - storageUsage
	if storageFree < 0 {
		storageFree = 0
	}

	m.mu.RLock()
	lastUsed := m.lastUsed[account]
	m.mu.RUnlock()

	state := "HEALTHY"
	if quotaRemaining == 0 {
		state = "QUOTA_EXHAUSTED"
	} else if storageTotal > 0 && storageFree == 0 {
		state = "STORAGE_FULL"
	}

	return AccountSnapshot{
		Name:           account,
		Enabled:        true,
		Healthy:        true,
		QuotaRemaining: quotaRemaining,
		QuotaTotal:     quotaTotal,
		StorageFree:    storageFree,
		ActiveJobs:     active,
		MaxJobs:        m.maxJobs,
		LastUsedAt:     lastUsed,
		State:          state,
	}, nil
}

func (m *Manager) SubmitOffline(ctx context.Context, account, source string) (OfflineTask, error) {
	entry, err := m.get(account)
	if err != nil {
		return OfflineTask{}, err
	}
	if !entry.enabled {
		return OfflineTask{}, fmt.Errorf("pikpak account %q is disabled", account)
	}
	if err := entry.client.Login(ctx); err != nil {
		return OfflineTask{}, err
	}

	// A bridge process can crash after PikPak accepted the request but before
	// the remote task ID was persisted. Re-check existing tasks by the exact
	// submitted source before creating a new one so restart recovery does not
	// consume another offline-download quota slot.
	tasks, err := entry.client.OfflineTasks(ctx)
	if err != nil {
		return OfflineTask{}, fmt.Errorf("check existing offline tasks before submit: %w", err)
	}
	if existing, ok := findOfflineTaskBySource(tasks, source); ok {
		result := mapOfflineTask(existing)
		result.Existing = true
		return result, nil
	}

	task, err := entry.client.CreateOfflineTask(ctx, source)
	if err != nil {
		return OfflineTask{}, err
	}
	m.mu.Lock()
	m.lastUsed[account] = time.Now().UTC()
	m.mu.Unlock()
	return mapOfflineTask(task), nil
}

func findOfflineTaskBySource(tasks []offlineTaskAPI, source string) (offlineTaskAPI, bool) {
	source = strings.TrimSpace(source)
	for _, task := range tasks {
		if strings.TrimSpace(task.Params.URL) == source {
			return task, true
		}
	}
	return offlineTaskAPI{}, false
}

func (m *Manager) GetOfflineTask(ctx context.Context, account, taskID string) (OfflineTask, error) {
	entry, err := m.get(account)
	if err != nil {
		return OfflineTask{}, err
	}
	if err := entry.client.Login(ctx); err != nil {
		return OfflineTask{}, err
	}
	tasks, err := entry.client.OfflineTasks(ctx)
	if err != nil {
		return OfflineTask{}, err
	}
	for _, task := range tasks {
		if task.ID == taskID {
			return mapOfflineTask(task), nil
		}
	}
	return OfflineTask{}, fmt.Errorf("pikpak offline task %q not found", taskID)
}

func (m *Manager) ListFiles(ctx context.Context, account, rootFileID string) ([]RemoteFile, error) {
	entry, err := m.get(account)
	if err != nil {
		return nil, err
	}
	if err := entry.client.Login(ctx); err != nil {
		return nil, err
	}
	root, err := entry.client.File(ctx, rootFileID)
	if err != nil {
		return nil, err
	}
	if root.Kind != fileKindFolder {
		return []RemoteFile{{
			ID:           root.ID,
			ParentID:     root.ParentID,
			Name:         root.Name,
			RelativePath: root.Name,
			Size:         int64(root.Size),
			IsFolder:     false,
		}}, nil
	}

	var out []RemoteFile
	if err := m.walkFiles(ctx, entry.client, root.ID, "", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (m *Manager) walkFiles(ctx context.Context, client *Client, parentID, prefix string, out *[]RemoteFile) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	children, err := client.ListByParentID(ctx, parentID)
	if err != nil {
		return err
	}
	for _, child := range children {
		relative := path.Join(prefix, child.Name)
		isFolder := child.Kind == fileKindFolder
		*out = append(*out, RemoteFile{
			ID:           child.ID,
			ParentID:     child.ParentID,
			Name:         child.Name,
			RelativePath: relative,
			Size:         int64(child.Size),
			IsFolder:     isFolder,
		})
		if isFolder {
			if err := m.walkFiles(ctx, client, child.ID, relative, out); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) GetDownloadURL(ctx context.Context, account, fileID string) (string, error) {
	entry, err := m.get(account)
	if err != nil {
		return "", err
	}
	if err := entry.client.Login(ctx); err != nil {
		return "", err
	}
	return entry.client.DownloadURL(ctx, fileID)
}

func (m *Manager) DeletePermanently(ctx context.Context, account, fileID string) error {
	entry, err := m.get(account)
	if err != nil {
		return err
	}
	if strings.TrimSpace(fileID) == "" {
		return fmt.Errorf("refusing to delete empty PikPak file ID")
	}
	if err := entry.client.Login(ctx); err != nil {
		return err
	}
	if err := entry.client.DeletePermanently(ctx, fileID); err != nil {
		// Cleanup is deliberately idempotent. If a previous delete succeeded
		// remotely but the bridge crashed before persisting COMPLETED, a retry
		// may observe that the exact recorded root ID is already gone.
		if KindOf(err) == ErrorKindNotFound {
			return nil
		}
		return err
	}
	return nil
}

func (m *Manager) get(name string) (*managedAccount, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.accounts[name]
	if !ok {
		return nil, fmt.Errorf("pikpak account %q not configured", name)
	}
	return entry, nil
}

func mapOfflineTask(task offlineTaskAPI) OfflineTask {
	return OfflineTask{
		ID:         task.ID,
		Status:     task.Phase,
		RootFileID: task.FileID,
		Error:      task.Message,
	}
}

func stateForError(err error) string {
	switch KindOf(err) {
	case ErrorKindCaptcha:
		return "CAPTCHA_REQUIRED"
	case ErrorKindAuth:
		return "AUTH_FAILED"
	case ErrorKindQuota:
		return "QUOTA_EXHAUSTED"
	case ErrorKindStorage:
		return "STORAGE_FULL"
	default:
		return "ERROR"
	}
}
