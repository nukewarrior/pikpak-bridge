package httpapi

import (
	"context"
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

func TestWebUIRoutes(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	server := New(db)

	for _, tc := range []struct {
		path        string
		contentType string
		contains    string
	}{
		{path: "/", contentType: "text/html", contains: "setupForm"},
		{path: "/assets/styles.css", contentType: "text/css", contains: "setup-screen"},
		{path: "/assets/app.js", contentType: "javascript", contains: "bootstrap"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
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
	db, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	runtime := &fakeRuntime{}
	server := New(db, WithRuntime(runtime))

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/setup", nil)
	statusRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(statusRes, statusReq)
	if statusRes.Code != http.StatusOK || !strings.Contains(statusRes.Body.String(), `"configured":false`) {
		t.Fatalf("unexpected setup status: %d %s", statusRes.Code, statusRes.Body.String())
	}

	body := `{
		"pikpak_accounts":[{"id":"pp01","name":"主账号","username":"user","password":"pass","max_jobs":2}],
		"aria2_instances":[{"id":"nas","name":"NAS","url":"http://aria2:6800/jsonrpc","secret":"rpc","max_active":4}],
		"targets":[{"id":"movies","name":"电影","aria2_instance":"nas","dir":"/downloads/movies","default":true}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", res.Code, res.Body.String())
	}
	if runtime.applied == nil ||
		len(runtime.applied.PikPak.Accounts) != 1 ||
		len(runtime.applied.Aria2.Instances) != 1 ||
		len(runtime.applied.Targets) != 1 {
		t.Fatalf("setup was not applied: %#v", runtime.applied)
	}
	if runtime.applied.Targets[0].Aria2InstanceID != "nas" {
		t.Fatalf("unexpected target: %#v", runtime.applied.Targets[0])
	}

	second := httptest.NewRecorder()
	server.Handler().ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(body)))
	if second.Code != http.StatusConflict {
		t.Fatalf("second setup: want 409, got %d", second.Code)
	}
}

func TestTaskCreationBlockedBeforeSetup(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	server := New(db, WithRuntime(&fakeRuntime{}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks",
		strings.NewReader(`{"url":"https://example.com/file"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", res.Code)
	}
}

func TestTaskCreationSnapshotsTargetAndDedupesSource(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	runtime := &fakeRuntime{
		configured: true,
		targets: []config.DownloadTarget{
			{ID: "movies", Name: "电影", Aria2InstanceID: "nas", Dir: "/media/movies", Default: true},
			{ID: "temp", Name: "临时", Aria2InstanceID: "nas", Dir: "/downloads/temp"},
		},
	}
	server := New(db, WithRuntime(runtime))
	source := "magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567"

	create := func(target string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks",
			strings.NewReader(`{"url":"`+source+`","target":"`+target+`"}`))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
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

	duplicate := create("movies")
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("same target: want 409, got %d", duplicate.Code)
	}

	otherTarget := create("temp")
	if otherTarget.Code != http.StatusConflict {
		t.Fatalf("different target: same source must remain 409, got %d: %s", otherTarget.Code, otherTarget.Body.String())
	}
}
