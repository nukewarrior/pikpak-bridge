package domain

import "time"

type TaskStatus string

const (
	TaskQueued               TaskStatus = "QUEUED"
	TaskWaitingPikPakAccount TaskStatus = "WAITING_PIKPAK_ACCOUNT"
	TaskPikPakSubmitting     TaskStatus = "PIKPAK_SUBMITTING"
	TaskPikPakRunning        TaskStatus = "PIKPAK_RUNNING"
	TaskPikPakComplete       TaskStatus = "PIKPAK_COMPLETE"
	TaskResolvingFiles       TaskStatus = "RESOLVING_FILES"
	TaskWaitingAria2         TaskStatus = "WAITING_ARIA2"
	TaskAria2Downloading     TaskStatus = "ARIA2_DOWNLOADING"
	TaskVerifying            TaskStatus = "VERIFYING"
	TaskReadyToCleanup       TaskStatus = "READY_TO_CLEANUP"
	TaskPikPakDeleting       TaskStatus = "PIKPAK_DELETING"
	TaskCompleted            TaskStatus = "COMPLETED"
	TaskCancelled            TaskStatus = "CANCELLED"
	TaskPikPakFailed         TaskStatus = "PIKPAK_FAILED"
	TaskAria2Failed          TaskStatus = "ARIA2_FAILED"
	TaskVerifyFailed         TaskStatus = "VERIFY_FAILED"
	TaskCleanupFailed        TaskStatus = "CLEANUP_FAILED"
)

type Task struct {
	ID              string     `json:"id"`
	Source          string     `json:"source"`
	SourceType      string     `json:"source_type"`
	SourceKey       string     `json:"-"`
	Name            string     `json:"name"`
	TargetID        string     `json:"target_id"`
	TargetName      string     `json:"target_name"`
	Aria2InstanceID string     `json:"aria2_instance_id"`
	DownloadDir     string     `json:"download_dir"`
	Status          TaskStatus `json:"status"`

	PikPakAccountID  string `json:"pikpak_account_id,omitempty"`
	PikPakTaskID     string `json:"pikpak_task_id,omitempty"`
	PikPakRootFileID string `json:"pikpak_root_file_id,omitempty"`

	RetryCount       int        `json:"retry_count"`
	ManualRetryCount int        `json:"manual_retry_count"`
	NextAttemptAt    *time.Time `json:"next_attempt_at,omitempty"`
	Error         string     `json:"error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}

type RemoteFile struct {
	TaskID       string `json:"task_id"`
	PikPakFileID string `json:"pikpak_file_id"`
	ParentFileID string `json:"parent_file_id,omitempty"`
	Name         string `json:"name"`
	RelativePath string `json:"relative_path"`
	Size         int64  `json:"size"`
	IsFolder     bool   `json:"is_folder"`
}

type DownloadStatus string

const (
	DownloadPending   DownloadStatus = "PENDING"
	DownloadSubmitted DownloadStatus = "SUBMITTED"
	DownloadActive    DownloadStatus = "ACTIVE"
	DownloadWaiting   DownloadStatus = "WAITING"
	DownloadPaused    DownloadStatus = "PAUSED"
	DownloadComplete  DownloadStatus = "COMPLETE"
	DownloadError     DownloadStatus = "ERROR"
)

type Download struct {
	TaskID           string         `json:"task_id"`
	PikPakFileID     string         `json:"pikpak_file_id"`
	Aria2InstanceID  string         `json:"aria2_instance_id"`
	Aria2GID         string         `json:"aria2_gid"`
	Status           DownloadStatus `json:"status"`
	RelativePath     string         `json:"relative_path"`
	ExpectedSize     int64          `json:"expected_size"`
	TotalLength      int64          `json:"total_length"`
	CompletedLength  int64          `json:"completed_length"`
	RetryCount       int            `json:"retry_count"`
	EOFRetryCount    int            `json:"eof_retry_count"`
	NextAttemptAt    *time.Time     `json:"next_attempt_at,omitempty"`
	LastError        string         `json:"last_error,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}


type TaskEvent struct {
	ID        int64     `json:"id"`
	TaskID    string    `json:"task_id"`
	Type      string    `json:"type"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}
