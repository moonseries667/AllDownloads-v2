package drive

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type rcloneFixture struct {
	mu       sync.Mutex
	fields   map[string]string
	clientID string
	version  string
	fail     bool
	calls    int
}

func newRcloneFixture(t *testing.T) (*fakeDrive, *Client, *rcloneFixture) {
	t.Helper()
	f, legacy := newFakeDrive(t)
	root := t.TempDir()
	configFile := filepath.Join(root, "source.local.conf")
	if err := os.WriteFile(configFile, []byte("synthetic config managed only by the fixture tool"), 0600); err != nil {
		t.Fatal(err)
	}
	token, _ := json.Marshal(map[string]any{"access_token": "fixture-only", "refresh_token": "synthetic-do-not-persist-refresh", "token_type": "Bearer", "expiry": time.Now().Add(time.Hour)})
	fixture := &rcloneFixture{fields: map[string]string{"type": "drive", "client_id": "fixture-client", "client_secret": "synthetic-do-not-persist-secret", "token": string(token)}, clientID: "fixture-client", version: "rclone v1.74.4"}
	command := func(ctx context.Context, executable string, timeout time.Duration, environment, args []string) ([]byte, []byte, error) {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		fixture.calls++
		if executable != filepath.Join(root, "rclone.exe") || timeout <= 0 || !containsPair(args, "--config", configFile) {
			t.Error("tool command did not use the explicit source")
		}
		env := map[string]string{}
		for _, entry := range environment {
			key, value, _ := strings.Cut(entry, "=")
			env[key] = value
		}
		if env["HTTP_PROXY"] != warpProxy || env["HTTPS_PROXY"] != warpProxy || env["NO_PROXY"] != "" {
			t.Error("tool bypassed WARP")
		}
		for key := range env {
			upper := strings.ToUpper(key)
			if strings.HasPrefix(upper, "RCLONE_") && upper != "RCLONE_CONFIG_PASS" {
				t.Error("inherited rclone override was not removed")
			}
		}
		if fixture.fail {
			return []byte("synthetic-do-not-leak"), []byte("synthetic-do-not-leak"), errors.New("synthetic tool error")
		}
		switch args[0] {
		case "version":
			return []byte(fixture.version + "\n"), nil, nil
		case "about":
			if args[1] != "gdrive:" || args[2] != "--json" {
				t.Error("about enumerated another source")
			}
			return []byte(`{"used":0}`), nil, nil
		case "config":
			if args[1] != "show" || args[2] != "gdrive" {
				t.Error("configuration was exported beyond the selected remote")
			}
			out := "[gdrive]\n"
			for key, value := range fixture.fields {
				out += key + " = " + value + "\n"
			}
			return []byte(out), nil, nil
		}
		t.Error("unexpected rclone command")
		return nil, nil, errors.New("unexpected command")
	}
	c, err := NewRclone(RcloneConfig{AccountID: "drive-main", RootID: "root", Executable: filepath.Join(root, "rclone.exe"), ConfigFile: configFile, Remote: "gdrive", BindingFile: filepath.Join(root, "v2", "drive-binding.json"), ProxyURL: warpProxy, Run: command})
	if err != nil {
		t.Fatal(err)
	}
	c.BaseURL = legacy.BaseURL
	c.HTTP = &http.Client{Transport: authRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/oauth2/v2/tokeninfo" {
			if r.Method != "POST" || r.URL.Query().Get("access_token") != "fixture-only" {
				t.Error("invalid private token identity request")
			}
			fixture.mu.Lock()
			id := fixture.clientID
			fixture.mu.Unlock()
			raw, _ := json.Marshal(map[string]any{"issued_to": id, "audience": id, "scope": driveScope, "expires_in": 3600})
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
	return f, c, fixture
}

func containsPair(args []string, key, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}

func TestRcloneAuthenticationPinsIdentityWithoutCopyingCredentials(t *testing.T) {
	t.Setenv("RCLONE_CONFIG_GDRIVE_TOKEN", "synthetic-parent-override")
	t.Setenv("RCLONE_DRIVE_SERVICE_ACCOUNT_FILE", "synthetic-parent-file")
	t.Setenv("NO_PROXY", "*")
	f, c, fixture := newRcloneFixture(t)
	if fixture.calls != 0 {
		t.Fatal("construction contacted the tool")
	}
	if err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(c.rclone.config.BindingFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "fixture-only") || strings.Contains(string(raw), "synthetic-do-not") {
		t.Fatal("binding copied credentials")
	}
	if _, err := c.StartOAuth(context.Background()); !isCode(err, "AUTH_MANAGED_EXTERNALLY") {
		t.Fatal(err)
	}
	state, root, req := archiveFixture(t, c)
	if result, err := c.Archive(context.Background(), state, "rclone-upload", root, req); err != nil || !result.Verified {
		t.Fatal("native upload did not share the verified identity", err)
	}
	before := append([]byte(nil), raw...)
	f.mu.Lock()
	f.principal = "principal-b"
	f.mu.Unlock()
	if err := c.Check(context.Background()); !isCode(err, "ACCOUNT_IDENTITY_CHANGED") {
		t.Fatal("changed account was accepted", err)
	}
	after, err := os.ReadFile(c.rclone.config.BindingFile)
	if err != nil || string(before) != string(after) {
		t.Fatal("identity mismatch overwrote binding")
	}
}

func TestRcloneAuthenticationRejectsUnsupportedOrChangedSources(t *testing.T) {
	for _, mode := range []string{"service-account", "shared-drive", "env-auth", "client-credentials", "impersonate", "custom-token-url", "client-mismatch", "version", "tool-failure"} {
		t.Run(mode, func(t *testing.T) {
			_, c, f := newRcloneFixture(t)
			want := ""
			switch mode {
			case "service-account":
				f.fields["service_account_file"] = "synthetic-account.json"
				want = "AUTH_MODE_UNSUPPORTED"
			case "shared-drive":
				f.fields["team_drive"] = "shared"
				want = "SHARED_DRIVE_UNSUPPORTED"
			case "env-auth":
				f.fields["env_auth"] = "true"
				want = "AUTH_MODE_UNSUPPORTED"
			case "client-credentials":
				f.fields["client_credentials"] = "true"
				want = "AUTH_MODE_UNSUPPORTED"
			case "impersonate":
				f.fields["impersonate"] = "synthetic-other-user"
				want = "AUTH_MODE_UNSUPPORTED"
			case "custom-token-url":
				f.fields["token_url"] = "http://127.0.0.1:9/not-google"
				want = "AUTH_MODE_UNSUPPORTED"
			case "client-mismatch":
				f.clientID = "other-client"
				want = "ACCOUNT_IDENTITY_CHANGED"
			case "version":
				f.version = "rclone v1.74.3"
				want = "MOUNT_TOOL_VERSION_MISMATCH"
			case "tool-failure":
				f.fail = true
				want = "AUTH_RCLONE_FAILED"
			}
			err := c.Check(context.Background())
			if !isCode(err, want) || strings.Contains(err.Error(), "synthetic-do-not-leak") {
				t.Fatal("unsupported source accepted or diagnostic leaked", err)
			}
			if _, err = os.Stat(c.rclone.config.BindingFile); !os.IsNotExist(err) {
				t.Fatal("unsupported source created binding")
			}
		})
	}
}

func TestRcloneRootAndClientChangesCannotRetargetOldBinding(t *testing.T) {
	for _, mode := range []string{"root", "client"} {
		t.Run(mode, func(t *testing.T) {
			_, c, f := newRcloneFixture(t)
			if err := c.Check(context.Background()); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			if mode == "root" {
				f.fields["root_folder_id"] = "different-root"
			} else {
				f.fields["client_id"] = "different-client"
				f.clientID = "different-client"
			}
			f.mu.Unlock()
			if err := c.Check(context.Background()); !isCode(err, "ACCOUNT_IDENTITY_CHANGED") {
				t.Fatal("source identity changed without refusal", err)
			}
		})
	}
}
