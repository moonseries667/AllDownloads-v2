package drive

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
)

// RcloneCommand runs a bounded Job-controlled tool with an exact environment.
// Its bounded stdout/stderr stay inside Go; callers must never log them.
type RcloneCommand func(context.Context, string, time.Duration, []string, []string) ([]byte, []byte, error)
type RcloneConfig struct {
	AccountID   string
	RootID      string
	Executable  string
	ConfigFile  string
	Remote      string
	BindingFile string
	ProxyURL    string
	Run         RcloneCommand
}
type rcloneBinding struct {
	Version     int    `json:"version"`
	AccountID   string `json:"accountId"`
	PrincipalID string `json:"principalId"`
	ClientID    string `json:"clientId"`
	ConfigFile  string `json:"configFile"`
	Remote      string `json:"remote"`
	RootID      string `json:"rootId"`
}
type rcloneAuth struct {
	config        RcloneConfig
	mu            sync.Mutex
	checked       bool
	cached        storedAuth
	until         time.Time
	stamp         string
	verifiedStamp atomic.Value
}

func NewRclone(config RcloneConfig) (*Client, error) {
	if config.AccountID == "" || config.Remote != "gdrive" || config.ProxyURL != warpProxy || config.Run == nil || !filepath.IsAbs(config.Executable) || !filepath.IsAbs(config.ConfigFile) || !filepath.IsAbs(config.BindingFile) || strings.EqualFold(filepath.Clean(config.BindingFile), filepath.Clean(config.ConfigFile)) || strings.EqualFold(filepath.Clean(config.BindingFile), filepath.Clean(config.Executable)) {
		return nil, fault("AUTH_CONFIG_INVALID")
	}
	return &Client{AccountID: config.AccountID, RootID: config.RootID, rclone: &rcloneAuth{config: config}}, nil
}

func (c *Client) credentials(ctx context.Context) (storedAuth, error) {
	if c.rclone != nil {
		return c.rclone.credentials(ctx, c)
	}
	if c.auth != nil {
		return c.auth.credentials(ctx, c)
	}
	return storedAuth{}, fault("AUTH_REQUIRED")
}
func (c *Client) persistentAuth() bool { return c.auth != nil || c.rclone != nil }
func (c *Client) invalidate(token string) error {
	if c.rclone != nil {
		c.rclone.mu.Lock()
		c.rclone.until = time.Time{}
		c.rclone.mu.Unlock()
		return nil
	}
	return c.auth.invalidate(token)
}
func (a *rcloneAuth) command(ctx context.Context, args ...string) ([]byte, error) {
	args = append(args, "--config", a.config.ConfigFile, "--ask-password=false", "--log-level", "ERROR", "--stats", "0")
	raw, diagnostic, err := a.config.Run(ctx, a.config.Executable, 30*time.Second, mountEnvironment(nil), args)
	clear(diagnostic)
	if err != nil {
		clear(raw)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fault("AUTH_RCLONE_FAILED")
	}
	if len(raw) > 1<<20 {
		clear(raw)
		return nil, fault("AUTH_CONFIG_INVALID")
	}
	return raw, nil
}
func (a *rcloneAuth) version(ctx context.Context) error {
	if a.checked {
		return nil
	}
	raw, err := a.command(ctx, "version")
	if err != nil {
		return err
	}
	defer clear(raw)
	if strings.TrimSpace(strings.SplitN(string(raw), "\n", 2)[0]) != "rclone v"+RcloneVersion {
		return fault("MOUNT_TOOL_VERSION_MISMATCH")
	}
	a.checked = true
	return nil
}

func configStamp(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return info.ModTime().UTC().Format(time.RFC3339Nano) + "/" + strconv.FormatInt(info.Size(), 10)
}
func (a *rcloneAuth) changed() bool {
	stamp, _ := a.verifiedStamp.Load().(string)
	return stamp == "" || configStamp(a.config.ConfigFile) != stamp
}

