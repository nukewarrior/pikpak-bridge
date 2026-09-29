package pikpak

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

const (
	fileKindFile   = "drive#file"
	fileKindFolder = "drive#folder"

	PhasePending  = "PHASE_TYPE_PENDING"
	PhaseRunning  = "PHASE_TYPE_RUNNING"
	PhaseComplete = "PHASE_TYPE_COMPLETE"
	PhaseError    = "PHASE_TYPE_ERROR"
)

type flexibleInt64 int64

func (v *flexibleInt64) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*v = 0
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		if s == "" {
			*v = 0
			return nil
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return fmt.Errorf("parse quoted int64 %q: %w", s, err)
		}
		*v = flexibleInt64(n)
		return nil
	}
	var n int64
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*v = flexibleInt64(n)
	return nil
}

type quotaValue struct {
	Limit flexibleInt64 `json:"limit"`
	Usage flexibleInt64 `json:"usage"`
}

type quotaMessage struct {
	Quota quotaValue `json:"quota"`
	Quotas struct {
		CloudDownload quotaValue `json:"cloud_download"`
	} `json:"quotas"`
}

type fileStat struct {
	Kind         string        `json:"kind"`
	ID           string        `json:"id"`
	ParentID     string        `json:"parent_id"`
	Name         string        `json:"name"`
	Size         flexibleInt64 `json:"size"`
	CreatedTime  time.Time     `json:"created_time"`
	ModifiedTime time.Time     `json:"modified_time"`
	Phase        string        `json:"phase"`
	Trashed      bool          `json:"trashed"`
}

type fileDetails struct {
	fileStat
	WebContentLink string `json:"web_content_link"`
	Links struct {
		ApplicationOctetStream struct {
			URL string `json:"url"`
		} `json:"application/octet-stream"`
	} `json:"links"`
	Medias []struct {
		Link struct {
			URL string `json:"url"`
		} `json:"link"`
	} `json:"medias"`
}

type offlineTaskAPI struct {
	ID       string        `json:"id"`
	FileID   string        `json:"file_id"`
	FileName string        `json:"file_name"`
	FileSize flexibleInt64 `json:"file_size"`
	Name     string        `json:"name"`
	Phase    string        `json:"phase"`
	Progress int64         `json:"progress"`
	Message  string        `json:"message"`
	Params   struct {
		URL string `json:"url"`
	} `json:"params"`
}

type offlineDownloadResponse struct {
	Task offlineTaskAPI `json:"task"`
}

type offlineListResponse struct {
	NextPageToken string           `json:"next_page_token"`
	Tasks         []offlineTaskAPI `json:"tasks"`
}

type filesResponse struct {
	NextPageToken string     `json:"next_page_token"`
	Files         []fileStat `json:"files"`
}
