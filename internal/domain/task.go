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
	ID               string     `json:"id"`
	Source            string     `json:"source"`
	SourceType        string     `json:"source_type"`
	SourceKey         string     `json:"-"`
	Status            TaskStatus `json:"status"`
	PikPakAccount     string     `json:"pikpak_account,omitempty"`
	PikPakTaskID      string     `json:"pikpak_task_id,omitempty"`
	PikPakRootFileID  string     `json:"pikpak_root_file_id,omitempty"`
	Aria2Instance     string     `json:"aria2_instance,omitempty"`
	Error             string     `json:"error,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
}
