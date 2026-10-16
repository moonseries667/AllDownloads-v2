package drive

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
)

const driveScope = "https://www.googleapis.com/auth/drive"

// GoogleConfig contains private runtime paths, never token values. Construction
// does not contact Google or start authorization. TokenFile belongs to this v2
// account; ClientFile is a newly supplied Desktop OAuth client JSON file.
type GoogleConfig struct {
	AccountID  string
	RootID     string
	TokenFile  string
	ClientFile string
}

type googleAuth struct {
	config          GoogleConfig
	mu              sync.Mutex
	sessionMu       sync.Mutex
	verifiedToken   string
	verifiedBinding string
	tokenURL        string
	authorizeURL    string
	sessions        map[string]*oauthSession
}

type storedAuth struct {
	Version      int       `json:"version"`
	AccountID    string    `json:"accountId"`
	PrincipalID  string    `json:"principalId"`
	ClientID     string    `json:"clientId"`
	ClientSecret string    `json:"clientSecret,omitempty"`
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	Expiry       time.Time `json:"expiry"`
	Scope        string    `json:"scope"`
}

func NewGoogle(config GoogleConfig) (*Client, error) {
	if config.AccountID == "" || !filepath.IsAbs(config.TokenFile) || (config.ClientFile != "" && !filepath.IsAbs(config.ClientFile)) || config.TokenFile == config.ClientFile {
		return nil, fault("AUTH_CONFIG_INVALID")
	}
	return &Client{AccountID: config.AccountID, RootID: config.RootID, auth: &googleAuth{config: config, sessions: map[string]*oauthSession{}}}, nil
}

// Check performs read-only identity, root and permission checks. It never starts
// login, creates a folder or uploads media.
func (c *Client) Check(ctx context.Context) error {
	obj, err := c.root(ctx)
	if err != nil {
		return err
	}
	if !obj.Capabilities.CanList || !obj.Capabilities.CanAdd {
		return fault("TARGET_PERMISSION_DENIED")
	}
	return nil
}

func authHTTP(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	copyClient := *client
	// Auth requests are small, bounded and never forward secrets on redirects.
	copyClient.Timeout = 30 * time.Second
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &copyClient
}

func privateEndpoint(override, production string) (string, error) {
	if override == "" {
		return production, nil
	}
	u, err := url.Parse(override)
	if err != nil {
		return "", fault("AUTH_CONFIG_INVALID")
	}
	base := &Client{BaseURL: u.Scheme + "://" + u.Host}
	if _, err := base.base(); err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fault("AUTH_CONFIG_INVALID")
	}
	return override, nil
}

func (a *googleAuth) read() (storedAuth, error) {
	var stored storedAuth
	raw, err := readPrivate(a.config.TokenFile)
	if os.IsNotExist(err) {
		return stored, fault("AUTH_REQUIRED")
	}
	if err != nil {
		return stored, fault("AUTH_STORE_UNAVAILABLE")
	}
	if json.Unmarshal(raw, &stored) != nil || stored.Version != 1 || stored.AccountID != a.config.AccountID || stored.PrincipalID == "" || stored.ClientID == "" || stored.RefreshToken == "" || stored.AccessToken == "" || stored.Expiry.IsZero() || !hasDriveScope(stored.Scope) {
		return storedAuth{}, fault("AUTH_STORE_INVALID")
	}
	return stored, nil
}

func hasDriveScope(scope string) bool {
	for _, value := range strings.Fields(scope) {
		if value == driveScope {
			return true
		}
	}
	return false
}

func (a *googleAuth) write(stored storedAuth) error {
	raw, err := json.Marshal(stored)
	if err != nil || writePrivate(a.config.TokenFile, raw) != nil {
		return fault("AUTH_STORE_UNAVAILABLE")
	}
	return nil
}

func storeLockFailure(err error) error {
	if privateLockBusy(err) {
		return media.Fail("AUTH_STORE_BUSY", "DRIVE", true)
	}
	return fault("AUTH_STORE_UNAVAILABLE")
}

