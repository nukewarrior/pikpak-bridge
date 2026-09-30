package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

var btihHex = regexp.MustCompile(`(?i)^[a-f0-9]{40}$`)

type NormalizedSource struct {
	Source     string
	SourceType string
	SourceKey  string
}

func NormalizeSource(input string) (NormalizedSource, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return NormalizedSource{}, errors.New("source is empty")
	}

	if btihHex.MatchString(input) {
		hash := strings.ToUpper(input)
		return NormalizedSource{
			Source:     "magnet:?xt=urn:btih:" + hash,
			SourceType: "magnet",
			SourceKey:  "btih:" + hash,
		}, nil
	}

	u, err := url.Parse(input)
	if err != nil {
		return NormalizedSource{}, fmt.Errorf("parse source: %w", err)
	}

	switch strings.ToLower(u.Scheme) {
	case "magnet":
		for _, xt := range u.Query()["xt"] {
			const prefix = "urn:btih:"
			if strings.HasPrefix(strings.ToLower(xt), prefix) {
				hash := strings.TrimSpace(xt[len(prefix):])
				if btihHex.MatchString(hash) {
					hash = strings.ToUpper(hash)
					return NormalizedSource{
						Source:     input,
						SourceType: "magnet",
						SourceKey:  "btih:" + hash,
					}, nil
				}
			}
		}
		return NormalizedSource{}, errors.New("magnet link has no 40-hex BTIH")
	case "http", "https":
		sum := sha256.Sum256([]byte(input))
		return NormalizedSource{
			Source:     input,
			SourceType: strings.ToLower(u.Scheme),
			SourceKey:  "url:" + hex.EncodeToString(sum[:]),
		}, nil
	case "ed2k":
		sum := sha256.Sum256([]byte(input))
		return NormalizedSource{
			Source:     input,
			SourceType: "ed2k",
			SourceKey:  "ed2k:" + hex.EncodeToString(sum[:]),
		}, nil
	default:
		return NormalizedSource{}, fmt.Errorf("unsupported source scheme %q", u.Scheme)
	}
}

func InitialTaskName(source, sourceType string) string {
	source = strings.TrimSpace(source)
	switch strings.ToLower(strings.TrimSpace(sourceType)) {
	case "magnet":
		if u, err := url.Parse(source); err == nil {
			if name := strings.TrimSpace(u.Query().Get("dn")); name != "" {
				return name
			}
		}
		return "Magnet 下载任务"
	case "http", "https":
		if u, err := url.Parse(source); err == nil {
			if p := strings.TrimSuffix(u.Path, "/"); p != "" {
				name := path.Base(p)
				if decoded, err := url.PathUnescape(name); err == nil {
					name = decoded
				}
				if name = strings.TrimSpace(name); name != "" && name != "." && name != "/" {
					return name
				}
			}
			if host := strings.TrimSpace(u.Hostname()); host != "" {
				return host
			}
		}
	case "ed2k":
		parts := strings.Split(source, "|")
		if len(parts) > 2 {
			name := strings.TrimSpace(parts[2])
			if decoded, err := url.PathUnescape(name); err == nil {
				name = decoded
			}
			if name = strings.TrimSpace(name); name != "" {
				return name
			}
		}
		return "ED2K 下载任务"
	}
	return "下载任务"
}

func NewTaskID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x-%s", time.Now().UTC().UnixMilli(), hex.EncodeToString(b[:])), nil
}
