package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lsongdev/bambulab-go/bambulab"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func stubDiscovery(t *testing.T) {
	t.Helper()
	previous := discoverPrinters
	previousPrinter := discoverPrinter
	discoverPrinters = func(context.Context, time.Duration) ([]bambulab.DiscoveredPrinter, error) {
		return []bambulab.DiscoveredPrinter{{ID: "SERIAL", IP: net.ParseIP("192.0.2.10")}}, nil
	}
	discoverPrinter = func(_ context.Context, id string) (bambulab.DiscoveredPrinter, error) {
		return bambulab.DiscoveredPrinter{ID: id, IP: net.ParseIP("192.0.2.10")}, nil
	}
	t.Cleanup(func() { discoverPrinters = previous; discoverPrinter = previousPrinter })
}
func cleanEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{"BAMBU_ACCOUNT", "BAMBU_PASSWORD", "BAMBU_CODE", "BAMBU_REGION", "BAMBU_ACCESS_TOKEN", "BAMBU_API_URL", "BAMBU_DEVICE_ID", "BAMBU_HOST", "BAMBU_ACCESS_CODE", "BAMBU_CA_FILE"} {
		t.Setenv(key, "")
	}
}
func TestLoginDevicesLogout(t *testing.T) {
	cleanEnvironment(t)
	stubDiscovery(t)
	path := filepath.Join(t.TempDir(), "nested", "credentials.json")
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	http.DefaultTransport = transportFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"accessToken":"secret-token","refreshToken":"secret-refresh","expiresIn":100}`
		if r.URL.Host != "api.bambulab.com" {
			t.Fatal(r.URL.Host)
		}
		if strings.HasSuffix(r.URL.Path, "login") {
			var credential map[string]string
			if err := json.NewDecoder(r.Body).Decode(&credential); err != nil {
				t.Fatal(err)
			}
			if credential["account"] != "me@example.test" || credential["password"] != "password" {
				t.Fatal(credential)
			}
		} else {
			if r.Header.Get("Authorization") != "Bearer secret-token" {
				t.Fatal("saved token not used")
			}
			body = `{"devices":[{"dev_id":"SERIAL","name":"printer"}]}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	var out, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--config", path, "--region", "global", "login", "--account", "me@example.test", "--password-stdin"}, strings.NewReader("password\n"), &out, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "secret") {
		t.Fatal("login leaked token")
	}
	cfg, err := loadConfig(path)
	if err != nil || cfg.AccessToken != "secret-token" || cfg.Region != "global" {
		t.Fatal(cfg, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--config", path, "devices"}, strings.NewReader(""), &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "SERIAL") {
		t.Fatal(out.String())
	}
	if err := Run(context.Background(), []string{"--config", path, "logout"}, strings.NewReader(""), &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	cachePath := (&app{configPath: path}).deviceCachePath()
	if _, err := os.Stat(cachePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("logout retained device cache", err)
	}
}
func TestVerificationChallengePreservesCredentials(t *testing.T) {
	cleanEnvironment(t)
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := saveConfig(path, config{AccessToken: "old"}); err != nil {
		t.Fatal(err)
	}
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	http.DefaultTransport = transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"loginType":"verifyCode"}`))}, nil
	})
	var out bytes.Buffer
	err := Run(context.Background(), []string{"--config", path, "login", "--account", "me", "--password-stdin"}, strings.NewReader("pass"), &out, &out)
	if err == nil || !strings.Contains(err.Error(), "verification required") {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path)
	if err != nil || cfg.AccessToken != "old" {
		t.Fatal(cfg, err)
	}
}
func TestInteractiveLoginWithVerification(t *testing.T) {
	cleanEnvironment(t)
	path := filepath.Join(t.TempDir(), "credentials.json")
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	requests := 0
	http.DefaultTransport = transportFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		var credential bambuCredential
		if err := json.NewDecoder(r.Body).Decode(&credential); err != nil {
			t.Fatal(err)
		}
		if credential.Account != "me@example.test" {
			t.Fatal(credential)
		}
		body := `{"loginType":"verifyCode"}`
		switch requests {
		case 1:
			if credential.Password != "password" || credential.Code != "" {
				t.Fatal(credential)
			}
		case 2:
			if credential.Password != "" || credential.Code != "bad" {
				t.Fatal(credential)
			}
			return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader(`{"message":"wrong code"}`))}, nil
		case 3:
			if credential.Password != "" || credential.Code != "123456" {
				t.Fatal(credential)
			}
			body = `{"accessToken":"new-token","refreshToken":"refresh-token"}`
		default:
			t.Fatalf("unexpected request %d", requests)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	var out, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--config", path, "login"}, strings.NewReader("me@example.test\npassword\nbad\n123456\n"), &out, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 3 || !strings.Contains(stderr.String(), "Verification code: ") || !strings.Contains(stderr.String(), "wrong code") {
		t.Fatal(requests, stderr.String())
	}
	if strings.Contains(out.String()+stderr.String(), "new-token") || strings.Contains(out.String()+stderr.String(), "password") {
		t.Fatal("credentials leaked")
	}
	cfg, err := loadConfig(path)
	if err != nil || cfg.AccessToken != "new-token" || cfg.RefreshToken != "refresh-token" {
		t.Fatal(cfg, err)
	}
}

func TestInteractiveLoginCanceledDuringRequest(t *testing.T) {
	cleanEnvironment(t)
	path := filepath.Join(t.TempDir(), "credentials.json")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	http.DefaultTransport = transportFunc(func(r *http.Request) (*http.Response, error) {
		cancel()
		return nil, r.Context().Err()
	})
	var out, stderr bytes.Buffer
	err := Run(ctx, []string{"--config", path, "login"}, strings.NewReader("me@example.test\npassword\n"), &out, &stderr)
	if !errors.Is(err, context.Canceled) || strings.Contains(stderr.String(), "Login failed") {
		t.Fatal(err, stderr.String())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("saved credentials after cancellation", err)
	}
}

func TestInteractiveLoginEOF(t *testing.T) {
	cleanEnvironment(t)
	path := filepath.Join(t.TempDir(), "credentials.json")
	for _, input := range []string{"", "me@example.test\n"} {
		var out, stderr bytes.Buffer
		err := Run(context.Background(), []string{"--config", path, "login"}, strings.NewReader(input), &out, &stderr)
		if !errors.Is(err, io.EOF) {
			t.Fatalf("input %q: %v", input, err)
		}
	}
}

func TestPrinterCommands(t *testing.T) {
	cleanEnvironment(t)
	stubDiscovery(t)
	t.Setenv("BAMBU_ACCESS_TOKEN", "test-token")
	path := filepath.Join(t.TempDir(), "credentials.json")
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	bindRequests := 0
	http.DefaultTransport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatal("missing token")
		}
		body := `{"devices":[{"dev_id":"SERIAL","name":"My printer","online":true,"print_status":"IDLE","dev_model_name":"P1","dev_product_name":"P1S","dev_access_code":"secret-code"},{"dev_id":"SECOND","name":"Other printer"}]}`
		if strings.HasSuffix(r.URL.Path, "/print") {
			body = `{"devices":[{"dev_id":"SERIAL","dev_name":"My printer","dev_online":true,"progress":42,"dev_access_code":"secret-code"}]}`
		} else if strings.HasSuffix(r.URL.Path, "/version") {
			if r.URL.Query().Get("dev_id") != "SERIAL" {
				t.Fatal(r.URL)
			}
			body = `{"devices":[{"dev_id":"SERIAL","version":"01.00"}]}`
		} else if strings.HasSuffix(r.URL.Path, "/preference") {
			body = `{}`
		} else if !strings.HasSuffix(r.URL.Path, "/bind") {
			t.Fatal(r.URL)
		} else {
			bindRequests++
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"devices"}, `"dev_id": "SERIAL"`},
		{[]string{"status"}, `"progress": 42`},
		{[]string{"--device", "SERIAL", "status"}, `"progress": 42`},
		{[]string{"status", "--device", "SERIAL"}, `"progress": 42`},
		{[]string{"version"}, `"version": "01.00"`},
		{[]string{"version", "--device=SERIAL"}, `"version": "01.00"`},
	} {
		var out, stderr bytes.Buffer
		args := append([]string{"--config", path}, tc.args...)
		args = append(args, "--json")
		if err := Run(context.Background(), args, strings.NewReader(""), &out, &stderr); err != nil {
			t.Fatal(args, err)
		}
		if !strings.Contains(out.String(), tc.want) {
			t.Fatal(args, out.String())
		}
	}
	var list bytes.Buffer
	if err := Run(context.Background(), []string{"--config", path, "devices"}, strings.NewReader(""), &list, &list); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list.String(), "NAME") || !strings.Contains(list.String(), "SERIAL") || !strings.Contains(list.String(), "192.0.2.10") || !strings.Contains(list.String(), "secret-code") || json.Valid(list.Bytes()) {
		t.Fatal(list.String())
	}
	var raw bytes.Buffer
	if err := Run(context.Background(), []string{"--config", path, "devices", "--json"}, strings.NewReader(""), &raw, &raw); err != nil {
		t.Fatal(err, raw.String())
	}
	var devices []bambulab.Device
	if err := json.Unmarshal(raw.Bytes(), &devices); err != nil || len(devices) != 2 || devices[0].AccessCode != "secret-code" || !strings.Contains(raw.String(), `"ip": "192.0.2.10"`) {
		t.Fatal(err, raw.String())
	}
	raw.Reset()
	if err := Run(context.Background(), []string{"--config", path, "status"}, strings.NewReader(""), &raw, &raw); err != nil || !strings.Contains(raw.String(), "Progress: 42%") || json.Valid(raw.Bytes()) {
		t.Fatal(err, raw.String())
	}
	for _, args := range [][]string{
		{"printer"}, {"delete", "/file"}, {"devices", "extra"}, {"ls", "one", "two"}, {"rm"}, {"rm", "one", "two"}, {"status", "extra"},
		{"pause", "extra"}, {"speed", "5"}, {"light", "invalid"},
		{"snapshot", "one.jpg", "two.jpg"}, {"version", "SERIAL"}, {"status", "--device"},
	} {
		var out bytes.Buffer
		if err := Run(context.Background(), append([]string{"--config", path}, args...), strings.NewReader(""), &out, &out); err == nil {
			t.Fatal("accepted", args)
		}
	}
	var out bytes.Buffer
	var err error
	before := bindRequests
	out.Reset()
	err = Run(context.Background(), []string{"--config", path, "pause"}, strings.NewReader(""), &out, &out)
	if err == nil || !strings.Contains(err.Error(), "no valid uid") || bindRequests != before {
		t.Fatal(err, bindRequests-before)
	}
}

func TestPrinterDefaultWhenNoDevicesBound(t *testing.T) {
	cleanEnvironment(t)
	stubDiscovery(t)
	t.Setenv("BAMBU_ACCESS_TOKEN", "test-token")
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	http.DefaultTransport = transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"devices":[]}`))}, nil
	})
	var out bytes.Buffer
	err := Run(context.Background(), []string{"--config", filepath.Join(t.TempDir(), "credentials.json"), "status"}, strings.NewReader(""), &out, &out)
	if err == nil || !strings.Contains(err.Error(), "no printers bound") {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--config", filepath.Join(t.TempDir(), "credentials.json"), "devices", "--json"}, strings.NewReader(""), &out, &out); err != nil || strings.TrimSpace(out.String()) != "[]" {
		t.Fatal(err, out.String())
	}
}

