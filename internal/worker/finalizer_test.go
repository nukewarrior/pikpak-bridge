package worker

import (
	"context"
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
func (p *cleanupProvider) CancelOfflineTask(context.Context, string, string) error {
	return nil
}
func (p *cleanupProvider) ResolveFiles(context.Context, string, string) (pikpak.ResolvedFiles, error) {
	return pikpak.ResolvedFiles{}, nil
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
		SourceKey: "url:test", TargetID: "default", TargetName: "默认", Aria2InstanceID: "a1", DownloadDir: "/downloads", Status: domain.TaskQueued,
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
	task.PikPakAccountID = "pp1"
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

func TestFinalizerCompletesAndRetainsCache(t *testing.T) {
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
	if provider.deletes != 0 {
        t.Fatalf("completed downloads must retain cloud cache, got %d deletes", provider.deletes)
    }
    cache, err := db.LatestRetainedCache(context.Background(), "url:test")
    if err != nil || cache == nil || cache.RootFileID != "root-1" {
        t.Fatalf("verified cloud cache not persisted: cache=%+v err=%v",cache,err)
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

func TestFinalizerNeverDeletesRetainedCache(t *testing.T) {
    db, task := prepareVerifiedTask(t, 1234, 1234, 1234)
    defer db.Close()
    provider := &cleanupProvider{}
    worker := NewFinalizer(db, provider, FinalizeOptions{VerifySize:true, CleanupEnabled:true})
    if err:=worker.RunOnce(context.Background());err!=nil {t.Fatal(err)}
    if provider.deletes!=0 {t.Fatalf("expected zero cloud deletes, got %d", provider.deletes)}
    got,err:=db.GetTask(context.Background(),task.ID)
    if err!=nil || got.Status!=domain.TaskCompleted {t.Fatalf("not completed: %+v, %v",got,err)}
}
