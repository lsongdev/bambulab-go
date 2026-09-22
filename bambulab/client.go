// Package bambulab provides cloud and local printer clients for Bambu Lab.
package bambulab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	API             = "https://api.bambulab.cn" // Kept for compatibility; the default is China.
	APIChina        = API
	APIGlobal       = "https://api.bambulab.com"
	maxResponseSize = 16 << 20
)

type Region string

const (
	RegionChina  Region = "china"
	RegionGlobal Region = "global"
)

// Client is safe for concurrent method calls. Configure it before first use.
type Client struct {
	client  *http.Client
	baseURL string
	mu      sync.RWMutex
	// AccessToken is retained for compatibility. Use SetAccessToken when concurrent.
	AccessToken string
}
type Option func(*Client)

func WithRegion(region Region) Option {
	return func(c *Client) {
		if region == RegionGlobal {
			c.baseURL = APIGlobal
		} else {
			c.baseURL = APIChina
		}
	}
}
func WithBaseURL(baseURL string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(baseURL, "/") }
}
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		if client != nil {
			c.client = client
		}
	}
}
func WithAccessToken(token string) Option { return func(c *Client) { c.AccessToken = token } }
func NewClient(options ...Option) *Client {
	c := &Client{client: &http.Client{Timeout: 30 * time.Second}, baseURL: API}
	for _, option := range options {
		option(c)
	}
	return c
}
func (c *Client) SetAccessToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.AccessToken = token
}
func (c *Client) Token() string { c.mu.RLock(); defer c.mu.RUnlock(); return c.AccessToken }

type Credential struct {
	Account  string `json:"account"`
	Password string `json:"password,omitempty"`
	Code     string `json:"code,omitempty"`
}
type LoginResponse struct {
	AccessToken      string `json:"accessToken"`
	RefreshToken     string `json:"refreshToken"`
	LoginType        string `json:"loginType"`
	ExpiresIn        int    `json:"expiresIn"`
	RefreshExpiresIn int    `json:"refreshExpiresIn"`
}
type ErrorResponse struct {
	Code    int    `json:"code"`
	Error   string `json:"error"`
	Message string `json:"message"`
}

// APIError includes HTTP and application error details. Body is never logged by the SDK.
type APIError struct {
	StatusCode int
	Code       int
	Name       string
	Message    string
	Body       json.RawMessage
}

func (e *APIError) Error() string {
	detail := e.Message
	if detail == "" {
		detail = e.Name
	}
	if detail == "" {
		detail = http.StatusText(e.StatusCode)
	}
	return fmt.Sprintf("bambulab: HTTP %d, code %d: %s", e.StatusCode, e.Code, detail)
}

// Do sends an authenticated JSON request to a relative API path. It also allows
// access to endpoints not yet represented by a typed method. No requests are retried.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body, result any) error {
	return c.do(ctx, method, path, query, body, result, true)
}
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, result any, auth bool) error {
	base, err := url.Parse(c.baseURL)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return fmt.Errorf("bambulab: invalid API base URL")
	}
	ref, err := url.Parse(path)
	if err != nil || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || ref.IsAbs() || ref.Host != "" || ref.Fragment != "" {
		return fmt.Errorf("bambulab: API path must be relative to the configured server")
	}
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return fmt.Errorf("bambulab: URL: %w", err)
	}
	q := u.Query()
	for k, values := range query {
		q[k] = append([]string(nil), values...)
	}
	u.RawQuery = q.Encode()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("bambulab: encode request: %w", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return fmt.Errorf("bambulab: create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "bambulab-go")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		if token := c.Token(); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("bambulab: request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return fmt.Errorf("bambulab: read response: %w", err)
	}
	if len(data) > maxResponseSize {
		return fmt.Errorf("bambulab: response exceeds %d bytes", maxResponseSize)
	}
	var envelope ErrorResponse
	_ = json.Unmarshal(data, &envelope)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || envelope.Code != 0 || envelope.Error != "" {
		return &APIError{StatusCode: resp.StatusCode, Code: envelope.Code, Name: envelope.Error, Message: envelope.Message, Body: append(json.RawMessage(nil), data...)}
	}
	if result == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("bambulab: decode response: %w", err)
	}
	return nil
}

// Login automatically saves a returned access token. A verifyCode response is a
// challenge, not a completed login; submit a new Credential containing Code.
func (c *Client) Login(credential *Credential) (*LoginResponse, error) {
	return c.LoginContext(context.Background(), credential)
}
func (c *Client) LoginContext(ctx context.Context, credential *Credential) (*LoginResponse, error) {
	if credential == nil || strings.TrimSpace(credential.Account) == "" || (credential.Password == "") == (credential.Code == "") {
		return nil, fmt.Errorf("bambulab: account and exactly one of password or code are required")
	}
	var response LoginResponse
	if err := c.do(ctx, http.MethodPost, "/v1/user-service/user/login", nil, credential, &response, false); err != nil {
		return nil, err
	}
	if response.AccessToken != "" {
		c.SetAccessToken(response.AccessToken)
	}
	return &response, nil
}

// RefreshToken exposes the documented endpoint, which may return 401 even for valid tokens.
func (c *Client) RefreshToken(ctx context.Context, token string) (*LoginResponse, error) {
	if token == "" {
		return nil, fmt.Errorf("bambulab: refresh token is required")
	}
	var response LoginResponse
	if err := c.do(ctx, http.MethodPost, "/v1/user-service/user/refreshtoken", nil, map[string]string{"refreshToken": token}, &response, false); err != nil {
		return nil, err
	}
	if response.AccessToken != "" {
		c.SetAccessToken(response.AccessToken)
	}
	return &response, nil
}
func (c *Client) GetProfile() (Profile, error) { return c.GetProfileContext(context.Background()) }
func (c *Client) GetProfileContext(ctx context.Context) (response Profile, err error) {
	err = c.Do(ctx, http.MethodGet, "/v1/user-service/my/profile", nil, nil, &response)
	return
}
func (c *Client) ListDevices() (BindResponse, error) {
	return c.ListDevicesContext(context.Background())
}
func (c *Client) ListDevicesContext(ctx context.Context) (response BindResponse, err error) {
	err = c.Do(ctx, http.MethodGet, "/v1/iot-service/api/user/bind", nil, nil, &response)
	return
}
