package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nukewarrior/pikpak-bridge/internal/aria2"
	"github.com/nukewarrior/pikpak-bridge/internal/config"
	"github.com/nukewarrior/pikpak-bridge/internal/domain"
	"github.com/nukewarrior/pikpak-bridge/internal/pikpak"
	"github.com/nukewarrior/pikpak-bridge/internal/store"
)

type fakeRuntime struct {
	configured       bool
	applied          *config.Config
	cfg              *config.Config
	targets          []config.DownloadTarget
	cancelledPikPak  []string
	deletedPikPak    []string
	cancelledAria2   []string
}

func (f *fakeRuntime) Configured() bool { return f.configured }

func (f *fakeRuntime) CurrentConfig() *config.Config {
	if f.cfg != nil {
		return cloneTestConfig(f.cfg)
	}
	if f.applied != nil {
		return cloneTestConfig(f.applied)
	}
	return nil
}

func (f *fakeRuntime) ApplySetup(_ context.Context, cfg *config.Config) error {
	f.applied = cloneTestConfig(cfg)
	f.cfg = cloneTestConfig(cfg)
	f.targets = append([]config.DownloadTarget(nil), cfg.Targets...)
	f.configured = true
	return nil
}

func (f *fakeRuntime) ApplyConfig(_ context.Context, cfg *config.Config) error {
	f.applied = cloneTestConfig(cfg)
	f.cfg = cloneTestConfig(cfg)
	f.targets = append([]config.DownloadTarget(nil), cfg.Targets...)
	f.configured = true
	return nil
}

func (f *fakeRuntime) AccountIDs() []string { return nil }

func (f *fakeRuntime) RefreshAccount(context.Context, string) (pikpak.AccountSnapshot, error) {
	return pikpak.AccountSnapshot{}, nil
}

func (f *fakeRuntime) CancelPikPakOffline(_ context.Context, accountID, taskID string) error {
	f.cancelledPikPak = append(f.cancelledPikPak, accountID+":"+taskID)
	return nil
}

func (f *fakeRuntime) DeletePikPakFile(_ context.Context, accountID, fileID string) error {
	f.deletedPikPak = append(f.deletedPikPak, accountID+":"+fileID)
	return nil
}

func (f *fakeRuntime) CancelAria2(_ context.Context, instanceID, gid string) error {
	f.cancelledAria2 = append(f.cancelledAria2, instanceID+":"+gid)
	return nil
}

func (f *fakeRuntime) Aria2Snapshots(context.Context) []aria2.InstanceSnapshot { return nil }

func (f *fakeRuntime) DownloadTargets() []config.DownloadTarget { return f.targets }

func (f *fakeRuntime) ResolveTarget(id string) (config.DownloadTarget, error) {
	for _, target := range f.targets {
		if target.ID == id || (id == "" && target.Default) {
			return target, nil
		}
	}
	if id == "" && len(f.targets) == 1 {
		return f.targets[0], nil
	}
	return config.DownloadTarget{}, context.Canceled
}

