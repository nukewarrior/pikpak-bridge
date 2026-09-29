package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nukewarrior/pikpak-bridge/internal/domain"
	"github.com/nukewarrior/pikpak-bridge/internal/pikpak"
	"github.com/nukewarrior/pikpak-bridge/internal/store"
)

type cleanupProvider struct {
	deletes int
	err     error
}

func (p *cleanupProvider) RefreshAccount(context.Context, string) (pikpak.AccountSnapshot, error) {
	return pikpak.AccountSnapshot{}, nil
}
func (p *cleanupProvider) SubmitOffline(context.Context, string, string) (pikpak.OfflineTask, error) {
	return pikpak.OfflineTask{}, nil
}
func (p *cleanupProvider) GetOfflineTask(context.Context, string, string) (pikpak.OfflineTask, error) {
	return pikpak.OfflineTask{}, nil
}
func (p *cleanupProvider) ListFiles(context.Context, string, string) ([]pikpak.RemoteFile, error) {
	return nil, nil
}
func (p *cleanupProvider) GetDownloadURL(context.Context, string, string) (string, error) {
	return "", nil
}
func (p *cleanupProvider) DeletePermanently(context.Context, string, string) error {
	p.deletes++
	return p.err
}

func prepareVerifiedTask(t *testing.T, expected, total, completed int64) (*store.SQLite, domain.Task) {
	t.Helper()
	db, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task := domain.Task{
		ID: "verify-task", Source: "https://example.invalid/source", SourceType: "https",
		SourceKey: "url:test", Status: domain.TaskQueued,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	task, err = db.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	task.Status = domain.TaskVerifying
	task.PikPakAccount = "pp1"
	task.PikPakRootFileID = "root-1"
	if err := db.SaveTask(context.Background(), &task, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceRemoteFiles(context.Background(), task.ID, []domain.RemoteFile{{
		TaskID: task.ID, PikPakFileID: "file-1", Name: "file.bin",
		RelativePath: "file.bin", Size: expected,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureDownloads(context.Background(), task.ID, "a1", deterministicGID); err != nil {
		t.Fatal(err)
	}
	downloads, err := db.ListDownloads(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	downloads[0].Status = domain.DownloadComplete
	downloads[0].TotalLength = total
	downloads[0].CompletedLength = completed
	if err := db.SaveDownload(context.Background(), &downloads[0]); err != nil {
		t.Fatal(err)
	}
	return db, task
}

func TestFinalizerCompletesAndDeletesExactRoot(t *testing.T) {
	db, task := prepareVerifiedTask(t, 1234, 1234, 1234)
	defer db.Close()
	provider := &cleanupProvider{}
	w := NewFinalizer(db, provider, FinalizeOptions{
		VerifySize: true, CleanupEnabled: true, CleanupDelay: 0, MaxRetry: 3,
	})

	for i := 0; i < 3; i++ {
		if err := w.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.TaskCompleted {
		t.Fatalf("want COMPLETED, got %s", got.Status)
	}
	if provider.deletes != 1 {
		t.Fatalf("want exactly one delete, got %d", provider.deletes)
	}
	if got.CompletedAt == nil {
		t.Fatal("completed_at not set")
	}
}

func TestFinalizerRejectsSizeMismatchWithoutDelete(t *testing.T) {
	db, task := prepareVerifiedTask(t, 1234, 1000, 1000)
	defer db.Close()
	provider := &cleanupProvider{}
	w := NewFinalizer(db, provider, FinalizeOptions{
		VerifySize: true, CleanupEnabled: true,
	})
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.TaskVerifyFailed {
		t.Fatalf("want VERIFY_FAILED, got %s", got.Status)
	}
	if provider.deletes != 0 {
		t.Fatalf("cleanup must not run after failed verification")
	}
}

func TestFinalizerRetriesCleanup(t *testing.T) {
	db, task := prepareVerifiedTask(t, 1234, 1234, 1234)
	defer db.Close()
	provider := &cleanupProvider{err: errors.New("temporary delete failure")}
	w := NewFinalizer(db, provider, FinalizeOptions{
		VerifySize: true, CleanupEnabled: true, CleanupDelay: 0,
		RetryInterval: time.Millisecond, MaxRetry: 3,
	})
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := w.RunOnce(context.Background()); err == nil {
		// RunOnce logs per-task failures internally and returns only iteration
		// failures, so this path is expected.
	}
	got, err := db.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.TaskPikPakDeleting {
		t.Fatalf("want PIKPAK_DELETING retry state, got %s", got.Status)
	}
}
