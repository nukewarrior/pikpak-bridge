package pikpak

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

func TestFlexibleInt64(t *testing.T) {
	var a struct {
		Value flexibleInt64 `json:"value"`
	}
	for _, input := range []string{`{"value":"123"}`, `{"value":123}`} {
		if err := json.Unmarshal([]byte(input), &a); err != nil {
			t.Fatal(err)
		}
		if int64(a.Value) != 123 {
			t.Fatalf("want 123, got %d", a.Value)
		}
	}
}

func TestClassifyAPIError(t *testing.T) {
	if got := KindOf(classifyAPIError("x", 16, 200, "expired")); got != ErrorKindAuth {
		t.Fatalf("want auth, got %s", got)
	}
	if got := KindOf(classifyAPIError("x", 0, 200, "cloud download quota exceeded")); got != ErrorKindQuota {
		t.Fatalf("want quota, got %s", got)
	}
	if got := KindOf(classifyAPIError("x", 0, 200, "storage space full")); got != ErrorKindStorage {
		t.Fatalf("want storage, got %s", got)
	}
}

func TestPickDownloadURL(t *testing.T) {
	var f fileDetails
	f.WebContentLink = "web"
	f.Links.ApplicationOctetStream.URL = "octet"
	if got := pickDownloadURL(f); got != "octet" {
		t.Fatalf("want octet, got %q", got)
	}
}

func TestSessionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := sessionData{
		AccessToken:  "a",
		RefreshToken: "r",
		CaptchaToken: "c",
		UserID:       "u",
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
	}
	if err := saveSession(dir, "user@example.com", want); err != nil {
		t.Fatal(err)
	}
	got, err := loadSession(dir, "user@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken {
		t.Fatalf("unexpected session: %#v", got)
	}
	info, err := os.Stat(sessionPath(dir, "user@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("want session mode 0600, got %o", info.Mode().Perm())
	}
	if got.expired(time.Now()) {
		t.Fatal(errors.New("fresh session reported expired"))
	}
}
