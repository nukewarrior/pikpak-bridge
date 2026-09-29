package pikpak

import (
	"context"
	"time"
)

type AccountSnapshot struct {
	Name           string
	Enabled        bool
	Healthy        bool
	QuotaRemaining int64
	QuotaTotal     int64
	StorageFree    int64
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

type Provider interface {
	RefreshAccount(ctx context.Context, account string) (AccountSnapshot, error)
	SubmitOffline(ctx context.Context, account, source string) (OfflineTask, error)
	GetOfflineTask(ctx context.Context, account, taskID string) (OfflineTask, error)
	ListFiles(ctx context.Context, account, rootFileID string) ([]RemoteFile, error)
	GetDownloadURL(ctx context.Context, account, fileID string) (string, error)
	DeletePermanently(ctx context.Context, account, fileID string) error
}
