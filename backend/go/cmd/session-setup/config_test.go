package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupTestConfig(t *testing.T, extra map[string]any) (*localConfig, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "runner.json")
	fields := map[string]any{"stateDir": filepath.Join(root, "runtime", "runner"), "runnerId": "isolated-test", "isolatedTestMode": true, "largeId": json.RawMessage(`900719925474099312345`)}
	for key, value := range extra {
		fields[key] = value
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, root
}

func TestQuarkSetupChecksIdentityThenAtomicallyBindsNewCookie(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/account/info" || r.Header.Get("Cookie") != "sid=new-synthetic-cookie" {
			t.Errorf("unexpected setup request: %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `{"status":200,"code":0,"data":{"uid":"new-user"}}`)
	}))
	defer server.Close()
	cfg, root := setupTestConfig(t, map[string]any{"quarkAccounts": map[string]any{"quark-main": map[string]any{"accountUrl": server.URL + "/account/info", "keepField": "kept"}}})
	err := setupQuark(context.Background(), cfg, func() ([]byte, error) { return []byte("sid=new-synthetic-cookie\r\n"), nil })
	if err != nil || calls != 1 {
		t.Fatalf("setup=%v, calls=%d", err, calls)
	}
	cfg, err = loadConfig(cfg.path)
	if err != nil {
		t.Fatal(err)
	}
	a, err := cfg.quarkAccount()
	if err != nil || a.ExpectedUserID != "new-user" || a.CookieFile != filepath.Join(root, "runtime", "auth", "quark-cookie.local") {
		t.Fatalf("account=%+v err=%v", a, err)
	}
	cookie, err := os.ReadFile(a.CookieFile)
	if err != nil || string(cookie) != "sid=new-synthetic-cookie" {
		t.Fatal("private Cookie was not saved exactly")
	}
	raw, _ := os.ReadFile(cfg.path)
	if bytes.Contains(raw, []byte("sid=")) || !bytes.Contains(raw, []byte(`900719925474099312345`)) || !bytes.Contains(raw, []byte(`"keepField": "kept"`)) {
		t.Fatal("credential leaked or unrelated config was altered")
	}
	entries, _ := os.ReadDir(filepath.Dir(cfg.path))
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatal("temporary config was left behind")
		}
	}
}

func TestQuarkSetupRejectsDifferentOwnerWithoutAnySavedChanges(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":200,"code":0,"data":{"uid":"different-user"}}`)
	}))
	defer server.Close()
	cfg, _ := setupTestConfig(t, map[string]any{"quarkAccounts": map[string]any{"quark-main": map[string]any{"accountUrl": server.URL + "/account/info", "expectedUserId": "bound-user"}}})
	a, _ := cfg.quarkAccount()
	if err := writeAtomic(a.CookieFile, []byte("sid=old-synthetic-cookie"), 0600); err != nil {
		t.Fatal(err)
	}
	original := append([]byte(nil), cfg.original...)
	if err := setupQuark(context.Background(), cfg, func() ([]byte, error) { return []byte("sid=new-synthetic-cookie"), nil }); err == nil {
		t.Fatal("changed identity accepted")
	}
	raw, _ := os.ReadFile(cfg.path)
	cookie, _ := os.ReadFile(a.CookieFile)
	if !bytes.Equal(raw, original) || string(cookie) != "sid=old-synthetic-cookie" {
		t.Fatal("a rejected identity changed the configuration or Cookie")
	}
}

func TestQuarkSetupStopsIfConfigurationChangedDuringInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":200,"code":0,"data":{"uid":"new-user"}}`)
	}))
	defer server.Close()
	cfg, _ := setupTestConfig(t, map[string]any{"quarkAccounts": map[string]any{"quark-main": map[string]any{"accountUrl": server.URL + "/account/info"}}})
	a, _ := cfg.quarkAccount()
	err := setupQuark(context.Background(), cfg, func() ([]byte, error) {
		return []byte("sid=synthetic"), os.WriteFile(cfg.path, append(cfg.original, '\n'), 0600)
	})
	if err == nil {
		t.Fatal("concurrent configuration update was overwritten")
	}
	if _, err := os.Stat(a.CookieFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Cookie was written after configuration changed")
	}
}

func TestPrivatePathsCannotPointOutsideNewAuthDirectory(t *testing.T) {
	cfg, root := setupTestConfig(t, nil)
	for _, path := range []string{filepath.Join(root, "old-cookie.local"), filepath.Join(root, "runtime", "auth"), "relative-cookie.local"} {
		if err := cfg.privatePath(path); err == nil {
			t.Fatalf("unsafe private path accepted: %s", path)
		}
	}
	a, err := cfg.quarkAccount()
	if err != nil || a.ExpectedUserID != "" || cfg.privatePath(a.CookieFile) != nil {
		t.Fatalf("fresh account path not derived: %+v %v", a, err)
	}
}

func TestQuarkUnboundExistingCookieIsNeverReadOrReplaced(t *testing.T) {
	cfg, _ := setupTestConfig(t, nil)
	a, _ := cfg.quarkAccount()
	if err := writeAtomic(a.CookieFile, []byte("untouched-synthetic-original"), 0600); err != nil {
		t.Fatal(err)
	}
	called := false
	err := setupQuark(context.Background(), cfg, func() ([]byte, error) { called = true; return nil, nil })
	if err == nil || called {
		t.Fatal("unbound existing session was replaced")
	}
}

func TestConfigLockExcludesSecondSetup(t *testing.T) {
	cfg, _ := setupTestConfig(t, nil)
	unlock, err := lockConfig(cfg.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockConfig(cfg.path); err == nil {
		t.Fatal("concurrent setup was allowed")
	}
	unlock()
	unlockAgain, err := lockConfig(cfg.path)
	if err != nil {
		t.Fatal(err)
	}
	unlockAgain()
}

func TestExtensionRejectsInvalidProtocolWithoutOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic-extension.json")
	if err := os.WriteFile(path, []byte(`{"name":"wrong","session":"c3ludGhldGljLW5vdC1hLXNlc3Npb24="}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TDL_EXTENSION", path)
	var output bytes.Buffer
	err := runIdentityExtension(context.Background(), &output)
	if err == nil || output.Len() != 0 || strings.Contains(err.Error(), "session") {
		t.Fatalf("invalid extension output=%q err=%v", output.String(), err)
	}
}
