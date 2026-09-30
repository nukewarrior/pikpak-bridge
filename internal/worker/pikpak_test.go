package worker

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nukewarrior/pikpak-bridge/internal/domain"
	"github.com/nukewarrior/pikpak-bridge/internal/pikpak"
	"github.com/nukewarrior/pikpak-bridge/internal/store"
)

type fakeProvider struct {
	snapshot pikpak.AccountSnapshot
	submit   pikpak.OfflineTask
	rootName string
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
func (f *fakeProvider) ResolveFiles(context.Context, string, string) (pikpak.ResolvedFiles, error) {
	return pikpak.ResolvedFiles{RootName: f.rootName, Files: f.files}, nil
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
		rootName: "movie.mkv",
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
	if got.Name != "movie.mkv" {
		t.Fatalf("want resolved task name movie.mkv, got %q", got.Name)
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


type poolProvider struct {
	snapshots map[string]pikpak.AccountSnapshot
	submits   []string
}

func (p *poolProvider) RefreshAccount(_ context.Context, id string) (pikpak.AccountSnapshot, error) {
	return p.snapshots[id], nil
}

func (p *poolProvider) SubmitOffline(_ context.Context, id, _ string) (pikpak.OfflineTask, error) {
	p.submits = append(p.submits, id)
	return pikpak.OfflineTask{
		ID:     "remote-" + id,
		Status: pikpak.PhaseRunning,
	}, nil
}

func (p *poolProvider) GetOfflineTask(context.Context, string, string) (pikpak.OfflineTask, error) {
	return pikpak.OfflineTask{}, nil
}

func (p *poolProvider) ResolveFiles(context.Context, string, string) (pikpak.ResolvedFiles, error) {
	return pikpak.ResolvedFiles{}, nil
}

func (p *poolProvider) GetDownloadURL(context.Context, string, string) (string, error) {
	return "", nil
}

func (p *poolProvider) DeletePermanently(context.Context, string, string) error {
	return nil
}

func TestWorkerBalancesBurstAcrossAccountCapacity(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	for i := 1; i <= 3; i++ {
		task := domain.Task{
			ID:              fmt.Sprintf("task-%d", i),
			Source:          fmt.Sprintf("https://example.invalid/file-%d", i),
			SourceType:      "https",
			SourceKey:       fmt.Sprintf("url:%d", i),
			TargetID:        "default",
			TargetName:      "默认",
			Aria2InstanceID: "nas",
			DownloadDir:     "/downloads",
			Status:          domain.TaskQueued,
			CreatedAt:       now.Add(time.Duration(i) * time.Millisecond),
			UpdatedAt:       now.Add(time.Duration(i) * time.Millisecond),
		}
		if err := db.CreateTask(context.Background(), task); err != nil {
			t.Fatal(err)
		}
	}

	provider := &poolProvider{snapshots: map[string]pikpak.AccountSnapshot{
		"pp1": {
			ID: "pp1", Name: "PikPak 1", Enabled: true, Healthy: true,
			QuotaRemaining: 3, QuotaTotal: 3, StorageFree: 10_000_000_000, MaxJobs: 1,
		},
		"pp2": {
			ID: "pp2", Name: "PikPak 2", Enabled: true, Healthy: true,
			QuotaRemaining: 3, QuotaTotal: 3, StorageFree: 10_000_000_000, MaxJobs: 1,
		},
	}}
	w := New(db, provider, Options{
		AccountIDs:    []string{"pp1", "pp2"},
		QuotaRefresh:  time.Hour,
		RetryInterval: time.Minute,
		MaxRetry:      3,
	})

	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.submits) != 2 {
		t.Fatalf("want 2 submissions for two available slots, got %#v", provider.submits)
	}
	if provider.submits[0] != "pp1" || provider.submits[1] != "pp2" {
		t.Fatalf("want balanced pp1 then pp2, got %#v", provider.submits)
	}

	third, err := db.GetTask(context.Background(), "task-3")
	if err != nil {
		t.Fatal(err)
	}
	if third.Status != domain.TaskWaitingPikPakAccount {
		t.Fatalf("want third task waiting for account capacity, got %s", third.Status)
	}
}


func TestWorkerWaitsForQuotaRefreshWhenAllEnabledAccountsExhausted(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	task := domain.Task{
		ID:              "quota-wait",
		Source:          "magnet:?xt=urn:btih:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		SourceType:      "magnet",
		SourceKey:       "btih:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		TargetID:        "default",
		TargetName:      "默认",
		Aria2InstanceID: "nas",
		DownloadDir:     "/downloads",
		Status:          domain.TaskQueued,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	provider := &poolProvider{snapshots: map[string]pikpak.AccountSnapshot{
		"pp1": {
			ID: "pp1", Name: "PikPak 1", Enabled: true, Healthy: true,
			QuotaRemaining: 0, QuotaTotal: 3, StorageFree: 10_000_000_000, MaxJobs: 2,
		},
		"pp2": {
			ID: "pp2", Name: "PikPak 2", Enabled: true, Healthy: true,
			QuotaRemaining: 0, QuotaTotal: 10, StorageFree: 10_000_000_000, MaxJobs: 2,
		},
	}}
	quotaRefresh := 5 * time.Minute
	w := New(db, provider, Options{
		AccountIDs:    []string{"pp1", "pp2"},
		QuotaRefresh:  quotaRefresh,
		RetryInterval: 30 * time.Second,
		MaxRetry:      3,
	})

	before := time.Now().UTC()
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.TaskWaitingPikPakAccount {
		t.Fatalf("want %s, got %s", domain.TaskWaitingPikPakAccount, got.Status)
	}
	if got.Error != allQuotaExhaustedError {
		t.Fatalf("want quota exhausted error %q, got %q", allQuotaExhaustedError, got.Error)
	}
	if got.RetryCount != 0 {
		t.Fatalf("quota exhaustion must not consume task retries, got %d", got.RetryCount)
	}
	if got.NextAttemptAt == nil {
		t.Fatal("quota exhaustion should schedule the next quota refresh")
	}
	wait := got.NextAttemptAt.Sub(before)
	if wait < quotaRefresh-2*time.Second || wait > quotaRefresh+2*time.Second {
		t.Fatalf("want next attempt near quota refresh %s, got %s", quotaRefresh, wait)
	}
	if len(provider.submits) != 0 {
		t.Fatalf("quota-exhausted accounts must not receive submissions: %#v", provider.submits)
	}

	events, err := db.ListTaskEvents(context.Background(), task.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "pikpak.waiting_quota" {
		t.Fatalf("want one quota wait event, got %#v", events)
	}

	past := time.Now().UTC().Add(-time.Second)
	got.NextAttemptAt = &past
	if err := db.SaveTask(context.Background(), &got, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	events, err = db.ListTaskEvents(context.Background(), task.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("repeated quota checks must not duplicate wait events: %#v", events)
	}
}


func TestNextQuotaRefreshAtRespectsAccountCooldown(t *testing.T) {
	now := time.Now().UTC()
	w := New(nil, nil, Options{
		AccountIDs:     []string{"pp1"},
		QuotaRefresh:   5 * time.Minute,
		WorkerInterval: 2 * time.Second,
	})
	runtime := w.runtime("pp1")
	runtime.has = true
	runtime.snapshot = pikpak.AccountSnapshot{
		ID:             "pp1",
		Enabled:        true,
		QuotaRemaining: 0,
	}
	runtime.refreshed = now.Add(-10 * time.Minute)
	runtime.cooldown = now.Add(20 * time.Minute)

	got := w.nextQuotaRefreshAt(now)
	if !got.Equal(runtime.cooldown) {
		t.Fatalf("want cooldown end %s, got %s", runtime.cooldown, got)
	}
}