// Parse only the selected remote and only fields needed for personal OAuth.
// Client secrets/refresh tokens are deliberately not returned or persisted.
func parseRcloneRemote(raw []byte, remote string) (map[string]string, error) {
	fields := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	section := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if line != "["+remote+"]" || section != "" {
				return nil, fault("AUTH_CONFIG_INVALID")
			}
			section = remote
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || section != remote {
			return nil, fault("AUTH_CONFIG_INVALID")
		}
		switch key {
		case "type", "client_id", "token", "root_folder_id", "team_drive", "env_auth", "service_account_file", "service_account_credentials", "client_credentials", "impersonate", "token_url", "auth_url":
			if _, exists := fields[key]; exists {
				return nil, fault("AUTH_CONFIG_INVALID")
			}
			fields[key] = strings.TrimSpace(value)
		}
	}
	if scanner.Err() != nil || fields["type"] != "drive" {
		return nil, fault("AUTH_CONFIG_INVALID")
	}
	if fields["team_drive"] != "" {
		return nil, fault("SHARED_DRIVE_UNSUPPORTED")
	}
	if fields["service_account_file"] != "" || fields["service_account_credentials"] != "" || (fields["env_auth"] != "" && fields["env_auth"] != "false") {
		return nil, fault("AUTH_MODE_UNSUPPORTED")
	}
	if (fields["client_credentials"] != "" && fields["client_credentials"] != "false") || fields["impersonate"] != "" || (fields["token_url"] != "" && fields["token_url"] != "https://oauth2.googleapis.com/token") || (fields["auth_url"] != "" && fields["auth_url"] != "https://accounts.google.com/o/oauth2/auth" && fields["auth_url"] != "https://accounts.google.com/o/oauth2/v2/auth") {
		return nil, fault("AUTH_MODE_UNSUPPORTED")
	}
	return fields, nil
}

func (a *rcloneAuth) credentials(ctx context.Context, c *Client) (storedAuth, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if c.HTTP == nil {
		return storedAuth{}, fault("AUTH_PROXY_REQUIRED")
	}
	stamp := configStamp(a.config.ConfigFile)
	if stamp != "" && stamp == a.stamp && time.Now().Before(a.until) {
		return a.cached, nil
	}
	if err := a.version(ctx); err != nil {
		return storedAuth{}, err
	}
	// about refreshes through rclone's normal provider without enumerating media.
	quota, err := a.command(ctx, "about", a.config.Remote+":", "--json")
	if err != nil {
		return storedAuth{}, err
	}
	var quotaObject map[string]json.RawMessage
	validQuota := json.Unmarshal(quota, &quotaObject) == nil && quotaObject != nil
	clear(quota)
	if !validQuota {
		return storedAuth{}, fault("PROVIDER_CHANGED")
	}
	raw, err := a.command(ctx, "config", "show", a.config.Remote)
	if err != nil {
		return storedAuth{}, err
	}
	fields, err := parseRcloneRemote(raw, a.config.Remote)
	clear(raw)
	if err != nil {
		return storedAuth{}, err
	}
	observedStamp := configStamp(a.config.ConfigFile)
	if observedStamp == "" {
		return storedAuth{}, fault("AUTH_CONFIG_INVALID")
	}
	var token struct {
		AccessToken string    `json:"access_token"`
		TokenType   string    `json:"token_type"`
		Expiry      time.Time `json:"expiry"`
	}
	if json.Unmarshal([]byte(fields["token"]), &token) != nil || token.AccessToken == "" || (!strings.EqualFold(token.TokenType, "Bearer") && token.TokenType != "") {
		return storedAuth{}, fault("AUTH_REQUIRED")
	}
	principal, err := c.principal(ctx, token.AccessToken)
	if err != nil {
		return storedAuth{}, err
	}
	clientID, scope, expiry, err := c.tokenIdentity(ctx, token.AccessToken)
	if err != nil {
		return storedAuth{}, err
	}
	if fields["client_id"] != "" && fields["client_id"] != clientID {
		return storedAuth{}, fault("ACCOUNT_IDENTITY_CHANGED")
	}
	if !hasDriveScope(scope) {
		return storedAuth{}, fault("AUTH_SCOPE_REQUIRED")
	}
	root := fields["root_folder_id"]
	if root == "" {
		root = "root"
	}
	if a.config.RootID != "" && a.config.RootID != "root" && root != "root" && a.config.RootID != root {
		return storedAuth{}, fault("AUTH_ROOT_MISMATCH")
	}
	binding := rcloneBinding{1, c.AccountID, principal, clientID, filepath.Clean(a.config.ConfigFile), a.config.Remote, root}
	if configStamp(a.config.ConfigFile) != observedStamp {
		return storedAuth{}, fault("AUTH_SOURCE_CHANGED")
	}
	if err = a.bind(binding); err != nil {
		return storedAuth{}, err
	}
	a.cached = storedAuth{Version: 1, AccountID: c.AccountID, PrincipalID: principal, ClientID: clientID, AccessToken: token.AccessToken, Expiry: expiry, Scope: scope, SourceRootID: root}
	a.until = time.Now().Add(30 * time.Second)
	if early := expiry.Add(-10 * time.Second); early.Before(a.until) {
		a.until = early
	}
	a.stamp = observedStamp
	a.verifiedStamp.Store(observedStamp)
	return a.cached, nil
}

