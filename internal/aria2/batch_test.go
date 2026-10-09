package aria2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nukewarrior/pikpak-bridge/internal/config"
)

func TestRegistryBatchMulticallAndPartialErrors(t *testing.T) {
	const gid1 = "0123456789abcdef"
	const gid2 = "fedcba9876543210"
	var callsSeen int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var rpc struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(req.Body).Decode(&rpc); err != nil { t.Errorf("decode: %v", err); return }
		if rpc.Method != "system.multicall" { t.Errorf("unexpected method: %s", rpc.Method); return }
		var calls []struct {
			Method string `json:"methodName"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(rpc.Params[0], &calls); err != nil { t.Errorf("calls: %v", err); return }
		if len(calls) != 2 { t.Errorf("expected two calls, got %d", len(calls)); return }
		for i, call := range calls {
			var secret string
			if err := json.Unmarshal(call.Params[0], &secret); err != nil || secret != "token:secret" {
				t.Errorf("nested token %d: %q (%v)", i, secret, err)
			}
		}
		var results any
		switch calls[0].Method {
		case "aria2.addUri":
			for i, call := range calls {
				if call.Method != "aria2.addUri" { t.Errorf("mixed call: %s", call.Method) }
				var opts map[string]string
				if err := json.Unmarshal(call.Params[2], &opts); err != nil { t.Errorf("opts: %v", err) }
				if opts["remote-time"] != "false" || opts["dir"] != "/downloads/Album" || opts["auto-file-renaming"] != "false" {
					t.Errorf("bad options: %#v", opts)
				}
				if i == 0 && (opts["continue"] != "true" || opts["allow-overwrite"] != "") {
					t.Errorf("normal download changed: %#v", opts)
				}
				if i == 1 && (opts["continue"] != "false" || opts["allow-overwrite"] != "true") {
					t.Errorf("repeat download changed: %#v", opts)
				}
			}
			results = []any{[]string{gid1}, map[string]any{"code": 8, "message": "one file failed"}}
		case "aria2.tellStatus":
			results = []any{[]any{map[string]any{
				"gid": gid1, "status": "complete", "totalLength": "123", "completedLength": "123",
			}}, map[string]any{"code": 1, "message": "GID not found"}}
		default:
			t.Errorf("unexpected nested method: %s", calls[0].Method)
			return
		}
		callsSeen++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "result": results})
	}))
	defer server.Close()
	registry := NewRegistry([]config.Aria2Instance{{ID: "a1", Name: "main", URL: server.URL, Secret: "secret"}})
	added, err := registry.AddBatch(context.Background(), "a1", "/downloads", []AddRequest{
		{URI: "https://example.invalid/a", GID: gid1, RelativePath: "Album/1.jpg"},
		{URI: "https://example.invalid/b", GID: gid2, RelativePath: "Album/2.jpg", Overwrite: true},
	})
	if err != nil { t.Fatal(err) }
	if len(added) != 2 || added[0].GID != gid1 || added[0].Err != nil || added[1].Err == nil {
		t.Fatalf("partial add results: %+v", added)
	}
	statuses, err := registry.TellStatusBatch(context.Background(), "a1", []string{gid1, gid2})
	if err != nil { t.Fatal(err) }
	if len(statuses) != 2 || statuses[0].Status.Status != "complete" || statuses[0].Err != nil || statuses[1].Err == nil {
		t.Fatalf("partial status results: %+v", statuses)
	}
	if callsSeen != 2 { t.Fatal(fmt.Sprintf("expected two HTTP RPC calls, got %d", callsSeen)) }
}

func TestRegistryBatchRejectsTraversalWithoutSendingIt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		t.Error("invalid path must not be submitted")
	}))
	defer server.Close()
	registry := NewRegistry([]config.Aria2Instance{{ID: "a1", URL: server.URL}})
	results, err := registry.AddBatch(context.Background(), "a1", "/downloads", []AddRequest{
		{URI: "https://example.invalid/a", GID: "0123456789abcdef", RelativePath: "../escape.jpg"},
	})
	if err != nil { t.Fatal(err) }
	if len(results) != 1 || results[0].Err == nil { t.Fatalf("expected path error, got %+v", results) }
}