// Credentials are verified before use and before persisting a refresh. A failed
// refresh never falls back to an expired token and never overwrites the store.
func (a *googleAuth) credentials(ctx context.Context, c *Client) (storedAuth, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	unlock, err := lockPrivate(a.config.TokenFile)
	if err != nil {
		return storedAuth{}, storeLockFailure(err)
	}
	defer unlock()
	stored, err := a.read()
	if err != nil {
		return storedAuth{}, err
	}
	refreshed := false
	refresh := func() error {
		values := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {stored.RefreshToken}, "client_id": {stored.ClientID}}
		if stored.ClientSecret != "" {
			values.Set("client_secret", stored.ClientSecret)
		}
		grant, err := a.grant(ctx, c.HTTP, values)
		if err != nil {
			return err
		}
		stored.AccessToken, stored.Expiry = grant.AccessToken, time.Now().Add(time.Duration(grant.ExpiresIn)*time.Second)
		if grant.RefreshToken != "" {
			stored.RefreshToken = grant.RefreshToken
		}
		if grant.Scope != "" {
			stored.Scope = grant.Scope
		}
		if !hasDriveScope(stored.Scope) {
			return fault("AUTH_SCOPE_REQUIRED")
		}
		refreshed = true
		return nil
	}
	if !stored.Expiry.After(time.Now().Add(time.Minute)) {
		if err := refresh(); err != nil {
			return storedAuth{}, err
		}
	}
	if refreshed || a.verifiedToken != stored.AccessToken || a.verifiedBinding != authBinding(stored) {
		principal, err := c.principal(ctx, stored.AccessToken)
		// A token can be revoked before its advertised expiry. Only this
		// read-only identity request is retried, once, after a fresh grant.
		if isCode(err, "AUTH_REQUIRED") && !refreshed {
			if err := refresh(); err != nil {
				return storedAuth{}, err
			}
			principal, err = c.principal(ctx, stored.AccessToken)
		}
		if err != nil {
			return storedAuth{}, err
		}
		if principal != stored.PrincipalID {
			return storedAuth{}, fault("ACCOUNT_IDENTITY_CHANGED")
		}
	}
	if refreshed {
		if err = a.write(stored); err != nil {
			return storedAuth{}, err
		}
	}
	a.verifiedToken = stored.AccessToken
	a.verifiedBinding = authBinding(stored)
	return stored, nil
}

func (a *googleAuth) invalidate(token string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	unlock, err := lockPrivate(a.config.TokenFile)
	if err != nil {
		return storeLockFailure(err)
	}
	defer unlock()
	stored, err := a.read()
	if err != nil {
		return err
	}
	if stored.AccessToken != token {
		return nil
	}
	stored.Expiry = time.Now().Add(-time.Second)
	a.verifiedToken = ""
	a.verifiedBinding = ""
	return a.write(stored)
}

type tokenGrant struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

func (a *googleAuth) grant(ctx context.Context, client *http.Client, values url.Values) (tokenGrant, error) {
	var grant tokenGrant
	endpoint, err := privateEndpoint(a.tokenURL, "https://oauth2.googleapis.com/token")
	if err != nil {
		return grant, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return grant, fault("AUTH_CONFIG_INVALID")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := authHTTP(client).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return grant, ctx.Err()
		}
		return grant, media.Fail("AUTH_UNAVAILABLE", "DRIVE", true)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return grant, fault("PROVIDER_CHANGED")
	}
	if resp.StatusCode != 200 {
		// Provider descriptions can contain tokens or locators. Never relay them.
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &failure)
		if failure.Error == "invalid_grant" {
			return grant, fault("AUTH_REQUIRED")
		}
		if failure.Error == "invalid_client" || failure.Error == "unauthorized_client" {
			return grant, fault("AUTH_CONFIG_INVALID")
		}
		return grant, media.Fail("AUTH_UNAVAILABLE", "DRIVE", resp.StatusCode == 429 || resp.StatusCode >= 500)
	}
	if json.Unmarshal(raw, &grant) != nil || grant.AccessToken == "" || !strings.EqualFold(grant.TokenType, "Bearer") || grant.ExpiresIn < 120 || grant.ExpiresIn > 86400 {
		return tokenGrant{}, fault("PROVIDER_CHANGED")
	}
	return grant, nil
}

func (c *Client) principal(ctx context.Context, token string) (string, error) {
	base, err := c.base()
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/drive/v3/about?fields=user(permissionId)", nil)
	if err != nil {
		return "", fault("INVALID_DRIVE_REQUEST")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := authHTTP(c.HTTP).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", media.Fail("NETWORK_ERROR", "DRIVE", true)
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		if resp.StatusCode == 401 {
			return "", fault("AUTH_REQUIRED")
		}
		if resp.StatusCode == 403 {
			return "", fault("TARGET_PERMISSION_DENIED")
		}
		return "", media.Fail("AUTH_UNAVAILABLE", "DRIVE", resp.StatusCode == 429 || resp.StatusCode >= 500)
	}
	var about struct {
		User struct {
			PermissionID string `json:"permissionId"`
		} `json:"user"`
	}
	if err = decodeResponse(resp, &about); err != nil {
		return "", err
	}
	if about.User.PermissionID == "" {
		return "", fault("PROVIDER_CHANGED")
	}
	return about.User.PermissionID, nil
}

func authBinding(stored storedAuth) string {
	return digest([]byte(stored.AccountID + "\x00" + stored.PrincipalID + "\x00" + stored.ClientID))
}
