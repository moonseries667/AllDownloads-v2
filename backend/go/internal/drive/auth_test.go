package drive

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func sha256Sum(value string) string {
	sum := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func persistentFixture(t *testing.T) (*fakeDrive, *Client) {
	t.Helper()
	f, legacy := newFakeDrive(t)
	root := t.TempDir()
	c, err := NewGoogle(GoogleConfig{AccountID: "account", TokenFile: filepath.Join(root, "drive.local.auth"), ClientFile: filepath.Join(root, "client.local.json")})
	if err != nil {
		t.Fatal(err)
	}
	c.HTTP, c.BaseURL = legacy.HTTP, legacy.BaseURL
	clientJSON := `{"installed":{"client_id":"synthetic-desktop-client","client_secret":"synthetic-client-secret"}}`
	if err = os.WriteFile(c.auth.config.ClientFile, []byte(clientJSON), 0600); err != nil {
		t.Fatal(err)
	}
	return f, c
}
func seedAuth(t *testing.T, c *Client, expiry time.Time) storedAuth {
	t.Helper()
	stored := storedAuth{Version: 1, AccountID: c.AccountID, PrincipalID: "principal-a", ClientID: "synthetic-desktop-client", ClientSecret: "synthetic-client-secret", AccessToken: "fixture-only", RefreshToken: "synthetic-refresh", Expiry: expiry, Scope: driveScope}
	unlock, err := lockPrivate(c.auth.config.TokenFile)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err = c.auth.write(stored); err != nil {
		t.Fatal(err)
	}
	return stored
}
func grantServer(t *testing.T, c *Client, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c.auth.tokenURL = server.URL + "/token"
}
func respondGrant(w http.ResponseWriter, refresh string) {
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fixture-only", "refresh_token": refresh, "token_type": "Bearer", "expires_in": 3600, "scope": driveScope})
}
func TestPersistentAuthRefreshOnceAcrossConcurrentReadsAndRestart(t *testing.T) {
	f, c := persistentFixture(t)
	seedAuth(t, c, time.Now().Add(-time.Hour))
	var calls atomic.Int32
	grantServer(t, c, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.ParseForm() != nil || r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "synthetic-refresh" {
			t.Error("invalid refresh contract")
			w.WriteHeader(400)
			return
		}
		// Google may omit refresh_token on refresh; preserve the old one.
		respondGrant(w, "")
	})
	var group sync.WaitGroup
	for i := 0; i < 12; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := c.Check(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	if calls.Load() != 1 {
		t.Fatal("concurrent requests did not share refresh", calls.Load())
	}
	stored, err := c.auth.read()
	if err != nil || stored.RefreshToken != "synthetic-refresh" || !stored.Expiry.After(time.Now()) {
		t.Fatal("refresh was not durably merged", err)
	}
	restarted, err := NewGoogle(c.auth.config)
	if err != nil {
		t.Fatal(err)
	}
	restarted.HTTP, restarted.BaseURL = c.HTTP, c.BaseURL
	if err = restarted.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	about, writes := f.aboutReads, f.writes
	f.mu.Unlock()
	if about != 2 || writes != 0 {
		t.Fatal("identity was not rechecked on restart or Check wrote", about, writes)
	}
	raw, err := os.ReadFile(c.auth.config.TokenFile)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" && (strings.Contains(string(raw), "synthetic-refresh") || strings.Contains(string(raw), "fixture-only")) {
		t.Fatal("Windows token store contains plaintext")
	}
}

