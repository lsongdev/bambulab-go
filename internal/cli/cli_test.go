package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func cleanEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{"BAMBU_ACCOUNT", "BAMBU_PASSWORD", "BAMBU_CODE", "BAMBU_REGION", "BAMBU_ACCESS_TOKEN", "BAMBU_API_URL", "BAMBU_DEVICE_ID", "BAMBU_HOST", "BAMBU_ACCESS_CODE", "BAMBU_CA_FILE"} {
		t.Setenv(key, "")
	}
}
func TestLoginDevicesLogout(t *testing.T) {
	cleanEnvironment(t)
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
