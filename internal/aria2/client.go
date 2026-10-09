package aria2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

type Client struct {
	url    string
	secret string
	http   *http.Client
	seq    atomic.Uint64
}

type GlobalStat struct {
	DownloadSpeed string `json:"downloadSpeed"`
	UploadSpeed   string `json:"uploadSpeed"`
	NumActive     string `json:"numActive"`
	NumWaiting    string `json:"numWaiting"`
	NumStopped    string `json:"numStopped"`
}

type Status struct {
	GID             string `json:"gid"`
	Status          string `json:"status"`
	TotalLength     string `json:"totalLength"`
	CompletedLength string `json:"completedLength"`
	DownloadSpeed   string `json:"downloadSpeed"`
	ErrorCode       string `json:"errorCode,omitempty"`
	ErrorMessage    string `json:"errorMessage,omitempty"`
}

func New(url, secret string) *Client {
	return &Client{
		url:    url,
		secret: secret,
		http:   &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) GetGlobalStat(ctx context.Context) (GlobalStat, error) {
	var out GlobalStat
	err := c.call(ctx, "aria2.getGlobalStat", nil, &out)
	return out, err
}

func (c *Client) AddURI(ctx context.Context, uri, dir string) (string, error) {
	options := map[string]string{}
	if dir != "" {
		options["dir"] = dir
	}
	return c.AddURIWithOptions(ctx, uri, options)
}

func (c *Client) AddURIWithOptions(ctx context.Context, uri string, options map[string]string) (string, error) {
	params := []any{[]string{uri}}
	if len(options) > 0 {
		params = append(params, options)
	}
	var gid string
	if err := c.call(ctx, "aria2.addUri", params, &gid); err != nil {
		return "", err
	}
	return gid, nil
}

func (c *Client) TellStatus(ctx context.Context, gid string) (Status, error) {
	var out Status
	err := c.call(ctx, "aria2.tellStatus", []any{gid}, &out)
	return out, err
}

func (c *Client) Remove(ctx context.Context, gid string) error {
	var result string
	return c.call(ctx, "aria2.remove", []any{gid}, &result)
}

func (c *Client) RemoveDownloadResult(ctx context.Context, gid string) error {
	var result string
	return c.call(ctx, "aria2.removeDownloadResult", []any{gid}, &result)
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params,omitempty"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) call(ctx context.Context, method string, params []any, out any) error {
	if c.secret != "" {
		params = append([]any{"token:" + c.secret}, params...)
	}
	return c.callRaw(ctx, method, params, out)
}

// system.multicall authorizes each nested method separately. Do not add a
// token to the outer params: the nested calls contain their own credentials.
type methodCall struct {
	MethodName string `json:"methodName"`
	Params     []any  `json:"params"`
}

func (c *Client) multiCall(ctx context.Context, calls []methodCall) ([]json.RawMessage, error) {
	if len(calls) == 0 {
		return []json.RawMessage{}, nil
	}
	if c.secret != "" {
		for i := range calls {
			calls[i].Params = append([]any{"token:" + c.secret}, calls[i].Params...)
		}
	}
	var out []json.RawMessage
	err := c.callRaw(ctx, "system.multicall", []any{calls}, &out)
	if err != nil {
		return nil, err
	}
	if len(out) != len(calls) {
		return nil, fmt.Errorf("aria2 multicall returned %d results for %d calls", len(out), len(calls))
	}
	return out, nil
}

func parseMultiResult(raw json.RawMessage, out any) error {
	var failure rpcError
	if len(raw) > 0 && raw[0] == '{' {
		if err := json.Unmarshal(raw, &failure); err != nil { return err }
		return fmt.Errorf("aria2 rpc error %d: %s", failure.Code, failure.Message)
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil { return err }
	if len(values) != 1 {
		return fmt.Errorf("aria2 multicall result has %d values, expected 1", len(values))
	}
	return json.Unmarshal(values[0], out)
}

func (c *Client) callRaw(ctx context.Context, method string, params []any, out any) error {
	if c.url == "" {
		return errors.New("aria2 rpc url is empty")
	}
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      c.seq.Add(1),
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("aria2 rpc http status %s", resp.Status)
	}

	var rpcResp rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return err
	}
	if rpcResp.Error != nil {
		return fmt.Errorf("aria2 rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(rpcResp.Result, out)
}
