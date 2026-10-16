package telegram

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	p "github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms"
)

const IdentityExtension = "alldownloads-self"

// IdentityHelperPath is inside the isolated tool home used by all tdl commands.
// session-setup installs its own executable here as a local tdl extension.
func (t *TDL) IdentityHelperPath() string {
	name := "tdl-" + IdentityExtension
	filename := name
	if runtime.GOOS == "windows" {
		filename += ".exe"
	}
	return filepath.Join(t.WorkDir, "tool-home", ".tdl", "extensions", name, filename)
}

func (t *TDL) authAccount(id string) (TDLAccount, error) {
	a, ok := t.Accounts[id]
	if !ok {
		return a, p.NewFault("AUTH_REQUIRED", "IDENTITY")
	}
	if a.Namespace != "alldownloads-v2" || !filepath.IsAbs(a.StorageDir) || strings.ContainsAny(a.StorageDir, ",\x00\r\n") || !filepath.IsAbs(t.WorkDir) || !filepath.IsAbs(t.Executable) {
		return a, p.NewFault("INVALID_INPUT", "CONFIG")
	}
	return a, nil
}

func (t *TDL) checkAuthVersion(ctx context.Context, a TDLAccount) error {
	out, err := t.command(ctx, append(t.args(a), "version")...)
	if err != nil {
		return err
	}
	if !pinnedTDLVersion(out) {
		return p.NewFault("TOOL_VERSION_MISMATCH", "CONFIG")
	}
	return nil
}

func pinnedTDLVersion(out []byte) bool {
	fields := strings.Fields(string(out))
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "Version:" && strings.TrimPrefix(fields[i+1], "v") == TDLVersion {
			return true
		}
	}
	return false
}

// CurrentIdentity calls a local tdl extension which makes only users.getUsers
// with inputUserSelf. It never runs chat ls/export or reads Saved Messages.
// Empty PeerID is allowed only so local setup can establish the first binding.
func (t *TDL) CurrentIdentity(ctx context.Context, account string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	a, err := t.authAccount(account)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(a.StorageDir); err != nil || !info.IsDir() {
		return "", p.NewFault("AUTH_REQUIRED", "IDENTITY")
	}
	if err := t.checkAuthVersion(ctx, a); err != nil {
		return "", err
	}
	out, err := t.command(ctx, append(t.args(a), IdentityExtension)...)
	if err != nil {
		return "", err
	}
	var result struct {
		SelfID string `json:"selfId"`
	}
	if json.Unmarshal(out, &result) != nil || !positiveSelfID(result.SelfID) {
		return "", p.NewFault("PROVIDER_CHANGED", "IDENTITY")
	}
	if a.PeerID != "" && a.PeerID != result.SelfID {
		return "", p.NewFault("ACCOUNT_IDENTITY_CHANGED", "IDENTITY")
	}
	return result.SelfID, nil
}

// CheckSession is an explicit online identity check. Ordinary Check remains a
// local tool/config check and must not cause an account to become ACTIVE.
func (t *TDL) CheckSession(ctx context.Context, account string) (string, error) {
	a, err := t.authAccount(account)
	if err != nil {
		return "", err
	}
	if !positiveSelfID(a.PeerID) {
		return "", p.NewFault("AUTH_REQUIRED", "IDENTITY")
	}
	return t.CurrentIdentity(ctx, account)
}

func positiveSelfID(raw string) bool {
	n, err := strconv.ParseInt(raw, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == raw
}

// LoginQR is for the user's local interactive terminal only. Streams go
// directly to that terminal; no output, passwords or QR tokens are collected.
func (t *TDL) LoginQR(ctx context.Context, account string, input io.Reader, output, diagnostic io.Writer) error {
	a, err := t.authAccount(account)
	if err != nil {
		return err
	}
	if a.PeerID != "" {
		return p.NewFault("INVALID_INPUT", "CONFIG")
	}
	if err := os.MkdirAll(a.StorageDir, 0700); err != nil {
		return p.SafeFault(err, "CONFIG")
	}
	if err := t.checkAuthVersion(ctx, a); err != nil {
		return err
	}
	environment, err := t.toolEnvironment()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, t.Executable, append(t.args(a), "login", "--type", "qr")...)
	cmd.Env = childEnvironment(environment)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = input, output, diagnostic
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return p.SafeFault(ctx.Err(), "AUTH")
		}
		return p.NewFault("AUTH_OR_TOOL_REQUIRED", "AUTH")
	}
	return nil
}

func (t *TDL) toolEnvironment() (map[string]string, error) {
	toolHome := filepath.Join(t.WorkDir, "tool-home")
	if !filepath.IsAbs(toolHome) {
		return nil, p.NewFault("INVALID_INPUT", "CONFIG")
	}
	temporary := filepath.Join(toolHome, "tmp")
	if err := os.MkdirAll(temporary, 0700); err != nil {
		return nil, p.SafeFault(err, "CONFIG")
	}
	return map[string]string{"USERPROFILE": toolHome, "HOME": toolHome, "TMP": temporary, "TEMP": temporary, "TMPDIR": temporary, "TDL_DEBUG": "false", "TDL_PROXY": "", "TDL_NTP": "", "TDL_EXTENSION": ""}, nil
}

func childEnvironment(overrides map[string]string) []string {
	var result []string
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		replaced := false
		for key := range overrides {
			if strings.EqualFold(key, name) {
				replaced = true
				break
			}
		}
		if !replaced {
			result = append(result, item)
		}
	}
	for key, value := range overrides {
		result = append(result, key+"="+value)
	}
	return result
}
