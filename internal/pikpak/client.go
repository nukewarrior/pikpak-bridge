package pikpak

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	apiDrive      = "https://api-drive.mypikpak.com"
	apiUser       = "https://user.mypikpak.com"
	clientID      = "YNxT9w7GMdWvEOKa"
	clientSecret  = "dbw2OtmVEeuUvIptb1Coyg"
	clientVersion = "1.21.0"
	packageName   = "com.pikcloud.pikpak"
	userAgent     = "ANDROID-com.pikcloud.pikpak/1.21.0"
)

var captchaSalts = []string{
	"",
	"E32cSkYXC2bciKJGxRsE8ZgwmH/YwkvpD6/O9guSOa2irCwciH4xPHaH",
	"QtqgfMgHP2TFl",
	"zOKgHT56L7nIzFzDpUGhpWFrgP53m3G6ML",
	"S",
	"THxpsktzfFXizUv7DK1y/N7NZ1WhayViluBEvAJJ8bA1Wr6",
	"y9PXH3xGUhG/zQI8CaapRw2LhldCaFM9CRlKpZXJvj+pifu",
	"+RaaG7T8FRTI4cP019N5y9ofLyHE9ySFUr",
	"6Pf1l8UTeuzYldGtb/d",
}

type Client struct {
	username     string
	password     string
	accessToken  string
	refreshToken string
	captchaToken string
	userID       string
	expiresAt    int64
	deviceID     string
	sessionDir   string
	httpClient   *http.Client
}

func NewClient(username, password, sessionDir string) *Client {
	sum := md5.Sum([]byte(username))
	return &Client{
		username:   username,
		password:   password,
		deviceID:   hex.EncodeToString(sum[:]),
		sessionDir: sessionDir,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				MaxIdleConnsPerHost:   8,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
				ExpectContinueTimeout: time.Second,
				IdleConnTimeout:       90 * time.Second,
			},
		},
	}
}

func (c *Client) Login(ctx context.Context) error {
	if c.username == "" || c.password == "" {
		return &Error{Kind: ErrorKindAuth, Op: "login", Message: "username and password are required"}
	}

	if data, err := loadSession(c.sessionDir, c.username); err == nil {
		c.accessToken = data.AccessToken
		c.refreshToken = data.RefreshToken
		c.captchaToken = data.CaptchaToken
		c.userID = data.UserID
		c.expiresAt = data.ExpiresAt

		if !data.expired(time.Now()) {
			return nil
		}
		if c.refreshToken != "" {
			if err := c.refreshAccessToken(ctx); err == nil {
				return c.persistSession()
			}
		}
	}

	if err := c.passwordLogin(ctx); err != nil {
		return err
	}
	return c.persistSession()
}

func (c *Client) persistSession() error {
	return saveSession(c.sessionDir, c.username, sessionData{
		AccessToken:  c.accessToken,
		RefreshToken: c.refreshToken,
		CaptchaToken: c.captchaToken,
		UserID:       c.userID,
		ExpiresAt:    c.expiresAt,
	})
}

func (c *Client) passwordLogin(ctx context.Context) error {
	token, err := c.loginCaptcha(ctx)
	if err != nil {
		return err
	}

	body := map[string]string{
		"client_id":     clientID,
		"client_secret": clientSecret,
		"grant_type":    "password",
		"username":      c.username,
		"password":      c.password,
		"captcha_token": token,
	}
	var resp map[string]any
	if err := c.rawJSON(ctx, http.MethodPost, apiUser+"/v1/auth/signin", body, &resp); err != nil {
		return err
	}
	if code := int64(number(resp["error_code"])); code != 0 {
		return classifyAPIError("login", code, http.StatusOK, responseMessage(resp))
	}

	c.accessToken = stringValue(resp["access_token"])
	c.refreshToken = stringValue(resp["refresh_token"])
	c.userID = stringValue(resp["sub"])
	c.captchaToken = token
	expiresIn := int64(number(resp["expires_in"]))
	c.expiresAt = time.Now().Add(time.Duration(expiresIn)*time.Second - sessionExpirySkew).Unix()

	if c.accessToken == "" {
		return &Error{Kind: ErrorKindAuth, Op: "login", Message: "empty access token"}
	}
	return nil
}

func (c *Client) loginCaptcha(ctx context.Context) (string, error) {
	body := map[string]any{
		"client_id": clientID,
		"device_id": c.deviceID,
		"action":    "POST:/v1/auth/signin",
		"meta": map[string]string{
			"username": c.username,
		},
	}
	var resp map[string]any
	if err := c.rawJSON(ctx, http.MethodPost, apiUser+"/v1/shield/captcha/init", body, &resp); err != nil {
		return "", err
	}
	if code := int64(number(resp["error_code"])); code != 0 {
		return "", classifyAPIError("captcha init", code, http.StatusOK, responseMessage(resp))
	}
	if verifyURL := stringValue(resp["url"]); verifyURL != "" {
		return "", &Error{
			Kind:      ErrorKindCaptcha,
			Op:        "captcha init",
			Message:   "verification required",
			VerifyURL: verifyURL,
		}
	}
	token := stringValue(resp["captcha_token"])
	if token == "" {
		return "", &Error{Kind: ErrorKindCaptcha, Op: "captcha init", Message: "empty captcha token"}
	}
	return token, nil
}