func openTestStore(t *testing.T) *store.SQLite {
	t.Helper()
	db, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func testConfig() *config.Config {
	cfg := config.Default()
	cfg.PikPak.Accounts = []config.PikPakAccount{{
		ID: "pp01", Name: "Primary", Username: "user", Password: "account-secret", MaxJobs: 2,
	}}
	cfg.Aria2.Instances = []config.Aria2Instance{{
		ID: "nas", Name: "NAS", URL: "http://aria2:6800/jsonrpc", Secret: "rpc-secret",
	}}
	cfg.Targets = []config.DownloadTarget{{
		ID: "movies", Name: "Movies", Aria2InstanceID: "nas", Dir: "/downloads/movies", Default: true,
	}}
	return cfg
}

func cloneTestConfig(src *config.Config) *config.Config {
	if src == nil {
		return nil
	}
	dst := *src
	dst.PikPak.Accounts = append([]config.PikPakAccount(nil), src.PikPak.Accounts...)
	dst.Aria2.Instances = append([]config.Aria2Instance(nil), src.Aria2.Instances...)
	dst.Targets = append([]config.DownloadTarget(nil), src.Targets...)
	return &dst
}

func TestWebUIRoutes(t *testing.T) {
	server := New(openTestStore(t))
	for _, tc := range []struct {
		path        string
		contentType string
		contains    string
	}{
		{"/", "text/html", "resourceDialog"},
		{"/assets/styles.css", "text/css", "setup-screen"},
		{"/assets/app.js", "javascript", "bootstrap"},
	} {
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if res.Code != http.StatusOK {
			t.Fatalf("%s: want 200, got %d", tc.path, res.Code)
		}
		if !strings.Contains(res.Header().Get("Content-Type"), tc.contentType) {
			t.Fatalf("%s: unexpected content type %q", tc.path, res.Header().Get("Content-Type"))
		}
		if !strings.Contains(res.Body.String(), tc.contains) {
			t.Fatalf("%s: response missing %q", tc.path, tc.contains)
		}
	}
}

func TestFirstRunSetup(t *testing.T) {
	runtime := &fakeRuntime{}
	server := New(openTestStore(t), WithRuntime(runtime))

	payload := setupRequest{
		PikPakAccounts: []setupPikPakAccount{{
			ID: "pp01", Name: "Primary", Username: "user", Password: "p", MaxJobs: 2,
		}},
		Aria2Instances: []setupAria2Instance{{
			ID: "nas", Name: "NAS", URL: "http://aria2:6800/jsonrpc",
		}},
		Targets: []setupTarget{{
			ID: "movies", Name: "Movies", Aria2InstanceID: "nas", Dir: "/downloads/movies", Default: true,
		}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", res.Code, res.Body.String())
	}
	if runtime.applied == nil || len(runtime.applied.Targets) != 1 {
		t.Fatalf("setup was not applied: %#v", runtime.applied)
	}
	if runtime.applied.Targets[0].Aria2InstanceID != "nas" {
		t.Fatalf("unexpected target: %#v", runtime.applied.Targets[0])
	}

	second := httptest.NewRecorder()
	server.Handler().ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/api/v1/setup", bytes.NewReader(body)))
	if second.Code != http.StatusConflict {
		t.Fatalf("second setup: want 409, got %d", second.Code)
	}
}

func TestConfigReadRedactsSecrets(t *testing.T) {
	cfg := testConfig()
	runtime := &fakeRuntime{configured: true, cfg: cfg, targets: cfg.Targets}
	server := New(openTestStore(t), WithRuntime(runtime))

	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if strings.Contains(body, "account-secret") || strings.Contains(body, "rpc-secret") {
		t.Fatalf("configuration response leaked a secret: %s", body)
	}
	if !strings.Contains(body, `"password_set":true`) || !strings.Contains(body, `"secret_set":true`) {
		t.Fatalf("configuration response did not report stored secrets: %s", body)
	}
}

func TestConfigUpdatePreservesBlankSecrets(t *testing.T) {
	cfg := testConfig()
	runtime := &fakeRuntime{configured: true, cfg: cfg, targets: cfg.Targets}
	server := New(openTestStore(t), WithRuntime(runtime))

	payload := setupRequest{
		PikPakAccounts: []setupPikPakAccount{{
			ID: "pp01", Name: "Renamed", Username: "user", Password: "", MaxJobs: 3,
		}},
		Aria2Instances: []setupAria2Instance{{
			ID: "nas", Name: "NAS Updated", URL: "http://aria2-new:6800/jsonrpc", Secret: "",
		}},
		Targets: []setupTarget{{
			ID: "movies", Name: "Movies", Aria2InstanceID: "nas", Dir: "/media/movies", Default: true,
		}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", res.Code, res.Body.String())
	}
	if runtime.cfg.PikPak.Accounts[0].Password != "account-secret" {
		t.Fatal("blank password replaced the stored PikPak password")
	}
	if runtime.cfg.Aria2.Instances[0].Secret != "rpc-secret" {
		t.Fatal("blank secret replaced the stored aria2 secret")
	}
	if runtime.cfg.PikPak.Accounts[0].Name != "Renamed" || runtime.cfg.PikPak.Accounts[0].MaxJobs != 3 {
		t.Fatalf("PikPak account update was not applied: %#v", runtime.cfg.PikPak.Accounts[0])
	}
	if runtime.cfg.Aria2.Instances[0].URL != "http://aria2-new:6800/jsonrpc" {
		t.Fatalf("aria2 update was not applied: %#v", runtime.cfg.Aria2.Instances[0])
	}
	if runtime.cfg.Targets[0].Dir != "/media/movies" {
		t.Fatalf("target update was not applied: %#v", runtime.cfg.Targets[0])
	}
}

func TestConfigUpdateRejectsRemovingActivePikPakAccount(t *testing.T) {
	db := openTestStore(t)
	cfg := testConfig()
	runtime := &fakeRuntime{configured: true, cfg: cfg, targets: cfg.Targets}
	server := New(db, WithRuntime(runtime))

	now := time.Now().UTC()
	task := domain.Task{
		ID: "active-task", Source: "https://example.invalid/file", SourceType: "https", SourceKey: "url:active",
		TargetID: "movies", TargetName: "Movies", Aria2InstanceID: "nas", DownloadDir: "/downloads/movies",
		Status: domain.TaskQueued, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	task, err := db.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	task.Status = domain.TaskPikPakRunning
	task.PikPakAccountID = "pp01"
	if err := db.SaveTask(context.Background(), &task, "", ""); err != nil {
		t.Fatal(err)
	}

	payload := setupRequest{
		PikPakAccounts: []setupPikPakAccount{{
			ID: "pp02", Name: "Other", Username: "other", Password: "other-secret", MaxJobs: 2,
		}},
		Aria2Instances: []setupAria2Instance{{
			ID: "nas", Name: "NAS", URL: "http://aria2:6800/jsonrpc",
		}},
		Targets: []setupTarget{{
			ID: "movies", Name: "Movies", Aria2InstanceID: "nas", Dir: "/downloads/movies", Default: true,
		}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "pp01") {
		t.Fatalf("conflict did not identify the active account: %s", res.Body.String())
	}
}

func TestTaskCreationBlockedBeforeSetup(t *testing.T) {
	server := New(openTestStore(t), WithRuntime(&fakeRuntime{}))
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks",
		strings.NewReader(`{"url":"https://example.com/file"}`))
	req.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", res.Code)
	}
}

func TestTaskCreationSnapshotsTargetAndDedupesSource(t *testing.T) {
	runtime := &fakeRuntime{
		configured: true,
		targets: []config.DownloadTarget{
			{ID: "movies", Name: "Movies", Aria2InstanceID: "nas", Dir: "/media/movies", Default: true},
			{ID: "temp", Name: "Temp", Aria2InstanceID: "nas", Dir: "/downloads/temp"},
		},
	}
	server := New(openTestStore(t), WithRuntime(runtime))
	source := "magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567"

	create := func(target string) *httptest.ResponseRecorder {
		body, err := json.Marshal(createTaskRequest{URL: source, Target: target})
		if err != nil {
			t.Fatal(err)
		}
		res := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		server.Handler().ServeHTTP(res, req)
		return res
	}

	first := create("movies")
	if first.Code != http.StatusCreated {
		t.Fatalf("movies: want 201, got %d: %s", first.Code, first.Body.String())
	}
	if !strings.Contains(first.Body.String(), `"target_id":"movies"`) ||
		!strings.Contains(first.Body.String(), `"download_dir":"/media/movies"`) ||
		!strings.Contains(first.Body.String(), `"name":"Magnet 下载任务"`) {
		t.Fatalf("task did not snapshot target/name: %s", first.Body.String())
	}

	if got := create("movies"); got.Code != http.StatusConflict {
		t.Fatalf("same target: want 409, got %d", got.Code)
	}
	if got := create("temp"); got.Code != http.StatusConflict {
		t.Fatalf("different target: same source must remain 409, got %d", got.Code)
	}
}


func TestRetryFailedAria2TaskResetsCountersAndKeepsHistory(t *testing.T) {
	db := openTestStore(t)
	server := New(db)

	now := time.Now().UTC()
	task := domain.Task{
		ID:              "failed-aria2",
		Source:          "https://example.invalid/file",
		SourceType:      "https",
		SourceKey:       "url:failed-aria2",
		TargetID:        "movies",
		TargetName:      "Movies",
		Aria2InstanceID: "nas",
		DownloadDir:     "/downloads/movies",
		Status:          domain.TaskAria2Failed,
		PikPakAccountID: "pp01",
		PikPakTaskID:    "remote-task",
		PikPakRootFileID:"root-file",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	task, err := db.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	task.Status = domain.TaskAria2Failed
	task.RetryCount = 10
	task.Error = "aria2 下载失败"
	if err := db.SaveTask(context.Background(), &task, "aria2.failed", task.Error); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceRemoteFiles(context.Background(), task.ID, []domain.RemoteFile{{
		TaskID: task.ID, PikPakFileID: "file-1", Name: "movie.mkv",
		RelativePath: "folder/movie.mkv", Size: 1234,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureDownloads(context.Background(), task.ID, "nas", func(_, _ string) string { return "0123456789abcdef" }); err != nil {
		t.Fatal(err)
	}
	downloads, err := db.ListDownloads(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	download := downloads[0]
	download.Status = domain.DownloadError
	download.RetryCount = 10
	download.EOFRetryCount = 10
	download.LastError = "Failed to receive data, cause: EOF was received"
	if err := db.SaveDownload(context.Background(), &download); err != nil {
		t.Fatal(err)
	}
	if err := db.AddTaskEvent(context.Background(), task.ID, "aria2.eof_exhausted", "旧错误历史"); err != nil {
		t.Fatal(err)
	}

	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+task.ID+"/retry", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", res.Code, res.Body.String())
	}

	got, err := db.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.TaskWaitingAria2 {
		t.Fatalf("want WAITING_ARIA2, got %s", got.Status)
	}
	if got.RetryCount != 0 || got.ManualRetryCount != 1 || got.Error != "" {
		t.Fatalf("task retry counters were not reset: %#v", got)
	}

	downloads, err = db.ListDownloads(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(downloads) != 1 {
		t.Fatalf("unexpected downloads: %#v", downloads)
	}
	if downloads[0].Status != domain.DownloadPending ||
		downloads[0].RetryCount != 0 ||
		downloads[0].EOFRetryCount != 0 ||
		downloads[0].Aria2GID != "" {
		t.Fatalf("download retry state was not reset: %#v", downloads[0])
	}

	eventsRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(eventsRes, httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+task.ID+"/events?limit=20", nil))
	if eventsRes.Code != http.StatusOK {
		t.Fatalf("events: want 200, got %d: %s", eventsRes.Code, eventsRes.Body.String())
	}
	body := eventsRes.Body.String()
	if !strings.Contains(body, "旧错误历史") || !strings.Contains(body, "task.manual_retry") {
		t.Fatalf("retry did not preserve error history: %s", body)
	}
}

func TestRetryRejectsNonFailedTask(t *testing.T) {
	db := openTestStore(t)
	server := New(db)
	now := time.Now().UTC()
	task := domain.Task{
		ID: "active-retry", Source: "https://example.invalid/active", SourceType: "https",
		SourceKey: "url:active-retry", TargetID: "movies", TargetName: "Movies",
		Aria2InstanceID: "nas", DownloadDir: "/downloads/movies",
		Status: domain.TaskQueued, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+task.ID+"/retry", nil))
	if res.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", res.Code, res.Body.String())
	}
}


func TestCancelActiveTaskQueuesDurableCleanup(t *testing.T) {
	db := openTestStore(t)
	runtime := &fakeRuntime{}
	server := New(db, WithRuntime(runtime))

	now := time.Now().UTC()
	task := domain.Task{
		ID:              "cancel-active",
		Source:          "magnet:?xt=urn:btih:CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC",
		SourceType:      "magnet",
		SourceKey:       "btih:CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC",
		TargetID:        "movies",
		TargetName:      "Movies",
		Aria2InstanceID: "nas",
		DownloadDir:     "/downloads/movies",
		Status:          domain.TaskQueued,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	task, err := db.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	task.Status = domain.TaskAria2Downloading
	task.PikPakAccountID = "pp01"
	task.PikPakTaskID = "remote-task"
	task.PikPakRootFileID = "root-file"
	if err := db.SaveTask(context.Background(), &task, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceRemoteFiles(context.Background(), task.ID, []domain.RemoteFile{{
		TaskID: task.ID, PikPakFileID: "file-1", Name: "file.bin", RelativePath: "file.bin", Size: 123,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureDownloads(context.Background(), task.ID, "nas", func(_, _ string) string { return "0123456789abcdef" }); err != nil {
		t.Fatal(err)
	}
	downloads, err := db.ListDownloads(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	download := downloads[0]
	download.Status = domain.DownloadActive
	if err := db.SaveDownload(context.Background(), &download); err != nil {
		t.Fatal(err)
	}

	stale := task
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+task.ID+"/cancel", nil))
	if res.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", res.Code, res.Body.String())
	}
	got, err := db.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.TaskCancelling || got.CompletedAt != nil {
		t.Fatalf("task was not queued for cleanup: %#v", got)
	}
	if len(runtime.cancelledPikPak) != 0 || len(runtime.deletedPikPak) != 0 || len(runtime.cancelledAria2) != 0 {
		t.Fatal("HTTP request must not perform non-durable remote cleanup")
	}

	stale.Status = domain.TaskPikPakRunning
	if err := db.SaveTask(context.Background(), &stale, "", ""); err == nil {
		t.Fatal("stale worker update revived a cancelling task")
	}
	got, err = db.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.TaskCancelling {
		t.Fatalf("cancelling task was revived: %s", got.Status)
	}
}