func TestRefreshFailuresPreserveStoreAndNeverUpload(t *testing.T) {
	for _, mode := range []string{"revoked", "server", "malformed", "identity", "scope", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			f, c := persistentFixture(t)
			seedAuth(t, c, time.Now().Add(-time.Hour))
			state, root, req := archiveFixture(t, &Client{AccountID: c.AccountID, HTTP: c.HTTP, BaseURL: c.BaseURL, Token: func() string { return "fixture-only" }})
			before, err := os.ReadFile(c.auth.config.TokenFile)
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			grantServer(t, c, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch mode {
				case "revoked":
					w.WriteHeader(400)
					_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"synthetic-secret-must-not-leak"}`)
				case "server":
					w.WriteHeader(503)
					_, _ = io.WriteString(w, "synthetic-secret-must-not-leak")
				case "malformed":
					_, _ = io.WriteString(w, `{"access_token":"synthetic-secret-must-not-leak"}`)
				case "scope":
					_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fixture-only", "token_type": "Bearer", "expires_in": 3600, "scope": "openid"})
				case "redirect":
					w.Header().Set("Location", f.server.URL+"/forbidden")
					w.WriteHeader(307)
				default:
					respondGrant(w, "synthetic-rotated-refresh")
				}
			})
			if mode == "identity" {
				f.mu.Lock()
				f.principal = "principal-b"
				f.mu.Unlock()
			}
			result, err := c.Archive(context.Background(), state, "operation", root, req)
			if err == nil || result.Verified || strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatal("failed auth was accepted or leaked details")
			}
			after, readErr := os.ReadFile(c.auth.config.TokenFile)
			if readErr != nil || string(before) != string(after) {
				t.Fatal("failed refresh changed durable store", readErr)
			}
			f.mu.Lock()
			writes, puts, reserved := f.writes, f.puts, f.reserved
			f.mu.Unlock()
			if calls.Load() != 1 || writes != 0 || puts != 0 || reserved != 0 {
				t.Fatal("auth failure caused retry or remote write", calls.Load(), writes, puts, reserved)
			}
		})
	}
}

func TestPersistentArchiveLostResponseAndIdentityBinding(t *testing.T) {
	f, c := persistentFixture(t)
	seedAuth(t, c, time.Now().Add(time.Hour))
	state, root, req := archiveFixture(t, c)
	f.mu.Lock()
	f.loseResponse = true
	f.mu.Unlock()
	if result, err := c.Archive(context.Background(), state, "persisted", root, req); err == nil || result.Verified {
		t.Fatal("lost response accepted")
	}
	restarted, err := NewGoogle(c.auth.config)
	if err != nil {
		t.Fatal(err)
	}
	restarted.HTTP, restarted.BaseURL = c.HTTP, c.BaseURL
	if result, err := restarted.Verify(context.Background(), state, "persisted", root, req); err != nil || !result.Verified {
		t.Fatal("read-only recovery failed", err)
	}
	// Replace only this synthetic v2 store with another valid Google identity.
	stored, err := c.auth.read()
	if err != nil {
		t.Fatal(err)
	}
	stored.PrincipalID = "principal-b"
	if err = c.auth.write(stored); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.principal = "principal-b"
	f.mu.Unlock()
	other, err := NewGoogle(c.auth.config)
	if err != nil {
		t.Fatal(err)
	}
	other.HTTP, other.BaseURL = c.HTTP, c.BaseURL
	for _, verify := range []bool{true, false} {
		var result ArchiveResult
		if verify {
			result, err = other.Verify(context.Background(), state, "persisted", root, req)
		} else {
			result, err = other.Archive(context.Background(), state, "persisted", root, req)
		}
		if !isCode(err, "ACCOUNT_IDENTITY_CHANGED") || result.Verified {
			t.Fatal("old receipt adopted by new identity", err)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.puts != 1 || f.writes != 1 || f.reserved != 1 {
		t.Fatal("recovery repeated upload", f.puts, f.writes, f.reserved)
	}
}

type authRoundTrip func(*http.Request) (*http.Response, error)

func (f authRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEarlyRevocationRefreshesOnlyIdentityRead(t *testing.T) {
	_, c := persistentFixture(t)
	seedAuth(t, c, time.Now().Add(time.Hour))
	var about, refreshes atomic.Int32
	transport := http.DefaultTransport
	c.HTTP = &http.Client{Transport: authRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/drive/v3/about" && about.Add(1) == 1 {
			return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
		}
		return transport.RoundTrip(r)
	})}
	grantServer(t, c, func(w http.ResponseWriter, r *http.Request) { refreshes.Add(1); respondGrant(w, "synthetic-rotated") })
	if err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err := c.auth.read()
	if err != nil || stored.RefreshToken != "synthetic-rotated" || refreshes.Load() != 1 || about.Load() != 2 {
		t.Fatal("early revocation was not refreshed once", err)
	}
}

func TestUpload401DoesNotReplayAndRefreshesBeforeReconciliation(t *testing.T) {
	f, c := persistentFixture(t)
	seedAuth(t, c, time.Now().Add(time.Hour))
	state, root, req := archiveFixture(t, c)
	f.mu.Lock()
	f.rejectAuthPut = true
	f.mu.Unlock()
	if result, err := c.Archive(context.Background(), state, "auth-lost", root, req); !isCode(err, "AUTH_REQUIRED") || result.Verified {
		t.Fatal("401 upload was accepted or retried", err)
	}
	var refreshes atomic.Int32
	grantServer(t, c, func(w http.ResponseWriter, r *http.Request) { refreshes.Add(1); respondGrant(w, "") })
	if result, err := c.Verify(context.Background(), state, "auth-lost", root, req); err != nil || !result.Verified {
		t.Fatal("401 recovery failed", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if refreshes.Load() != 1 || f.puts != 1 || f.writes != 1 || f.reserved != 1 {
		t.Fatal("401 recovery repeated effects", refreshes.Load(), f.puts, f.writes, f.reserved)
	}
}

func TestTokenStoreLockAndLegacyReceiptCannotBeAdopted(t *testing.T) {
	_, c := persistentFixture(t)
	seedAuth(t, c, time.Now().Add(time.Hour))
	unlock, err := lockPrivate(c.auth.config.TokenFile)
	if err != nil {
		t.Fatal(err)
	}
	checkErr := c.Check(context.Background())
	unlock()
	if !isCode(checkErr, "AUTH_STORE_BUSY") {
		t.Fatal("concurrent process lock was ignored", checkErr)
	}
	legacy := &Client{AccountID: c.AccountID, HTTP: c.HTTP, BaseURL: c.BaseURL, Token: func() string { return "fixture-only" }}
	state, root, req := archiveFixture(t, legacy)
	if _, err = legacy.Archive(context.Background(), state, "legacy", root, req); err != nil {
		t.Fatal(err)
	}
	if result, err := c.Verify(context.Background(), state, "legacy", root, req); !isCode(err, "ACCOUNT_IDENTITY_CHANGED") || result.Verified {
		t.Fatal("legacy unbound receipt was silently adopted", err)
	}
}

func callbackURL(t *testing.T, session OAuthSession, state string) string {
	t.Helper()
	u, err := url.Parse(session.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	if state == "" {
		state = u.Query().Get("state")
	}
	return u.Query().Get("redirect_uri") + "?" + url.Values{"state": {state}, "code": {"synthetic-user-code"}}.Encode()
}
func invokeCallback(t *testing.T, address string) int {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func TestOAuthLoopbackPKCESuccessStateValidationAndRestart(t *testing.T) {
	_, c := persistentFixture(t)
	if err := c.Check(context.Background()); !isCode(err, "AUTH_REQUIRED") {
		t.Fatal(err)
	}
	var authorization url.Values
	var exchanges atomic.Int32
	grantServer(t, c, func(w http.ResponseWriter, r *http.Request) {
		exchanges.Add(1)
		if r.ParseForm() != nil {
			t.Error("invalid grant form")
			return
		}
		verifier := r.Form.Get("code_verifier")
		challenge := sha256Sum(verifier)
		if len(verifier) < 43 || challenge != authorization.Get("code_challenge") || r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("redirect_uri") != authorization.Get("redirect_uri") || r.Form.Get("code") != "synthetic-user-code" {
			t.Error("PKCE exchange did not match session")
		}
		respondGrant(w, "synthetic-new-refresh")
	})
	session, err := c.StartOAuth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = c.CancelOAuth(session.ID) })
	u, err := url.Parse(session.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	authorization = u.Query()
	if u.Host != "accounts.google.com" || authorization.Get("access_type") != "offline" || authorization.Get("code_challenge_method") != "S256" || authorization.Get("scope") != driveScope {
		t.Fatal("incorrect authorization request")
	}
	if _, err = c.StartOAuth(context.Background()); !isCode(err, "AUTH_SESSION_ACTIVE") {
		t.Fatal(err)
	}
	if status := invokeCallback(t, callbackURL(t, session, "incorrect-state")); status != 400 || exchanges.Load() != 0 {
		t.Fatal("invalid state consumed session")
	}
	if status := invokeCallback(t, callbackURL(t, session, "")); status != 200 {
		t.Fatal("callback failed", status)
	}
	view, err := c.PollOAuth(session.ID)
	if err != nil || view.State != "SUCCEEDED" || view.AuthorizationURL != "" {
		t.Fatal("session did not complete", err)
	}
	restarted, err := NewGoogle(c.auth.config)
	if err != nil {
		t.Fatal(err)
	}
	restarted.HTTP, restarted.BaseURL = c.HTTP, c.BaseURL
	if err = restarted.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if exchanges.Load() != 1 {
		t.Fatal("duplicate code exchange")
	}
}

func TestOAuthCancelMissingClientAndIdentityChange(t *testing.T) {
	f, c := persistentFixture(t)
	session, err := c.StartOAuth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	view, err := c.CancelOAuth(session.ID)
	if err != nil || view.State != "CANCELLED" || view.AuthorizationURL != "" {
		t.Fatal("cancel failed", err)
	}
	if _, err = c.PollOAuth("missing"); !isCode(err, "AUTH_SESSION_NOT_FOUND") {
		t.Fatal(err)
	}
	seedAuth(t, c, time.Now().Add(time.Hour))
	before, err := os.ReadFile(c.auth.config.TokenFile)
	if err != nil {
		t.Fatal(err)
	}
	grantServer(t, c, func(w http.ResponseWriter, r *http.Request) { respondGrant(w, "synthetic-new-refresh") })
	f.mu.Lock()
	f.principal = "principal-b"
	f.mu.Unlock()
	session, err = c.StartOAuth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = c.CancelOAuth(session.ID) })
	if status := invokeCallback(t, callbackURL(t, session, "")); status != 400 {
		t.Fatal(status)
	}
	view, err = c.PollOAuth(session.ID)
	if err != nil || view.State != "FAILED" || view.ReasonCode != "ACCOUNT_IDENTITY_CHANGED" {
		t.Fatal("changed identity accepted", err)
	}
	after, err := os.ReadFile(c.auth.config.TokenFile)
	if err != nil || string(before) != string(after) {
		t.Fatal("identity mismatch overwrote original store", err)
	}
	c.auth.config.ClientFile = filepath.Join(t.TempDir(), "missing.local.json")
	if _, err = c.StartOAuth(context.Background()); !isCode(err, "AUTH_CLIENT_REQUIRED") {
		t.Fatal(err)
	}
}

func TestOAuthPollAndCancelDuringExchangeDoNotBlockOrPersist(t *testing.T) {
	_, c := persistentFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	grantServer(t, c, func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		respondGrant(w, "synthetic-cancelled-refresh")
	})
	session, err := c.StartOAuth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = c.CancelOAuth(session.ID) })
	callbackDone := make(chan struct{})
	address := callbackURL(t, session, "")
	go func() {
		defer close(callbackDone)
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(address)
		if err == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("exchange did not start")
	}
	pollDone := make(chan OAuthSession, 1)
	go func() { view, _ := c.PollOAuth(session.ID); pollDone <- view }()
	select {
	case view := <-pollDone:
		if view.State != "PENDING" {
			t.Error("exchange has wrong state")
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("poll blocked behind token request")
	}
	view, err := c.CancelOAuth(session.ID)
	close(release)
	if err != nil || view.State != "CANCELLED" {
		t.Fatal("in-flight cancel failed", err)
	}
	select {
	case <-callbackDone:
	case <-time.After(5 * time.Second):
		t.Fatal("callback did not stop")
	}
	if _, err = os.Stat(c.auth.config.TokenFile); !os.IsNotExist(err) {
		t.Fatal("cancelled authorization persisted tokens", err)
	}
	view, err = c.PollOAuth(session.ID)
	if err != nil || view.State != "CANCELLED" {
		t.Fatal("late exchange revived cancelled session", err)
	}
}
