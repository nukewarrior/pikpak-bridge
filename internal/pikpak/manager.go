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
	id      string
	name    string
	maxJobs int
	enabled bool
	client  *Client
	mu      sync.Mutex
}

type Manager struct {
	mu       sync.RWMutex
	accounts map[string]*managedAccount
	lastUsed map[string]time.Time
}

func NewManager(accounts []config.PikPakAccount, sessionDir string) *Manager {
	m := &Manager{
		accounts: make(map[string]*managedAccount, len(accounts)),
		lastUsed: make(map[string]time.Time, len(accounts)),
	}
	for _, account := range accounts {
		enabled := config.Enabled(account.Enabled) &&
			strings.TrimSpace(account.Username) != "" &&
			strings.TrimSpace(account.Password) != ""
		m.accounts[account.ID] = &managedAccount{
			id:      account.ID,
			name:    account.Name,
			maxJobs: account.MaxJobs,
			enabled: enabled,
			client:  NewClient(account.Username, account.Password, sessionDir),
		}
	}
	return m
}

func (m *Manager) RefreshAccount(ctx context.Context, accountID string) (AccountSnapshot, error) {
	entry, err := m.get(accountID)
	if err != nil {
		return AccountSnapshot{}, err
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()

	base := AccountSnapshot{
		ID:      entry.id,
		Name:    entry.name,
		Enabled: entry.enabled,
		MaxJobs: entry.maxJobs,
	}
	if !entry.enabled {
		base.State = "DISABLED"
		return base, nil
	}
	if err := entry.client.Login(ctx); err != nil {
		base.State = stateForError(err)
		return base, err
	}

	q, err := entry.client.Quota(ctx)
	if err != nil {
		base.State = stateForError(err)
		return base, err
	}
	tasks, err := entry.client.OfflineTasks(ctx)
	if err != nil {
		base.State = stateForError(err)
		return base, err
	}

	activeTaskIDs := make([]string, 0)
	for _, task := range tasks {
		if task.Phase == PhasePending || task.Phase == PhaseRunning {
			activeTaskIDs = append(activeTaskIDs, task.ID)
		}
	}
	active := len(activeTaskIDs)

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
	lastUsed := m.lastUsed[accountID]
	m.mu.RUnlock()

	state := "HEALTHY"
	if quotaRemaining == 0 {
		state = "QUOTA_EXHAUSTED"
	} else if storageTotal > 0 && storageFree == 0 {
		state = "STORAGE_FULL"
	}

	base.Healthy = true
	base.QuotaRemaining = quotaRemaining
	base.QuotaTotal = quotaTotal
	base.StorageFree = storageFree
	base.StorageTotal = storageTotal
	base.ActiveJobs = active
	base.ActiveTaskIDs = activeTaskIDs
	base.LastUsedAt = lastUsed
	base.State = state
	return base, nil
}

func (m *Manager) SubmitOffline(ctx context.Context, accountID, source string) (OfflineTask, error) {
	entry, err := m.get(accountID)
	if err != nil {
		return OfflineTask{}, err
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if !entry.enabled {
		return OfflineTask{}, fmt.Errorf("pikpak account %q is disabled", accountID)
	}
	if err := entry.client.Login(ctx); err != nil {
		return OfflineTask{}, err
	}

	task, err := entry.client.CreateOfflineTask(ctx, source)
	if err != nil {
		return OfflineTask{}, err
	}
	m.mu.Lock()
	m.lastUsed[accountID] = time.Now().UTC()
	m.mu.Unlock()
	return mapOfflineTask(task), nil
}

func (m *Manager) GetOfflineTask(ctx context.Context, accountID, taskID string) (OfflineTask, error) {
	entry, err := m.get(accountID)
	if err != nil {
		return OfflineTask{}, err
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
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

func (m *Manager) CancelOfflineTask(ctx context.Context, accountID, taskID string) error {
	entry, err := m.get(accountID)
	if err != nil {
		return err
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if strings.TrimSpace(taskID) == "" {
		return fmt.Errorf("refusing to cancel empty PikPak task ID")
	}
	if err := entry.client.Login(ctx); err != nil {
		return err
	}
	if err := entry.client.DeleteOfflineTask(ctx, taskID, true); err != nil {
		if KindOf(err) == ErrorKindNotFound {
			return nil
		}
		return err
	}
	return nil
}

func (m *Manager) ResolveFiles(ctx context.Context, accountID, rootFileID string) (ResolvedFiles, error) {
	entry, err := m.get(accountID)
	if err != nil {
		return ResolvedFiles{}, err
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if err := entry.client.Login(ctx); err != nil {
		return ResolvedFiles{}, err
	}
	root, err := entry.client.File(ctx, rootFileID)
	if err != nil {
		return ResolvedFiles{}, err
	}
	if root.Kind != fileKindFolder {
		return ResolvedFiles{
			RootName: root.Name,
			Files: []RemoteFile{{
				ID:           root.ID,
				ParentID:     root.ParentID,
				Name:         root.Name,
				RelativePath: root.Name,
				Size:         int64(root.Size),
				IsFolder:     false,
			}},
		}, nil
	}

	var out []RemoteFile
	if err := m.walkFiles(ctx, entry.client, root.ID, root.Name, &out); err != nil {
		return ResolvedFiles{}, err
	}
	return ResolvedFiles{
		RootName: root.Name,
		Files:    out,
	}, nil
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

func (m *Manager) GetDownloadURL(ctx context.Context, accountID, fileID string) (string, error) {
	entry, err := m.get(accountID)
	if err != nil {
		return "", err
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if err := entry.client.Login(ctx); err != nil {
		return "", err
	}
	return entry.client.DownloadURL(ctx, fileID)
}

func (m *Manager) DeletePermanently(ctx context.Context, accountID, fileID string) error {
	entry, err := m.get(accountID)
	if err != nil {
		return err
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if strings.TrimSpace(fileID) == "" {
		return fmt.Errorf("refusing to delete empty PikPak file ID")
	}
	if err := entry.client.Login(ctx); err != nil {
		return err
	}
	if err := entry.client.DeletePermanently(ctx, fileID); err != nil {
		if KindOf(err) == ErrorKindNotFound {
			return nil
		}
		return err
	}
	return nil
}

func (m *Manager) get(id string) (*managedAccount, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.accounts[id]
	if !ok {
		return nil, fmt.Errorf("pikpak account %q not configured", id)
	}
	return entry, nil
}

func mapOfflineTask(task offlineTaskAPI) OfflineTask {
	return OfflineTask{
		ID:         task.ID,
		Status:     task.Phase,
		RootFileID: task.FileID,
		Progress:   task.Progress,
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