func (c *Client) refreshAccessToken(ctx context.Context) error {
	body := map[string]string{
		"client_id":     clientID,
		"client_secret": clientSecret,
		"grant_type":    "refresh_token",
		"refresh_token": c.refreshToken,
	}
	var resp map[string]any
	if err := c.rawJSON(ctx, http.MethodPost, apiUser+"/v1/auth/token", body, &resp); err != nil {
		return err
	}
	if code := int64(number(resp["error_code"])); code != 0 {
		return classifyAPIError("refresh token", code, http.StatusOK, responseMessage(resp))
	}
	if token := stringValue(resp["access_token"]); token != "" {
		c.accessToken = token
	}
	if token := stringValue(resp["refresh_token"]); token != "" {
		c.refreshToken = token
	}
	if userID := stringValue(resp["sub"]); userID != "" {
		c.userID = userID
	}
	expiresIn := int64(number(resp["expires_in"]))
	c.expiresAt = time.Now().Add(time.Duration(expiresIn)*time.Second - sessionExpirySkew).Unix()
	return nil
}

func (c *Client) Quota(ctx context.Context) (quotaMessage, error) {
	var out quotaMessage
	if err := c.doJSON(ctx, http.MethodGet, apiDrive+"/drive/v1/about", nil, &out); err != nil {
		return quotaMessage{}, err
	}
	return out, nil
}

func (c *Client) CreateOfflineTask(ctx context.Context, source string) (offlineTaskAPI, error) {
	body := map[string]any{
		"kind":        fileKindFile,
		"upload_type": "UPLOAD_TYPE_URL",
		"url": map[string]string{
			"url": source,
		},
	}
	var out offlineDownloadResponse
	if err := c.doJSON(ctx, http.MethodPost, apiDrive+"/drive/v1/files", body, &out); err != nil {
		return offlineTaskAPI{}, err
	}
	return out.Task, nil
}

func (c *Client) OfflineTasks(ctx context.Context) ([]offlineTaskAPI, error) {
	values := url.Values{}
	values.Set("type", "offline")
	values.Set("thumbnail_size", "SIZE_SMALL")
	values.Set("limit", "10000")
	values.Set("with", "reference_resource")

	var out []offlineTaskAPI
	for {
		var resp offlineListResponse
		endpoint := apiDrive + "/drive/v1/tasks?" + values.Encode()
		if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Tasks...)
		if resp.NextPageToken == "" {
			return out, nil
		}
		values.Set("page_token", resp.NextPageToken)
	}
}

func (c *Client) ListByParentID(ctx context.Context, parentID string) ([]fileStat, error) {
	values := url.Values{}
	values.Set("thumbnail_size", "SIZE_MEDIUM")
	values.Set("limit", "500")
	values.Set("parent_id", parentID)
	values.Set("with_audit", "false")
	values.Set("filters", `{"trashed":{"eq":false}}`)

	var out []fileStat
	for {
		var resp filesResponse
		endpoint := apiDrive + "/drive/v1/files?" + values.Encode()
		if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Files...)
		if resp.NextPageToken == "" {
			return out, nil
		}
		values.Set("page_token", resp.NextPageToken)
	}
}

func (c *Client) File(ctx context.Context, fileID string) (fileDetails, error) {
	values := url.Values{}
	values.Set("thumbnail_size", "SIZE_LARGE")
	values.Set("usage", "FETCH")

	var out fileDetails
	endpoint := apiDrive + "/drive/v1/files/" + url.PathEscape(fileID) + "?" + values.Encode()
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &out); err != nil {
		return fileDetails{}, err
	}
	return out, nil
}

func (c *Client) DeletePermanently(ctx context.Context, fileID string) error {
	body := map[string][]string{"ids": []string{fileID}}
	return c.doJSON(ctx, http.MethodPost, apiDrive+"/drive/v1/files:batchDelete", body, nil)
}

func (c *Client) DownloadURL(ctx context.Context, fileID string) (string, error) {
	f, err := c.File(ctx, fileID)
	if err != nil {
		return "", err
	}
	link := pickDownloadURL(f)
	if link == "" {
		return "", &Error{Kind: ErrorKindAPI, Op: "download url", Message: "no download URL returned"}
	}
	return link, nil
}

func pickDownloadURL(f fileDetails) string {
	if f.Links.ApplicationOctetStream.URL != "" {
		return f.Links.ApplicationOctetStream.URL
	}
	if f.WebContentLink != "" {
		return f.WebContentLink
	}
	for _, media := range f.Medias {
		if media.Link.URL != "" {
			return media.Link.URL
		}
	}
	return ""
}

