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
}

func (f *fakeRuntime) Configured() bool { return f.configured }
func (f *fakeRuntime) ApplySetup(_ context.Context, cfg *config.Config) error {
	f.applied = cfg
	f.configured = true
	return nil
}
func (f *fakeRuntime) AccountNames() []string { return nil }
func (f *fakeRuntime) RefreshAccount(context.Context, string) (pikpak.AccountSnapshot, error) {
	return pikpak.AccountSnapshot{}, nil
}
func (f *fakeRuntime) Aria2Snapshots(context.Context) []aria2.InstanceSnapshot { return nil }

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
		"pikpak_accounts":[{"name":"pp01","username":"user","password":"pass"}],
		"aria2_instances":[{"name":"nas","url":"http://aria2:6800/jsonrpc","secret":"rpc","dir":"/downloads","max_active":4,"weight":10}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", res.Code, res.Body.String())
	}
	if runtime.applied == nil || len(runtime.applied.PikPak.Accounts) != 1 || len(runtime.applied.Aria2.Instances) != 1 {
		t.Fatalf("setup was not applied: %#v", runtime.applied)
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
