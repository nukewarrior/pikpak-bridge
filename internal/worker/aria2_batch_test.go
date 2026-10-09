package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nukewarrior/pikpak-bridge/internal/aria2"
	"github.com/nukewarrior/pikpak-bridge/internal/domain"
	"github.com/nukewarrior/pikpak-bridge/internal/store"
)

type batchFakeAria2 struct {
	fakeAria2
	batches int
	maxBatch int
	failOnce map[string]bool
}

func (b *batchFakeAria2) TellStatusBatch(ctx context.Context, instanceID string, gids []string) ([]aria2.StatusResult, error) {
	results := make([]aria2.StatusResult, len(gids))
	for i, gid := range gids {
		results[i].Status, results[i].Err = b.TellStatus(ctx, instanceID, gid)
	}
	return results, nil
}

func (b *batchFakeAria2) AddBatch(ctx context.Context, instanceID, baseDir string, requests []aria2.AddRequest) ([]aria2.AddResult, error) {
	b.batches++
	if len(requests) > b.maxBatch { b.maxBatch = len(requests) }
	results := make([]aria2.AddResult, len(requests))
	for i, req := range requests {
		if b.failOnce[req.RelativePath] {
			delete(b.failOnce, req.RelativePath)
			results[i].Err = errors.New("temporary add failure")
			continue
		}
		results[i].GID, results[i].Err = b.Add(ctx, instanceID, baseDir, req.URI, req.GID, req.RelativePath, req.Overwrite)
	}
	return results, nil
}

type limitedURLProvider struct {
	fakeURLProvider
	mu sync.Mutex
	active int
	peak int
}

func (p *limitedURLProvider) GetDownloadURL(ctx context.Context, accountID, fileID string) (string, error) {
	p.mu.Lock()
	p.active++
	if p.active > p.peak { p.peak = p.active }
	p.mu.Unlock()
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Millisecond):
	}
	p.mu.Lock()
	p.active--
	p.mu.Unlock()
	return "https://example.invalid/" + fileID, ctx.Err()
}

func prepareImageFolderTask(t *testing.T, db *store.SQLite, taskID string, count int) domain.Task {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	task := domain.Task{
		ID: taskID, Source: "magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567",
		SourceType: "magnet", SourceKey: "btih:0123456789ABCDEF0123456789ABCDEF01234567",
		TargetID: "movies", TargetName: "电影", Aria2InstanceID: "a1",
		DownloadDir: "/downloads/movies", Status: domain.TaskWaitingAria2,
		PikPakAccountID: "pp1", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.CreateTask(ctx, task); err != nil { t.Fatal(err) }
	task, err := db.GetTask(ctx, taskID)
	if err != nil { t.Fatal(err) }
	task.Status = domain.TaskWaitingAria2
	task.PikPakAccountID = "pp1"
	if err := db.SaveTask(ctx, &task, "", ""); err != nil { t.Fatal(err) }
	files := make([]domain.RemoteFile, count)
	for i := range files {
		name := fmt.Sprintf("img-%03d.jpg", i)
		files[i] = domain.RemoteFile{
			TaskID: task.ID, PikPakFileID: fmt.Sprintf("file-%03d", i),
			Name: name, RelativePath: "Collection/" + name, Size: 1234,
		}
	}
	if err := db.ReplaceRemoteFiles(ctx, taskID, files); err != nil { t.Fatal(err) }
	return task
}

func TestBatchedFolderBoundedAndConcurrentAndResumable(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/batch-folder.db")
	if err != nil { t.Fatal(err) }
	defer db.Close()
	task := prepareImageFolderTask(t, db, "batch-folder", 117)
	// Simulate aria2 accepting an item just before a previous worker crashed.
	existingGID := deterministicGIDForAttempt(task.ID, "file-000", 0)
	backend := &batchFakeAria2{}
	backend.added = map[string]bool{existingGID: true}
	urls := &limitedURLProvider{}
	w := NewAria2(db, urls, backend, Aria2Options{StatusInterval: time.Millisecond, RetryInterval: time.Millisecond, MaxRetry: 3})
	if err := w.RunOnce(context.Background()); err != nil { t.Fatal(err) }
	if backend.addCalls > maxOutstandingDownloads { t.Fatalf("too many initial jobs: %d", backend.addCalls) }
	if backend.added[existingGID] == false { t.Fatal("existing gid overwritten") }
	if urls.peak < 2 || urls.peak > pikpakLinkConcurrency {
		t.Fatalf("expected bounded parallel URL resolution, got peak %d", urls.peak)
	}
	for i := 0; i < 15; i++ {
		time.Sleep(2 * time.Millisecond)
		if err := w.RunOnce(context.Background()); err != nil { t.Fatal(err) }
		got, err := db.GetTask(context.Background(), task.ID)
		if err != nil { t.Fatal(err) }
		if got.Status == domain.TaskVerifying { break }
	}
	got, err := db.GetTask(context.Background(), task.ID)
	if err != nil { t.Fatal(err) }
	if got.Status != domain.TaskVerifying { t.Fatalf("want VERIFYING, got %s", got.Status) }
	if backend.addCalls != 116 { t.Fatalf("expected 116 new submissions, got %d", backend.addCalls) }
	if backend.maxBatch > maxOutstandingDownloads || backend.maxBatch < 2 {
		t.Fatalf("unexpected batch size: %d", backend.maxBatch)
	}
	downloads, err := db.ListDownloads(context.Background(), task.ID)
	if err != nil { t.Fatal(err) }
	for _, d := range downloads {
		if d.Status != domain.DownloadComplete { t.Fatalf("unfinished %s: %s", d.RelativePath, d.Status) }
	}
}

func TestBatchPartialFailureRetriesOnlyFailedFile(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/partial.db")
	if err != nil { t.Fatal(err) }
	defer db.Close()
	task := prepareImageFolderTask(t, db, "partial", 3)
	backend := &batchFakeAria2{failOnce: map[string]bool{"Collection/img-001.jpg": true}}
	w := NewAria2(db, fakeURLProvider{}, backend, Aria2Options{StatusInterval: time.Millisecond, RetryInterval: time.Millisecond, MaxRetry: 3})
	for i := 0; i < 8; i++ {
		if err := w.RunOnce(context.Background()); err != nil { t.Fatal(err) }
		time.Sleep(2 * time.Millisecond)
	}
	download, err := db.ListDownloads(context.Background(), task.ID)
	if err != nil { t.Fatal(err) }
	for _, d := range download {
		if d.Status != domain.DownloadComplete { t.Fatalf("file %s remains %s", d.RelativePath, d.Status) }
	}
	if backend.addCalls != 3 { t.Fatalf("successful files should not be resubmitted, add calls = %d", backend.addCalls) }
}
