package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nukewarrior/pikpak-bridge/internal/aria2"
	"github.com/nukewarrior/pikpak-bridge/internal/domain"
	"github.com/nukewarrior/pikpak-bridge/internal/pikpak"
	"github.com/nukewarrior/pikpak-bridge/internal/store"
)

type fakeAria2 struct {
	added         map[string]bool
	addCalls      int
	tellStatusErr error
	statusByGID   map[string]aria2.Status
	forgetCalls   int
}

func (f *fakeAria2) Snapshots(context.Context) []aria2.InstanceSnapshot {
	return []aria2.InstanceSnapshot{{
		ID: "a1", Name: "aria2", Enabled: true, Healthy: true,
	}}
}
func (f *fakeAria2) Snapshot(context.Context, string) (aria2.InstanceSnapshot, error) {
	return aria2.InstanceSnapshot{
		ID: "a1", Name: "aria2", Enabled: true, Healthy: true,
	}, nil
}
func (f *fakeAria2) Add(_ context.Context, instanceID, baseDir, uri, gid, relativePath string) (string, error) {
	f.addCalls++
	if uri == "" || instanceID != "a1" || baseDir != "/downloads/movies" || relativePath == "" {
		return "", errors.New("bad add args")
	}
	if f.added == nil {
		f.added = map[string]bool{}
	}
	f.added[gid] = true
	return gid, nil
}
func (f *fakeAria2) TellStatus(_ context.Context, instanceID, gid string) (aria2.Status, error) {
	if f.tellStatusErr != nil {
		return aria2.Status{}, f.tellStatusErr
	}
	if instanceID != "a1" {
		return aria2.Status{}, errors.New("wrong instance")
	}
	if f.statusByGID != nil {
		if status, ok := f.statusByGID[gid]; ok {
			return status, nil
		}
	}
	if f.added != nil && f.added[gid] {
		return aria2.Status{
			GID: gid, Status: "complete", TotalLength: "1234", CompletedLength: "1234",
		}, nil
	}
	return aria2.Status{}, errors.New("not found")
}
func (f *fakeAria2) Forget(context.Context, string, string) error {
	f.forgetCalls++
	return nil
}

type fakeURLProvider struct{}

func (fakeURLProvider) RefreshAccount(context.Context, string) (pikpak.AccountSnapshot, error) {
	return pikpak.AccountSnapshot{}, nil
}
func (fakeURLProvider) SubmitOffline(context.Context, string, string) (pikpak.OfflineTask, error) {
	return pikpak.OfflineTask{}, nil
}
func (fakeURLProvider) GetOfflineTask(context.Context, string, string) (pikpak.OfflineTask, error) {
	return pikpak.OfflineTask{}, nil
}
func (fakeURLProvider) ListFiles(context.Context, string, string) ([]pikpak.RemoteFile, error) {
	return nil, nil
}
func (fakeURLProvider) GetDownloadURL(context.Context, string, string) (string, error) {
	return "https://example.invalid/file", nil
}
func (fakeURLProvider) DeletePermanently(context.Context, string, string) error { return nil }

