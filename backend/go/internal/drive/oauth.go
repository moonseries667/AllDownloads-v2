package drive

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
)

// OAuthSession exposes only progress and the user-facing consent URL. Codes,
// PKCE verifiers, tokens and the provider's raw error text are never returned.
// These are Go component states; the runner maps them to the frozen wire model.
type OAuthSession struct {
	ID               string
	State            string
	AuthorizationURL string
	ExpiresAt        time.Time
	ReasonCode       string
}
type oauthSession struct {
	mu         sync.Mutex
	view       OAuthSession
	cancel     context.CancelFunc
	cancelled  atomic.Bool
	processing bool
}
type desktopClient struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

func (a *googleAuth) desktopClient() (desktopClient, error) {
	var file struct {
		Installed desktopClient `json:"installed"`
	}
	if a.config.ClientFile == "" {
		return file.Installed, fault("AUTH_CLIENT_REQUIRED")
	}
	if err := privatePath(a.config.ClientFile); err != nil {
		return file.Installed, fault("AUTH_CONFIG_INVALID")
	}
	f, err := os.Open(a.config.ClientFile)
	if err != nil {
		return file.Installed, fault("AUTH_CLIENT_REQUIRED")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
		return file.Installed, fault("AUTH_CONFIG_INVALID")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(raw) > 65536 || json.Unmarshal(raw, &file) != nil || file.Installed.ClientID == "" {
		return desktopClient{}, fault("AUTH_CONFIG_INVALID")
	}
	return file.Installed, nil
}

func randomOAuth() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fault("AUTH_UNAVAILABLE")
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}

func (c *Client) StartOAuth(ctx context.Context) (OAuthSession, error) {
	if err := ctx.Err(); err != nil {
		return OAuthSession{}, err
	}
	if c.auth == nil {
		return OAuthSession{}, fault("AUTH_CONFIG_INVALID")
	}
	a := c.auth
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	for id, s := range a.sessions {
		s.mu.Lock()
		pending := s.view.State == "PENDING"
		expired := s.view.ExpiresAt.Add(10 * time.Minute).Before(time.Now())
		s.mu.Unlock()
		if pending {
			return OAuthSession{}, fault("AUTH_SESSION_ACTIVE")
		}
		if expired {
			delete(a.sessions, id)
		}
	}
	if len(a.sessions) >= 16 {
		return OAuthSession{}, fault("AUTH_SESSION_LIMIT")
	}
	desktop, err := a.desktopClient()
	if err != nil {
		return OAuthSession{}, err
	}
	// An existing account may renew consent, but cannot change OAuth client or
	// principal underneath old upload receipts. A missing store is first login.
	unlock, err := lockPrivate(a.config.TokenFile)
	if err != nil {
		return OAuthSession{}, storeLockFailure(err)
	}
	previous, readErr := a.read()
	unlock()
	if readErr != nil && !isCode(readErr, "AUTH_REQUIRED") {
		return OAuthSession{}, readErr
	}
	if readErr == nil && previous.ClientID != desktop.ClientID {
		return OAuthSession{}, fault("ACCOUNT_IDENTITY_CHANGED")
	}
	endpoint, err := privateEndpoint(a.authorizeURL, "https://accounts.google.com/o/oauth2/v2/auth")
	if err != nil {
		return OAuthSession{}, err
	}
	id, err := randomOAuth()
	if err != nil {
		return OAuthSession{}, err
	}
	state, err := randomOAuth()
	if err != nil {
		return OAuthSession{}, err
	}
	verifier, err := randomOAuth()
	if err != nil {
		return OAuthSession{}, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return OAuthSession{}, fault("AUTH_CALLBACK_UNAVAILABLE")
	}
	redirect := "http://" + listener.Addr().String() + "/oauth2/callback"
	challenge := sha256.Sum256([]byte(verifier))
	params := url.Values{"client_id": {desktop.ClientID}, "redirect_uri": {redirect}, "response_type": {"code"}, "scope": {driveScope}, "state": {state}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"}, "access_type": {"offline"}, "prompt": {"consent select_account"}}
	expires := time.Now().Add(5 * time.Minute)
	// An operation/request context is short-lived; the user's explicit session
	// remains pending until completion, CancelOAuth, or the five-minute deadline.
	sessionCtx, cancel := context.WithDeadline(context.WithoutCancel(ctx), expires)
	s := &oauthSession{view: OAuthSession{ID: id, State: "PENDING", AuthorizationURL: endpoint + "?" + params.Encode(), ExpiresAt: expires}, cancel: cancel}
	a.sessions[id] = s
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 35 * time.Second, MaxHeaderBytes: 8192}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.Method != http.MethodGet || r.URL.Path != "/oauth2/callback" || r.Host != listener.Addr().String() || len(r.URL.RawQuery) > 8192 {
			http.Error(w, "Invalid callback", 400)
			return
		}
		query := r.URL.Query()
		if len(query["state"]) != 1 || subtle.ConstantTimeCompare([]byte(query.Get("state")), []byte(state)) != 1 {
			http.Error(w, "Invalid callback", 400)
			return
		}
		s.mu.Lock()
		if s.view.State != "PENDING" || s.processing || sessionCtx.Err() != nil {
			s.mu.Unlock()
			http.Error(w, "Session ended", 410)
			return
		}
		s.processing = true
		s.mu.Unlock()
		var err error
		var stored storedAuth
		if query.Get("error") != "" {
			err = fault("AUTH_DENIED")
		} else if len(query["code"]) != 1 || query.Get("code") == "" {
			err = fault("AUTH_CALLBACK_INVALID")
		} else {
			stored, err = c.exchangeOAuth(sessionCtx, desktop, query.Get("code"), redirect, verifier, previous)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.view.State != "PENDING" {
			http.Error(w, "Session ended", 410)
			return
		}
		if err == nil {
			err = c.saveOAuth(sessionCtx, stored)
		}
		if err == nil {
			s.view.State = "SUCCEEDED"
		} else if s.cancelled.Load() {
			s.view.State = "CANCELLED"
		} else {
			s.view.State = "FAILED"
			s.view.ReasonCode = oauthReason(err)
		}
		s.view.AuthorizationURL = ""
		cancel()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if err != nil {
			w.WriteHeader(400)
			_, _ = io.WriteString(w, "Authorization did not complete. Return to AllDownloads for the reason.")
		} else {
			_, _ = io.WriteString(w, "Authorization saved. You can close this window and return to AllDownloads.")
		}
	})
	view := s.view
	go func() {
		err := server.Serve(listener)
		if err != nil && err != http.ErrServerClosed {
			s.mu.Lock()
			if s.view.State == "PENDING" {
				s.view.State = "FAILED"
				s.view.ReasonCode = "AUTH_CALLBACK_UNAVAILABLE"
				s.view.AuthorizationURL = ""
			}
			s.mu.Unlock()
			cancel()
		}
	}()
	go func() {
		<-sessionCtx.Done()
		shutdownCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = server.Shutdown(shutdownCtx)
		_ = listener.Close()
		s.mu.Lock()
		if s.view.State == "PENDING" && sessionCtx.Err() == context.DeadlineExceeded {
			s.view.State = "EXPIRED"
			s.view.ReasonCode = "AUTH_SESSION_EXPIRED"
			s.view.AuthorizationURL = ""
		}
		s.mu.Unlock()
	}()
	return view, nil
}