func (c *Client) doJSON(ctx context.Context, method, endpoint string, body, out any) error {
	if err := c.ensureAccess(ctx); err != nil {
		return err
	}
	return c.doJSONAttempt(ctx, method, endpoint, body, out, true)
}

func (c *Client) doJSONAttempt(ctx context.Context, method, endpoint string, body, out any, retry bool) error {
	reqBody, err := encodeBody(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reqBody)
	if err != nil {
		return err
	}
	c.setHeaders(req)
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	code, message := apiError(data)
	if code != 0 || message != "" || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if retry && (code == 4121 || code == 4122 || code == 16 || resp.StatusCode == http.StatusUnauthorized) {
			if err := c.refreshAccessToken(ctx); err != nil {
				return err
			}
			if err := c.persistSession(); err != nil {
				return err
			}
			return c.doJSONAttempt(ctx, method, endpoint, body, out, false)
		}
		if retry && code == 9 {
			if err := c.refreshCaptcha(ctx, method, endpoint); err != nil {
				return err
			}
			if err := c.persistSession(); err != nil {
				return err
			}
			return c.doJSONAttempt(ctx, method, endpoint, body, out, false)
		}
		if message == "" {
			message = strings.TrimSpace(string(data))
		}
		if message == "" {
			message = resp.Status
		}
		return classifyAPIError(method+" "+endpoint, code, resp.StatusCode, message)
	}

	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) rawJSON(ctx context.Context, method, endpoint string, body, out any) error {
	reqBody, err := encodeBody(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reqBody)
	if err != nil {
		return err
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		code, message := apiError(data)
		if message == "" {
			message = strings.TrimSpace(string(data))
		}
		return classifyAPIError(method+" "+endpoint, code, resp.StatusCode, message)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *Client) ensureAccess(ctx context.Context) error {
	if c.accessToken != "" && time.Now().Unix() < c.expiresAt {
		return nil
	}
	if c.refreshToken != "" {
		if err := c.refreshAccessToken(ctx); err == nil {
			return c.persistSession()
		}
	}
	if err := c.passwordLogin(ctx); err != nil {
		return err
	}
	return c.persistSession()
}

func (c *Client) refreshCaptcha(ctx context.Context, method, endpoint string) error {
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	sign := clientID + clientVersion + packageName + c.deviceID + timestamp
	for _, salt := range captchaSalts {
		sum := md5.Sum([]byte(sign + salt))
		sign = fmt.Sprintf("%x", sum)
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	body := map[string]any{
		"action":        method + ":" + u.Path,
		"captcha_token": c.captchaToken,
		"client_id":     clientID,
		"device_id":     c.deviceID,
		"meta": map[string]string{
			"captcha_sign":   "1." + sign,
			"user_id":        c.userID,
			"package_name":   packageName,
			"client_version": clientVersion,
			"timestamp":      timestamp,
		},
		"redirect_uri": "xlaccsdk01://xbase.cloud/callback?state=harbor",
	}
	var resp map[string]any
	if err := c.rawJSON(ctx, http.MethodPost, apiUser+"/v1/shield/captcha/init?client_id="+clientID, body, &resp); err != nil {
		return err
	}
	if code := int64(number(resp["error_code"])); code != 0 {
		return classifyAPIError("captcha refresh", code, http.StatusOK, responseMessage(resp))
	}
	if verifyURL := stringValue(resp["url"]); verifyURL != "" {
		return &Error{
			Kind:      ErrorKindCaptcha,
			Op:        "captcha refresh",
			Message:   "verification required",
			VerifyURL: verifyURL,
		}
	}
	c.captchaToken = stringValue(resp["captcha_token"])
	if c.captchaToken == "" {
		return &Error{Kind: ErrorKindCaptcha, Op: "captcha refresh", Message: "empty captcha token"}
	}
	return nil
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-Device-Id", c.deviceID)
	if c.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.accessToken)
	}
	if c.captchaToken != "" {
		req.Header.Set("X-Captcha-Token", c.captchaToken)
	}
}

func encodeBody(body any) (io.Reader, error) {
	if body == nil {
		return nil, nil
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

func apiError(data []byte) (int64, string) {
	var resp map[string]any
	if err := json.Unmarshal(data, &resp); err != nil {
		return 0, ""
	}
	code := int64(number(resp["error_code"]))
	return code, responseMessage(resp)
}

func responseMessage(resp map[string]any) string {
	errText := stringValue(resp["error"])
	desc := stringValue(resp["error_description"])
	switch {
	case errText == "":
		return desc
	case desc == "":
		return errText
	default:
		return errText + ": " + desc
	}
}

func number(v any) float64 {
	switch value := v.(type) {
	case float64:
		return value
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case string:
		n, _ := strconv.ParseFloat(value, 64)
		return n
	default:
		return 0
	}
}

func stringValue(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
