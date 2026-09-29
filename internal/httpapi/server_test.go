package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nukewarrior/pikpak-bridge/internal/store"
)

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
		{path: "/", contentType: "text/html", contains: "PikPak Bridge"},
		{path: "/assets/styles.css", contentType: "text/css", contains: "--accent"},
		{path: "/assets/app.js", contentType: "javascript", contains: "loadTasks"},
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

func TestRuntimeStatusWithoutProviders(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	server := New(db)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), "pikpak_accounts") ||
		!strings.Contains(res.Body.String(), "aria2_instances") {
		t.Fatalf("unexpected status response: %s", res.Body.String())
	}
}
