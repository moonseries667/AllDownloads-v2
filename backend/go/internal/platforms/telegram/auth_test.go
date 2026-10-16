package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	p "github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms"
)

// A synthetic child process exercises argv, stream routing and process-scoped
// profile settings without launching tdl or contacting a Telegram account.
func init() {
	if os.Getenv("ALLDOWNLOADS_TEST_QR_CHILD") != "yes" {
		return
	}
	_ = json.NewEncoder(os.Stdout).Encode(struct {
		Args                []string
		Profile, Home, Temp string
		Debug, Extension    string
	}{os.Args[1:], os.Getenv("USERPROFILE"), os.Getenv("HOME"), os.Getenv("TEMP"), os.Getenv("TDL_DEBUG"), os.Getenv("TDL_EXTENSION")})
	os.Exit(0)
}

func TestQRLoginUsesNewExplicitStorageAndDirectTerminalStreams(t *testing.T) {
	t.Setenv("ALLDOWNLOADS_TEST_QR_CHILD", "yes")
	t.Setenv("TDL_DEBUG", "true")
	root := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	storage := filepath.Join(root, "auth", "telegram")
	tdl := &TDL{Executable: executable, WorkDir: filepath.Join(root, "work"), Accounts: map[string]TDLAccount{"telegram-main": {Namespace: "alldownloads-v2", StorageDir: storage}}}
	tdl.Run = func(_ context.Context, _ string, _ time.Duration, _ ...string) ([]byte, error) {
		return []byte("Version: 0.20.4"), nil
	}
	var output, diagnostic bytes.Buffer
	if err := tdl.LoginQR(context.Background(), "telegram-main", strings.NewReader(""), &output, &diagnostic); err != nil {
		t.Fatal(err)
	}
	var actual struct {
		Args                                  []string
		Profile, Home, Temp, Debug, Extension string
	}
	if json.Unmarshal(output.Bytes(), &actual) != nil {
		t.Fatalf("synthetic child output was not streamed: %q", output.String())
	}
	if argValue(actual.Args, "--ns") != "alldownloads-v2" || argValue(actual.Args, "--storage") != "type=bolt,path="+storage || !hasArgs(actual.Args, "login", "--type", "qr") || strings.Contains(strings.Join(actual.Args, " "), "desktop") {
		t.Fatalf("unsafe QR command: %v", actual.Args)
	}
	home := filepath.Join(tdl.WorkDir, "tool-home")
	if actual.Profile != home || actual.Home != home || actual.Temp != filepath.Join(home, "tmp") || actual.Debug != "false" || actual.Extension != "" || diagnostic.Len() != 0 {
		t.Fatalf("wrong isolated child environment: %+v", actual)
	}
}

func TestExplicitCheckCallsOnlyVersionAndSelfExtension(t *testing.T) {
	root := t.TempDir()
	storage := filepath.Join(root, "new-auth")
	if err := os.Mkdir(storage, 0700); err != nil {
		t.Fatal(err)
	}
	tdl := &TDL{Executable: filepath.Join(root, "tdl.exe"), WorkDir: filepath.Join(root, "new-work"), Accounts: map[string]TDLAccount{
		"telegram-main": {Namespace: "alldownloads-v2", StorageDir: storage, PeerID: "9007199254740993"},
	}}
	calls := 0
	tdl.Run = func(ctx context.Context, executable string, timeout time.Duration, args ...string) ([]byte, error) {
		calls++
		if argValue(args, "--ns") != "alldownloads-v2" || argValue(args, "--storage") != "type=bolt,path="+storage {
			t.Fatalf("missing isolated session flags: %v", args)
		}
		switch args[len(args)-1] {
		case "version":
			return []byte("Version: 0.20.4"), nil
		case IdentityExtension:
			return []byte(`{"selfId":"9007199254740993"}`), nil
		default:
			t.Fatalf("identity check made a non-identity call: %v", args)
			return nil, nil
		}
	}
	if self, err := tdl.CheckSession(context.Background(), "telegram-main"); err != nil || self != "9007199254740993" || calls != 2 {
		t.Fatalf("self=%q, err=%v, calls=%d", self, err, calls)
	}
	a := tdl.Accounts["telegram-main"]
	a.PeerID = "123"
	tdl.Accounts["telegram-main"] = a
	if _, err := tdl.CheckSession(context.Background(), "telegram-main"); err == nil {
		t.Fatal("different real identity was accepted")
	} else {
		var fault *p.Fault
		if !errors.As(err, &fault) || fault.Code != "ACCOUNT_IDENTITY_CHANGED" {
			t.Fatalf("unexpected identity fault: %v", err)
		}
	}
	a.PeerID = ""
	tdl.Accounts["telegram-main"] = a
	calls = 0
	if _, err := tdl.CheckSession(context.Background(), "telegram-main"); err == nil || calls != 0 {
		t.Fatal("unbound account became active")
	}
	if _, err := tdl.CurrentIdentity(context.Background(), "telegram-main"); err != nil || calls != 2 {
		t.Fatalf("setup could not identify first owner: %v", err)
	}
}

func TestIdentityRejectsVersionAndMalformedOutput(t *testing.T) {
	for _, tc := range []struct{ version, output string }{
		{"Version: 0.20.3", `{"selfId":"123"}`},
		{"Version: 0.20.40", `{"selfId":"123"}`},
		{"Version: 0.20.4", `{"selfId":"-1"}`},
		{"Version: 0.20.4", `{"selfId":"0"}`},
		{"Version: 0.20.4", `{"selfId":123}`},
		{"Version: 0.20.4", `not JSON`},
	} {
		t.Run(tc.version+tc.output, func(t *testing.T) {
			root := t.TempDir()
			tdl := &TDL{Executable: filepath.Join(root, "tdl.exe"), WorkDir: root, Accounts: map[string]TDLAccount{"telegram-main": {Namespace: "alldownloads-v2", StorageDir: root}}}
			tdl.Run = func(_ context.Context, _ string, _ time.Duration, args ...string) ([]byte, error) {
				if args[len(args)-1] == "version" {
					return []byte(tc.version), nil
				}
				return []byte(tc.output), nil
			}
			if _, err := tdl.CurrentIdentity(context.Background(), "telegram-main"); err == nil {
				t.Fatal("bad tool output was accepted")
			}
		})
	}
}

func TestToolProfileAndTemporaryFilesStayInNewWorkDirectory(t *testing.T) {
	tdl := &TDL{WorkDir: filepath.Join(t.TempDir(), "telegram-work")}
	env, err := tdl.toolEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"USERPROFILE", "HOME", "TMP", "TEMP", "TMPDIR"} {
		if !strings.HasPrefix(env[key], tdl.WorkDir+string(filepath.Separator)) {
			t.Fatalf("%s escaped new runtime", key)
		}
	}
	if env["TDL_DEBUG"] != "false" || env["TDL_EXTENSION"] != "" {
		t.Fatal("unsafe inherited tdl settings")
	}
	t.Setenv("TDL_DEBUG", "true")
	for _, item := range childEnvironment(env) {
		if item == "TDL_DEBUG=true" {
			t.Fatal("inherited debug mode leaked to child")
		}
	}
}
