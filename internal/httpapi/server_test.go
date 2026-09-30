package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nukewarrior/pikpak-bridge/internal/aria2"
	"github.com/nukewarrior/pikpak-bridge/internal/config"
	"github.com/nukewarrior/pikpak-bridge/internal/pikpak"
	"github.com/nukewarrior/pikpak-bridge/internal/store"
)

type fakeRuntime struct {
	configured bool
	applied    *config.Config
	targets    []config.DownloadTarget
}

func (f *fakeRuntime) Configured() bool { return f.configured }

func (f *fakeRuntime) ApplySetup(_ context.Context, cfg *config.Config) error {
	f.applied = cfg
	f.targets = cfg.Targets
	f.configured = true
	return nil
}

func (f *fakeRuntime) AccountIDs() []string { return nil }

func (f *fakeRuntime) RefreshAccount(context.Context, string) (pikpak.AccountSnapshot, error) {
	return pikpak.AccountSnapshot{}, nil
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

func TestWebUIRoutes(t *testing.T) {
	server := New(openTestStore(t))
	for _, tc := range []struct {
		path        string
		contentType string
		contains    string
	}{
		{"/", "text/html", "setupForm"},
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
		!strings.Contains(first.Body.String(), `"download_dir":"/media/movies"`) {
		t.Fatalf("task did not snapshot target: %s", first.Body.String())
	}

	if got := create("movies"); got.Code != http.StatusConflict {
		t.Fatalf("same target: want 409, got %d", got.Code)
	}
	if got := create("temp"); got.Code != http.StatusConflict {
		t.Fatalf("different target: same source must remain 409, got %d", got.Code)
	}
}