func TestAria2WorkerAdvancesToVerifying(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	task := domain.Task{
		ID:              "task-a",
		Source:          "magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567",
		SourceType:      "magnet",
		SourceKey:       "btih:0123456789ABCDEF0123456789ABCDEF01234567",
		TargetID:        "movies",
		TargetName:      "电影",
		Aria2InstanceID: "a1",
		DownloadDir:     "/downloads/movies",
		Status:          domain.TaskWaitingAria2,
		PikPakAccountID: "pp1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	task, err = db.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	task.Status = domain.TaskWaitingAria2
	task.PikPakAccountID = "pp1"
	if err := db.SaveTask(context.Background(), &task, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceRemoteFiles(context.Background(), task.ID, []domain.RemoteFile{{
		TaskID: task.ID, PikPakFileID: "file-1", Name: "movie.mkv",
		RelativePath: "folder/movie.mkv", Size: 1234,
	}}); err != nil {
		t.Fatal(err)
	}

	backend := &fakeAria2{}
	w := NewAria2(db, fakeURLProvider{}, backend, Aria2Options{
		StatusInterval: time.Millisecond,
		RetryInterval:  time.Millisecond,
		MaxRetry:       3,
	})
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.TaskVerifying {
		t.Fatalf("want VERIFYING, got %s", got.Status)
	}
	downloads, err := db.ListDownloads(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(downloads) != 1 || downloads[0].Status != domain.DownloadComplete {
		t.Fatalf("unexpected downloads: %#v", downloads)
	}
	if downloads[0].Aria2InstanceID != "a1" {
		t.Fatalf("want a1, got %s", downloads[0].Aria2InstanceID)
	}
	if downloads[0].CompletedLength != 1234 {
		t.Fatalf("want completed length 1234, got %d", downloads[0].CompletedLength)
	}
}

func TestDeterministicGID(t *testing.T) {
	a := deterministicGID("task", "file")
	b := deterministicGID("task", "file")
	if a != b || len(a) != 16 {
		t.Fatalf("unexpected gid %q / %q", a, b)
	}
}


func TestAria2PollErrorDoesNotResubmit(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/poll-error.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	task := domain.Task{
		ID:              "task-poll",
		Source:          "https://example.invalid/file",
		SourceType:      "https",
		SourceKey:       "url:poll-error",
		TargetID:        "movies",
		TargetName:      "电影",
		Aria2InstanceID: "a1",
		DownloadDir:     "/downloads/movies",
		Status:          domain.TaskWaitingAria2,
		PikPakAccountID: "pp1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	task, err = db.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	task.Status = domain.TaskWaitingAria2
	task.PikPakAccountID = "pp1"
	if err := db.SaveTask(context.Background(), &task, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceRemoteFiles(context.Background(), task.ID, []domain.RemoteFile{{
		TaskID: task.ID, PikPakFileID: "file-1", Name: "movie.mkv",
		RelativePath: "folder/movie.mkv", Size: 1234,
	}}); err != nil {
		t.Fatal(err)
	}

	backend := &fakeAria2{}
	w := NewAria2(db, fakeURLProvider{}, backend, Aria2Options{
		StatusInterval: time.Millisecond,
		RetryInterval:  time.Millisecond,
		MaxRetry:       3,
	})
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if backend.addCalls != 1 {
		t.Fatalf("want one initial aria2 add, got %d", backend.addCalls)
	}

	backend.tellStatusErr = errors.New("EOF")
	time.Sleep(2 * time.Millisecond)
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	downloads, err := db.ListDownloads(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(downloads) != 1 {
		t.Fatalf("unexpected downloads: %#v", downloads)
	}
	if downloads[0].Status != domain.DownloadSubmitted {
		t.Fatalf("transient poll error changed status to %s", downloads[0].Status)
	}
	if downloads[0].LastError != "EOF" {
		t.Fatalf("want EOF recorded, got %q", downloads[0].LastError)
	}
	if backend.addCalls != 1 {
		t.Fatalf("transient poll error caused resubmit: add calls = %d", backend.addCalls)
	}
}


func TestAria2ConfirmedFailureKeepsOldRecordAndRotatesGID(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/confirmed-error.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	task := domain.Task{
		ID:              "task-confirmed-error",
		Source:          "https://example.invalid/file",
		SourceType:      "https",
		SourceKey:       "url:confirmed-error",
		TargetID:        "movies",
		TargetName:      "电影",
		Aria2InstanceID: "a1",
		DownloadDir:     "/downloads/movies",
		Status:          domain.TaskWaitingAria2,
		PikPakAccountID: "pp1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	task, err = db.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	task.Status = domain.TaskWaitingAria2
	task.PikPakAccountID = "pp1"
	if err := db.SaveTask(context.Background(), &task, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceRemoteFiles(context.Background(), task.ID, []domain.RemoteFile{{
		TaskID: task.ID, PikPakFileID: "file-1", Name: "movie.mkv",
		RelativePath: "folder/movie.mkv", Size: 1234,
	}}); err != nil {
		t.Fatal(err)
	}

	backend := &fakeAria2{}
	w := NewAria2(db, fakeURLProvider{}, backend, Aria2Options{
		StatusInterval: time.Millisecond,
		RetryInterval:  time.Millisecond,
		MaxRetry:       3,
	})

	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	downloads, err := db.ListDownloads(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(downloads) != 1 {
		t.Fatalf("unexpected downloads: %#v", downloads)
	}
	oldGID := downloads[0].Aria2GID

	backend.statusByGID = map[string]aria2.Status{
		oldGID: {
			GID:             oldGID,
			Status:          "error",
			TotalLength:     "1234",
			CompletedLength: "321",
			ErrorCode:       "3",
			ErrorMessage:    "resource not found",
		},
	}

	time.Sleep(2 * time.Millisecond)
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	downloads, err = db.ListDownloads(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if downloads[0].Aria2GID == oldGID {
		t.Fatalf("want retry gid different from failed gid %s", oldGID)
	}
	if downloads[0].Aria2GID != retryGID(oldGID) {
		t.Fatalf("unexpected retry gid %s", downloads[0].Aria2GID)
	}
	if downloads[0].Status != domain.DownloadPending {
		t.Fatalf("want pending after confirmed failure, got %s", downloads[0].Status)
	}
	if backend.forgetCalls != 0 {
		t.Fatalf("failed aria2 result was removed: forget calls = %d", backend.forgetCalls)
	}
	if backend.addCalls != 1 {
		t.Fatalf("confirmed failure should schedule retry, not immediately resubmit: add calls = %d", backend.addCalls)
	}

	time.Sleep(2 * time.Millisecond)
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if backend.addCalls != 2 {
		t.Fatalf("want retry submitted with a new gid, add calls = %d", backend.addCalls)
	}
}
