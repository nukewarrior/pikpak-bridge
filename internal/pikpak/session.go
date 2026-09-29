package pikpak

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const sessionExpirySkew = 5 * time.Minute

type sessionData struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	CaptchaToken string `json:"captcha_token"`
	UserID       string `json:"user_id"`
	ExpiresAt    int64  `json:"expires_at"`
}

func (s sessionData) expired(now time.Time) bool {
	return s.AccessToken == "" || now.Unix() >= s.ExpiresAt
}

func sessionPath(dir, username string) string {
	sum := sha256.Sum256([]byte(username))
	return filepath.Join(dir, hex.EncodeToString(sum[:])+".json")
}

func loadSession(dir, username string) (sessionData, error) {
	var out sessionData
	if dir == "" {
		return out, os.ErrNotExist
	}
	data, err := os.ReadFile(sessionPath(dir, username))
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return sessionData{}, err
	}
	return out, nil
}

func saveSession(dir, username string, data sessionData) error {
	if dir == "" {
		return errors.New("session directory is empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	target := sessionPath(dir, username)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