func oauthReason(err error) string {
	if f, ok := err.(*media.Failure); ok {
		return f.Code
	}
	return "AUTH_UNAVAILABLE"
}

func (c *Client) oauthSession(id string) (*oauthSession, error) {
	if c.auth == nil {
		return nil, fault("AUTH_CONFIG_INVALID")
	}
	c.auth.sessionMu.Lock()
	defer c.auth.sessionMu.Unlock()
	s := c.auth.sessions[id]
	if s == nil {
		return nil, fault("AUTH_SESSION_NOT_FOUND")
	}
	return s, nil
}
func (c *Client) PollOAuth(id string) (OAuthSession, error) {
	s, err := c.oauthSession(id)
	if err != nil {
		return OAuthSession{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.view, nil
}
func (c *Client) CancelOAuth(id string) (OAuthSession, error) {
	s, err := c.oauthSession(id)
	if err != nil {
		return OAuthSession{}, err
	}
	// Cancel interrupts an in-flight exchange. Only the final private file
	// commit holds s.mu; success already committed
	// remains success instead of falsely claiming it was undone.
	s.cancelled.Store(true)
	s.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.view.State == "PENDING" {
		s.view.State = "CANCELLED"
		s.view.ReasonCode = ""
		s.view.AuthorizationURL = ""
	}
	return s.view, nil
}

func (c *Client) exchangeOAuth(ctx context.Context, desktop desktopClient, code, redirect, verifier string, expected storedAuth) (storedAuth, error) {
	a := c.auth
	grant, err := a.grant(ctx, c.HTTP, url.Values{"grant_type": {"authorization_code"}, "client_id": {desktop.ClientID}, "client_secret": {desktop.ClientSecret}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {verifier}})
	if err != nil {
		return storedAuth{}, err
	}
	if grant.RefreshToken == "" {
		return storedAuth{}, fault("AUTH_REFRESH_REQUIRED")
	}
	if !hasDriveScope(grant.Scope) {
		return storedAuth{}, fault("AUTH_SCOPE_REQUIRED")
	}
	principal, err := c.principal(ctx, grant.AccessToken)
	if err != nil {
		return storedAuth{}, err
	}
	if expected.PrincipalID != "" && (expected.PrincipalID != principal || expected.ClientID != desktop.ClientID) {
		return storedAuth{}, fault("ACCOUNT_IDENTITY_CHANGED")
	}
	stored := storedAuth{Version: 1, AccountID: c.AccountID, PrincipalID: principal, ClientID: desktop.ClientID, ClientSecret: desktop.ClientSecret, AccessToken: grant.AccessToken, RefreshToken: grant.RefreshToken, Expiry: time.Now().Add(time.Duration(grant.ExpiresIn) * time.Second), Scope: grant.Scope}
	return stored, nil
}

func (c *Client) saveOAuth(ctx context.Context, stored storedAuth) error {
	a := c.auth
	unlock, err := lockPrivate(a.config.TokenFile)
	if err != nil {
		return storeLockFailure(err)
	}
	defer unlock()
	// Another runner could have authorized while this browser was open.
	current, err := a.read()
	if err != nil && !isCode(err, "AUTH_REQUIRED") {
		return err
	}
	if err == nil && authBinding(current) != authBinding(stored) {
		return fault("ACCOUNT_IDENTITY_CHANGED")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return a.write(stored)
}
