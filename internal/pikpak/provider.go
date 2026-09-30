package pikpak

import (
	"context"
	"time"
)

type AccountSnapshot struct {
	ID             string
	Name           string
	Enabled        bool
	Healthy        bool
	QuotaRemaining int64
	QuotaTotal     int64
	StorageFree    int64
	StorageTotal   int64
	ActiveJobs     int
	MaxJobs        int
	LastUsedAt     time.Time
	CooldownUntil  time.Time
	State          string
}

type OfflineTask struct {
	ID         string
	Status     string
	RootFileID string
	Error      string
}

type RemoteFile struct {
	ID           string
	ParentID     string
	Name         string
	RelativePath string
	Size         int64
	IsFolder     bool
}

type ResolvedFiles struct {
	RootName string
	Files    []RemoteFile
}

type Provider interface {
	RefreshAccount(ctx context.Context, accountID string) (AccountSnapshot, error)
	SubmitOffline(ctx context.Context, accountID, source string) (OfflineTask, error)
	GetOfflineTask(ctx context.Context, accountID, taskID string) (OfflineTask, error)
	ResolveFiles(ctx context.Context, accountID, rootFileID string) (ResolvedFiles, error)
	GetDownloadURL(ctx context.Context, accountID, fileID string) (string, error)
	DeletePermanently(ctx context.Context, accountID, fileID string) error
}
