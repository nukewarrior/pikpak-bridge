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
func (f *fakeAria2) Forget(_ context.Context, _ string, gid string) error {
	f.forgetCalls++
	if f.added != nil {
		delete(f.added, gid)
	}
	if f.statusByGID != nil {
		delete(f.statusByGID, gid)
	}
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


func TestPikPakEOFFailureRemovesAria2HistoryAndRetriesSameGID(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/pikpak-eof.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	task := domain.Task{
		ID:              "task-pikpak-eof",
		Source:          "https://example.invalid/file",
		SourceType:      "https",
		SourceKey:       "url:pikpak-eof",
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
	gid := downloads[0].Aria2GID

	backend.statusByGID = map[string]aria2.Status{
		gid: {
			GID:             gid,
			Status:          "error",
			TotalLength:     "1234",
			CompletedLength: "321",
			ErrorCode:       "1",
			ErrorMessage:    "Failed to receive data, cause: EOF was received",
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
	if downloads[0].Aria2GID != gid {
		t.Fatalf("EOF retry should keep gid %s, got %s", gid, downloads[0].Aria2GID)
	}
	if downloads[0].Status != domain.DownloadPending {
		t.Fatalf("want PENDING after EOF cleanup, got %s", downloads[0].Status)
	}
	if downloads[0].EOFRetryCount != 1 {
		t.Fatalf("want EOF retry count 1, got %d", downloads[0].EOFRetryCount)
	}
	if backend.forgetCalls != 1 {
		t.Fatalf("want one aria2 history cleanup, got %d", backend.forgetCalls)
	}
	if backend.addCalls != 1 {
		t.Fatalf("EOF handling should schedule retry, not immediately add: %d", backend.addCalls)
	}

	time.Sleep(2 * time.Millisecond)
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if backend.addCalls != 2 {
		t.Fatalf("want aria2 retry after EOF, add calls = %d", backend.addCalls)
	}

	downloads, err = db.ListDownloads(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if downloads[0].Aria2GID != gid {
		t.Fatalf("retry submission changed gid: want %s got %s", gid, downloads[0].Aria2GID)
	}
}

func TestPikPakEOFTellStatusErrorTriggersCleanup(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/pikpak-eof-rpc.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	task := domain.Task{
		ID:              "task-pikpak-eof-rpc",
		Source:          "https://example.invalid/file2",
		SourceType:      "https",
		SourceKey:       "url:pikpak-eof-rpc",
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
		TaskID: task.ID, PikPakFileID: "file-2", Name: "movie2.mkv",
		RelativePath: "folder/movie2.mkv", Size: 1234,
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
	backend.tellStatusErr = errors.New("Failed to receive data, cause: EOF was received")
	time.Sleep(2 * time.Millisecond)
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if backend.forgetCalls != 1 {
		t.Fatalf("want one cleanup for EOF tellStatus error, got %d", backend.forgetCalls)
	}
}


func TestPikPakEOFStopsAfterTenRetries(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/eof-exhausted.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	task := domain.Task{
		ID:              "task-eof-exhausted",
		Source:          "https://example.invalid/eof",
		SourceType:      "https",
		SourceKey:       "url:eof-exhausted",
		TargetID:        "movies",
		TargetName:      "电影",
		Aria2InstanceID: "a1",
		DownloadDir:     "/downloads/movies",
		Status:          domain.TaskAria2Downloading,
		PikPakAccountID: "pp1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceRemoteFiles(context.Background(), task.ID, []domain.RemoteFile{{
		TaskID: task.ID, PikPakFileID: "file-1", Name: "movie.mkv",
		RelativePath: "folder/movie.mkv", Size: 1234,
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
	download := downloads[0]

	backend := &fakeAria2{added: map[string]bool{download.Aria2GID: true}}
	w := NewAria2(db, fakeURLProvider{}, backend, Aria2Options{
		RetryInterval: time.Millisecond,
		MaxRetry:      10,
	})

	message := "Failed to receive data, cause: EOF was received"
	for i := 1; i <= 10; i++ {
		_ = w.retryPikPakEOF(context.Background(), &download, message)
		if download.EOFRetryCount != i {
			t.Fatalf("attempt %d: want EOF count %d, got %d", i, i, download.EOFRetryCount)
		}
	}

	if download.Status != domain.DownloadError {
		t.Fatalf("want ERROR after ten EOF retries, got %s", download.Status)
	}
	if download.NextAttemptAt != nil {
		t.Fatal("exhausted EOF retry should not schedule another attempt")
	}

	events, err := db.ListTaskEvents(context.Background(), task.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 10 {
		t.Fatalf("want 10 EOF history events, got %d", len(events))
	}
	if events[0].Type != "aria2.eof_exhausted" {
		t.Fatalf("latest event should be eof exhausted, got %s", events[0].Type)
	}
}

func TestManualRetryUsesDifferentDeterministicGID(t *testing.T) {
	initial := deterministicGIDForAttempt("task", "file", 0)
	retry1 := deterministicGIDForAttempt("task", "file", 1)
	retry1Again := deterministicGIDForAttempt("task", "file", 1)
	retry2 := deterministicGIDForAttempt("task", "file", 2)

	if initial == retry1 || retry1 == retry2 {
		t.Fatalf("manual retries must use different gids: %s %s %s", initial, retry1, retry2)
	}
	if retry1 != retry1Again {
		t.Fatalf("manual retry gid must remain deterministic: %s != %s", retry1, retry1Again)
	}
}
