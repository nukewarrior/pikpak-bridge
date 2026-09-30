package worker

import (
	"context"
	"testing"
	"time"

	"github.com/nukewarrior/pikpak-bridge/internal/domain"
	"github.com/nukewarrior/pikpak-bridge/internal/pikpak"
	"github.com/nukewarrior/pikpak-bridge/internal/store"
)

type fakeProvider struct {
	snapshot pikpak.AccountSnapshot
	submit   pikpak.OfflineTask
	files    []pikpak.RemoteFile
	submits  int
}

func (f *fakeProvider) RefreshAccount(context.Context, string) (pikpak.AccountSnapshot, error) {
	return f.snapshot, nil
}
func (f *fakeProvider) SubmitOffline(context.Context, string, string) (pikpak.OfflineTask, error) {
	f.submits++
	return f.submit, nil
}
func (f *fakeProvider) GetOfflineTask(context.Context, string, string) (pikpak.OfflineTask, error) {
	return f.submit, nil
}
func (f *fakeProvider) ListFiles(context.Context, string, string) ([]pikpak.RemoteFile, error) {
	return f.files, nil
}
func (f *fakeProvider) GetDownloadURL(context.Context, string, string) (string, error) {
	return "", nil
}
func (f *fakeProvider) DeletePermanently(context.Context, string, string) error {
	return nil
}

func TestWorkerAdvancesCompleteTaskToWaitingAria2(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	task := domain.Task{
		ID:              "task-1",
		Source:          "magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567",
		SourceType:      "magnet",
		SourceKey:       "btih:0123456789ABCDEF0123456789ABCDEF01234567",
		TargetID:        "movies",
		TargetName:      "电影",
		Aria2InstanceID: "nas",
		DownloadDir:     "/media/movies",
		Status:          domain.TaskQueued,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	provider := &fakeProvider{
		snapshot: pikpak.AccountSnapshot{
			ID:             "pp1",
			Name:           "主账号",
			Enabled:        true,
			Healthy:        true,
			QuotaRemaining: 3,
			QuotaTotal:     3,
			StorageFree:    10_000_000_000,
			MaxJobs:        2,
		},
		submit: pikpak.OfflineTask{
			ID:         "remote-task",
			Status:     pikpak.PhaseComplete,
			RootFileID: "root-file",
		},
		files: []pikpak.RemoteFile{{
			ID:           "file-1",
			ParentID:     "root-file",
			Name:         "movie.mkv",
			RelativePath: "movie.mkv",
			Size:         1234,
		}},
	}
	w := New(db, provider, Options{
		AccountIDs:     []string{"pp1"},
		QuotaRefresh:   time.Hour,
		StatusInterval: time.Millisecond,
		RetryInterval:  time.Millisecond,
		MaxRetry:       3,
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
	if got.Status != domain.TaskWaitingAria2 {
		t.Fatalf("want %s, got %s", domain.TaskWaitingAria2, got.Status)
	}
	if got.PikPakAccountID != "pp1" || got.PikPakTaskID != "remote-task" || got.PikPakRootFileID != "root-file" {
		t.Fatalf("unexpected PikPak state: %#v", got)
	}
	files, err := db.ListRemoteFiles(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].PikPakFileID != "file-1" {
		t.Fatalf("unexpected remote files: %#v", files)
	}
	if provider.submits != 1 {
		t.Fatalf("want one submit, got %d", provider.submits)
	}
}

func TestParseByteSize(t *testing.T) {
	tests := map[string]int64{
		"":     0,
		"1024": 1024,
		"2GB":  2_000_000_000,
		"2GiB": 2_147_483_648,
	}
	for input, want := range tests {
		got, err := ParseByteSize(input)
		if err != nil {
			t.Fatalf("%s: %v", input, err)
		}
		if got != want {
			t.Fatalf("%s: want %d, got %d", input, want, got)
		}
	}
}