func TestDeviceListAndIPCache(t *testing.T) {
	cleanEnvironment(t)
	stubDiscovery(t)
	path := filepath.Join(t.TempDir(), "credentials.json")
	previousPrinter := discoverPrinter
	discoveries := 0
	discoverPrinter = func(ctx context.Context, serial string) (bambulab.DiscoveredPrinter, error) {
		discoveries++
		return previousPrinter(ctx, serial)
	}
	t.Cleanup(func() { discoverPrinter = previousPrinter })
	previousTransport := http.DefaultTransport
	requests := 0
	http.DefaultTransport = transportFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"devices":[{"dev_id":"SERIAL","dev_access_code":"code"}]}`))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previousTransport })
	run := func() {
		t.Helper()
		var out bytes.Buffer
		if err := Run(context.Background(), []string{"--config", path, "--token", "test-token", "devices", "--json"}, strings.NewReader(""), &out, &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), `"ip": "192.0.2.10"`) {
			t.Fatal(out.String())
		}
	}
	run()
	run()
	if requests != 1 || discoveries != 1 {
		t.Fatal("cache missed", requests, discoveries)
	}
	cacheApp := &app{configPath: path, region: "china", token: "test-token"}
	info, err := os.Stat(cacheApp.deviceCachePath())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	cache := cacheApp.readDeviceCache()
	ip := cache.IPs["SERIAL"]
	ip.SeenAt = time.Now().Add(-deviceIPTTL - time.Minute)
	cache.IPs["SERIAL"] = ip
	if err := cacheApp.saveDeviceCache(cache); err != nil {
		t.Fatal(err)
	}
	run()
	if requests != 1 || discoveries != 2 {
		t.Fatal("IP expiry did not refresh discovery", requests, discoveries)
	}
	cache = cacheApp.readDeviceCache()
	cache.UpdatedAt = time.Now().Add(-deviceListTTL - time.Minute)
	if err := cacheApp.saveDeviceCache(cache); err != nil {
		t.Fatal(err)
	}
	run()
	if requests != 2 || discoveries != 2 {
		t.Fatal("device expiry did not refresh cloud list", requests, discoveries)
	}
}

type bambuCredential struct {
	Account  string `json:"account"`
	Password string `json:"password"`
	Code     string `json:"code"`
}

func TestCLIValidation(t *testing.T) {
	cleanEnvironment(t)
	path := filepath.Join(t.TempDir(), "credentials.json")
	for _, args := range [][]string{{"unknown"}, {"devices", "extra"}, {"devices"}, {"speed", "5"}, {"light", "invalid"}, {"login", "--account", "a", "--password-stdin", "--code", "b"}, {"--region", "invalid", "profile"}, {"--timeout", "0s", "profile"}} {
		var out bytes.Buffer
		if err := Run(context.Background(), append([]string{"--config", path}, args...), strings.NewReader(""), &out, &out); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"help"}, strings.NewReader(""), &out, &out); err != nil || !strings.Contains(out.String(), "login") {
		t.Fatal(err, out.String())
	}
}
func TestConfigPermissionsAndFileWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := saveConfig(path, config{AccessToken: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := saveConfig(path, config{AccessToken: "second"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(path); err == nil {
		t.Fatal("accepted insecure credential permissions")
	}
	download := filepath.Join(t.TempDir(), "file")
	if err := writeNewFile(download, func(w io.Writer) error { _, err := io.WriteString(w, "ok"); return err }); err != nil {
		t.Fatal(err)
	}
	if err := writeNewFile(download, func(io.Writer) error { return nil }); err == nil {
		t.Fatal("overwrote existing file")
	}
	partial := download + ".partial"
	failure := errors.New("failed")
	if err := writeNewFile(partial, func(io.Writer) error { return failure }); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if _, err := os.Stat(partial); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial file retained")
	}
}
