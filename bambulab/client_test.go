package bambulab

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func testClient(t *testing.T, status int, body string, check func(*http.Request)) *Client {
	t.Helper()
	return NewClient(WithAccessToken("test-token"), WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if check != nil {
			check(r)
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}))
}
func TestLoginAndRefresh(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		t.Run(map[bool]string{false: "login", true: "refresh"}[refresh], func(t *testing.T) {
			c := testClient(t, 200, `{"accessToken":"new","refreshToken":"refresh","expiresIn":123}`, func(r *http.Request) {
				if r.Method != "POST" || r.Header.Get("Authorization") != "" || r.Header.Get("Content-Type") != "application/json" {
					t.Fatalf("bad authentication request: %v", r)
				}
				var fields map[string]string
				if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
					t.Fatal(err)
				}
				if refresh {
					if fields["refreshToken"] != "old" {
						t.Fatal(fields)
					}
				} else {
					if fields["password"] != "pass" || fields["account"] != "account" {
						t.Fatal(fields)
					}
					if _, ok := fields["code"]; ok {
						t.Fatal(fields)
					}
				}
			})
			var err error
			if refresh {
				_, err = c.RefreshToken(context.Background(), "old")
			} else {
				_, err = c.Login(&Credential{Account: "account", Password: "pass"})
			}
			if err != nil || c.Token() != "new" {
				t.Fatalf("token=%s err=%v", c.Token(), err)
			}
		})
	}
	c := testClient(t, 200, `{"loginType":"verifyCode"}`, nil)
	response, err := c.Login(&Credential{Account: "a", Password: "p"})
	if err != nil || response.LoginType != "verifyCode" || c.Token() != "test-token" {
		t.Fatal(response, err)
	}
	for _, credential := range []*Credential{nil, {}, {Account: "a"}, {Account: "a", Password: "p", Code: "c"}} {
		if _, err := c.Login(credential); err == nil {
			t.Fatal("accepted invalid credential")
		}
	}
}
func TestHTTPErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		api    bool
	}{
		{"http", 401, `{"code":8,"message":"denied","error":"forbidden"}`, true},
		{"application", 200, `{"code":8,"message":"denied"}`, true},
		{"text", 502, "upstream unavailable", true},
		{"malformed", 200, "{", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, tc.status, tc.body, nil)
			_, err := c.GetProfile()
			if err == nil {
				t.Fatal("expected error")
			}
			var api *APIError
			if errors.As(err, &api) != tc.api {
				t.Fatalf("%T %v", err, err)
			}
			if api != nil && api.StatusCode != tc.status {
				t.Fatal(api)
			}
		})
	}
	c := testClient(t, 204, "", nil)
	if err := c.UpdateDeviceInfo(context.Background(), "serial", map[string]any{"name": "new"}); err != nil {
		t.Fatal(err)
	}
}
func TestCloudEndpoints(t *testing.T) {
	ctx := context.Background()
	zero := 0
	tests := []struct {
		name, method, path, query string
		call                      func(*Client) error
	}{
		{"profile", "GET", "/v1/user-service/my/profile", "", func(c *Client) error { _, e := c.GetProfile(); return e }},
		{"devices", "GET", "/v1/iot-service/api/user/bind", "", func(c *Client) error { _, e := c.ListDevices(); return e }},
		{"preference", "GET", "/v1/design-user-service/my/preference", "", func(c *Client) error { _, e := c.GetPreference(ctx); return e }},
		{"messages", "GET", "/v1/user-service/my/messages", "after=a%2Bb&limit=10&type=0", func(c *Client) error {
			_, e := c.ListMessages(ctx, MessageQuery{Type: &zero, After: "a+b", Limit: 10})
			return e
		}},
		{"tasks", "GET", "/v1/user-service/my/tasks", "deviceId=a%26b&limit=2", func(c *Client) error { _, e := c.ListTasks(ctx, TaskQuery{DeviceID: "a&b", Limit: 2}); return e }},
		{"create-task", "POST", "/v1/user-service/my/task", "", func(c *Client) error { _, e := c.CreateTask(ctx, Task{DeviceID: "serial"}); return e }},
		{"ticket", "GET", "/v1/user-service/my/ticket/a%2Fb", "", func(c *Client) error { _, e := c.GetTicket(ctx, "a/b"); return e }},
		{"resources", "GET", "/v1/iot-service/api/slicer/resource", "slicer%2Fplugins%2Fcloud=1.0", func(c *Client) error {
			_, e := c.GetResources(ctx, map[string]string{"slicer/plugins/cloud": "1.0"})
			return e
		}},
		{"settings", "GET", "/v1/iot-service/api/slicer/setting", "version=1.0", func(c *Client) error { _, e := c.ListSettings(ctx, "1.0"); return e }},
		{"setting", "GET", "/v1/iot-service/api/slicer/setting/a%3Fb", "", func(c *Client) error { _, e := c.GetSetting(ctx, "a?b"); return e }},
		{"device-info", "PATCH", "/v1/iot-service/api/user/device/info", "", func(c *Client) error { return c.UpdateDeviceInfo(ctx, "serial", map[string]any{"name": "renamed"}) }},
		{"version", "GET", "/v1/iot-service/api/user/device/version", "dev_id=serial", func(c *Client) error { _, e := c.GetDeviceVersion(ctx, "serial"); return e }},
		{"notification", "GET", "/v1/iot-service/api/user/notification", "action=upload&ticket=abc", func(c *Client) error { _, e := c.GetNotification(ctx, "upload", "abc"); return e }},
		{"print", "GET", "/v1/iot-service/api/user/print", "force=true", func(c *Client) error { _, e := c.GetPrintStatus(ctx, true); return e }},
		{"project-profile", "GET", "/v1/iot-service/api/user/profile/abc", "model_id=xyz", func(c *Client) error { _, e := c.GetProjectProfile(ctx, "abc", "xyz"); return e }},
		{"projects", "GET", "/v1/iot-service/api/user/project", "", func(c *Client) error { _, e := c.ListProjects(ctx); return e }},
		{"project", "GET", "/v1/iot-service/api/user/project/abc", "", func(c *Client) error { _, e := c.GetProject(ctx, "abc"); return e }},
		{"task", "GET", "/v1/iot-service/api/user/task/abc", "", func(c *Client) error { _, e := c.GetTask(ctx, "abc"); return e }},
		{"ttcode", "POST", "/v1/iot-service/api/user/ttcode", "", func(c *Client) error { _, e := c.GetTTCode(ctx, "serial"); return e }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			c := testClient(t, 200, `{"code":null,"error":null,"message":"success"}`, func(r *http.Request) {
				called = true
				if r.Method != tc.method || r.URL.EscapedPath() != tc.path || r.URL.RawQuery != tc.query {
					t.Fatalf("got %s %s", r.Method, r.URL)
				}
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Fatal("missing bearer token")
				}
				if tc.method == "GET" && r.Body != nil {
					t.Fatal("GET has body")
				}
			})
			if err := tc.call(c); err != nil {
				t.Fatal(err)
			}
			if !called {
				t.Fatal("not called")
			}
		})
	}
}
func TestDoValidationAndCancellation(t *testing.T) {
	c := NewClient(WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() })}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Do(ctx, "GET", "/test", nil, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, path := range []string{"https://evil.example/test", "//evil.example/test", "relative", "/test#fragment"} {
		if err := c.Do(context.Background(), "GET", path, nil, nil, nil); err == nil {
			t.Fatal(path)
		}
	}
	c = testClient(t, 200, `{}`, func(r *http.Request) {
		if r.URL.Host != "example.test" || r.URL.Path != "/prefix/test" || r.URL.Query().Get("q") != "x&y" {
			t.Fatal(r.URL)
		}
	})
	WithBaseURL("https://example.test/prefix/")(c)
	if err := c.Do(context.Background(), "GET", "/test", url.Values{"q": {"x&y"}}, nil, nil); err != nil {
		t.Fatal(err)
	}
}
