package pikpak

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nukewarrior/pikpak-bridge/internal/config"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestManagerParallelDownloadURLsShareSafeCredentials(t *testing.T) {
	manager := NewManager([]config.PikPakAccount{{ID:"p1", Username:"user", Password:"password"}}, t.TempDir())
	entry := manager.accounts["p1"]
	entry.client.accessToken = "existing-token"
	entry.client.expiresAt = time.Now().Add(time.Hour).Unix()
	var mu sync.Mutex
	active, peak := 0, 0
	entry.client.httpClient.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Authorization") != "Bearer existing-token" {
			t.Errorf("wrong auth header %q", req.Header.Get("Authorization"))
		}
		mu.Lock()
		active++
		if active > peak { peak = active }
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"web_content_link":"https://download.example/file"}`)),
		}, nil
	})
	var wg sync.WaitGroup
	for i:=0;i<8;i++ {
		wg.Add(1)
		go func(){
			defer wg.Done()
			got, err := manager.GetDownloadURL(context.Background(), "p1", "file1")
			if err!=nil || got!="https://download.example/file" { t.Errorf("link=%q err=%v",got,err) }
		}()
	}
	wg.Wait()
	if peak <= 1 { t.Fatalf("PikPak requests are still serialized (peak=%d)", peak) }
}

func TestManagerReadOnlyTokenFailureFallsBackToSerialRefresh(t *testing.T) {
	manager := NewManager([]config.PikPakAccount{{ID:"p1", Username:"user", Password:"password"}}, t.TempDir())
	entry := manager.accounts["p1"]
	entry.client.accessToken = "old-token"
	entry.client.refreshToken = "refresh-token"
	entry.client.expiresAt = time.Now().Add(time.Hour).Unix()
	var mu sync.Mutex
	refreshes := 0
	entry.client.httpClient.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, `{"web_content_link":"https://download.example/file"}`
		switch {
		case strings.Contains(req.URL.Path, "/v1/auth/token"):
			mu.Lock()
			refreshes++
			mu.Unlock()
			body = `{"access_token":"new-token","refresh_token":"new-refresh","expires_in":3600}`
		case req.Header.Get("Authorization") == "Bearer old-token":
			status, body = http.StatusUnauthorized, `{"error":"invalid token"}`
		case req.Header.Get("Authorization") != "Bearer new-token":
			t.Errorf("unexpected authorization %q", req.Header.Get("Authorization"))
		}
		return &http.Response{StatusCode:status, Header:make(http.Header), Body:io.NopCloser(strings.NewReader(body))}, nil
	})
	got, err := manager.GetDownloadURL(context.Background(), "p1", "file1")
	if err != nil || got != "https://download.example/file" { t.Fatalf("fallback link=%q err=%v", got, err) }
	if refreshes != 1 { t.Fatalf("want one serialized token refresh, got %d", refreshes) }
}