func (a *rcloneAuth) bind(binding rcloneBinding) error {
	unlock, err := lockPrivate(a.config.BindingFile)
	if err != nil {
		return storeLockFailure(err)
	}
	defer unlock()
	if err = privatePath(a.config.BindingFile); err != nil {
		return fault("AUTH_STORE_UNAVAILABLE")
	}
	raw, err := os.ReadFile(a.config.BindingFile)
	if errors.Is(err, os.ErrNotExist) {
		if executor.WriteStateFile(a.config.BindingFile, binding) != nil {
			return fault("AUTH_STORE_UNAVAILABLE")
		}
		return nil
	}
	if err != nil || len(raw) > 65536 {
		return fault("AUTH_STORE_UNAVAILABLE")
	}
	var prior rcloneBinding
	if json.Unmarshal(raw, &prior) != nil {
		return fault("AUTH_STORE_INVALID")
	}
	if prior != binding {
		return fault("ACCOUNT_IDENTITY_CHANGED")
	}
	return nil
}

func (c *Client) tokenIdentity(ctx context.Context, token string) (string, string, time.Time, error) {
	base, err := c.base()
	if err != nil {
		return "", "", time.Time{}, err
	}
	// Google's discovery schema specifies POST with access_token as a query
	// parameter. This private URL/error is never returned or logged.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/oauth2/v2/tokeninfo?"+url.Values{"access_token": {token}}.Encode(), nil)
	if err != nil {
		return "", "", time.Time{}, fault("AUTH_CONFIG_INVALID")
	}
	resp, err := authHTTP(c.HTTP).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", "", time.Time{}, ctx.Err()
		}
		return "", "", time.Time{}, fault("AUTH_UNAVAILABLE")
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return "", "", time.Time{}, fault("AUTH_REQUIRED")
	}
	var info struct {
		IssuedTo  string `json:"issued_to"`
		Audience  string `json:"audience"`
		Scope     string `json:"scope"`
		ExpiresIn int64  `json:"expires_in"`
	}
	if err = decodeResponse(resp, &info); err != nil {
		return "", "", time.Time{}, err
	}
	if info.IssuedTo == "" {
		info.IssuedTo = info.Audience
	}
	if info.IssuedTo == "" || info.ExpiresIn <= 0 || info.ExpiresIn > 86400 {
		return "", "", time.Time{}, fault("PROVIDER_CHANGED")
	}
	return info.IssuedTo, info.Scope, time.Now().Add(time.Duration(info.ExpiresIn) * time.Second), nil
}
