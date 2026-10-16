package bilibili

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func generateQR(w http.ResponseWriter) {
	fmt.Fprint(w, `{"code":0,"data":{"url":"https://passport.bilibili.com/h5-app/passport/login/scan?navhide=1&qrcode_key=fixture-key","qrcode_key":"fixture-key"}}`)
}

func allowPoll(s *Session) {
	s.mu.Lock()
	s.flow.nextPoll = time.Time{}
	s.mu.Unlock()
}

func assertCookieUnchanged(t *testing.T, s *Session) {
	t.Helper()
	raw, err := os.ReadFile(s.cookieFile)
	if err != nil || string(raw) != cookieOne {
		t.Fatal("old session was changed", err)
	}
}

func TestQRNewAccountRouteMatchesObservedGenerateResponse(t *testing.T) {
	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/x/passport-login/web/qrcode/generate" {
			t.Error("QR creation followed the scan URL or changed its request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, `{"code":0,"data":{"url":"https://account.bilibili.com/h5/account-h5/auth/scan-web?navhide=1&callback=close&from=&qrcode_key=fixture-key","qrcode_key":"fixture-key"}}`)
	})
	view, err := s.BeginQR(context.Background())
	if err != nil || view.State != "WAITING_SCAN" {
		t.Fatalf("observed official route rejected: state=%s err=%v", view.State, err)
	}
	defer s.CancelQR(view.ID)
	if view.URL != "https://account.bilibili.com/h5/account-h5/auth/scan-web?callback=close&from=&navhide=1&qrcode_key=fixture-key" {
		t.Fatalf("QR content changed the official route: %s", view.URL)
	}
}

func TestQRCanonicalRoutesDiscardUpstreamFields(t *testing.T) {
	for _, route := range []struct{ name, source, expected string }{
		{"legacy", "https://passport.bilibili.com/h5-app/passport/login/scan?navhide=1&qrcode_key=fixture-key&refresh_token=private-upstream", "https://passport.bilibili.com/h5-app/passport/login/scan?from=&navhide=1&qrcode_key=fixture-key"},
		{"account", "https://account.bilibili.com/h5/account-h5/auth/scan-web?navhide=1&callback=close&from=&qrcode_key=fixture-key&refresh_token=private-upstream", "https://account.bilibili.com/h5/account-h5/auth/scan-web?callback=close&from=&navhide=1&qrcode_key=fixture-key"},
	} {
		t.Run(route.name, func(t *testing.T) {
			s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/x/passport-login/web/qrcode/generate" {
					t.Error("QR creation followed an upstream URL")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"url": route.source, "qrcode_key": "fixture-key", "refresh_token": "private-upstream"}})
			})
			view, err := s.BeginQR(context.Background())
			if err != nil || view.State != "WAITING_SCAN" {
				t.Fatalf("official route rejected: state=%s err=%v", view.State, err)
			}
			defer s.CancelQR(view.ID)
			if view.URL != route.expected || strings.Contains(view.URL, "private-upstream") {
				t.Fatal("canonical QR included upstream fields or changed the approved route")
			}
		})
	}
}

func TestQRRejectsAmbiguousOrUnapprovedScanRoutes(t *testing.T) {
	const accountRoute = "https://account.bilibili.com/h5/account-h5/auth/scan-web"
	const query = "?navhide=1&callback=close&from=&qrcode_key=fixture-key"
	for _, test := range []struct{ name, source string }{
		{"wrong callback", accountRoute + "?navhide=1&callback=open&from=&qrcode_key=fixture-key"},
		{"missing callback", accountRoute + "?navhide=1&from=&qrcode_key=fixture-key"},
		{"duplicate callback", accountRoute + query + "&callback=close"},
		{"duplicate key", accountRoute + query + "&qrcode_key=fixture-key"},
		{"duplicate navhide", accountRoute + query + "&navhide=1"},
		{"duplicate extra parameter", accountRoute + query + "&extra=a&extra=b"},
		{"mismatched key", accountRoute + "?navhide=1&callback=close&from=&qrcode_key=wrong-key"},
		{"missing key", accountRoute + "?navhide=1&callback=close&from="},
		{"userinfo", "https://user@account.bilibili.com/h5/account-h5/auth/scan-web" + query},
		{"fragment", accountRoute + query + "#section"},
		{"empty fragment", accountRoute + query + "#"},
		{"explicit default port", "https://account.bilibili.com:443/h5/account-h5/auth/scan-web" + query},
		{"extra port", "https://account.bilibili.com:8443/h5/account-h5/auth/scan-web" + query},
		{"unapproved host", "https://account.bilibili.com.evil.invalid/h5/account-h5/auth/scan-web" + query},
		{"wrong account path", "https://account.bilibili.com/h5-app/passport/login/scan" + query},
		{"wrong passport path", "https://passport.bilibili.com/h5/account-h5/auth/scan-web" + query},
		{"encoded path", "https://account.bilibili.com/h5/account-h5/auth/%73can-web" + query},
		{"plain HTTP", "http://account.bilibili.com/h5/account-h5/auth/scan-web" + query},
		{"malformed query", accountRoute + query + "&extra=%ZZ"},
		{"legacy wrong callback", "https://passport.bilibili.com/h5-app/passport/login/scan?qrcode_key=fixture-key&callback=open"},
		{"legacy duplicate key", "https://passport.bilibili.com/h5-app/passport/login/scan?qrcode_key=fixture-key&qrcode_key=fixture-key"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"url": test.source, "qrcode_key": "fixture-key"}})
			})
			view, err := s.BeginQR(context.Background())
			wantFault(t, err, "PLATFORM_RESPONSE_UNKNOWN")
			if view.State != "FAILED" || view.URL != "" {
				t.Fatal("unapproved or ambiguous route became public QR content")
			}
		})
	}
}

func TestQRWaitConfirmSaveAndCheck(t *testing.T) {
	var code atomic.Int32
	var polls atomic.Int32
	code.Store(86101)
	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/passport-login/web/qrcode/generate":
			if r.Header.Get("Cookie") != "" {
				t.Error("existing session sent during QR creation")
			}
			generateQR(w)
		case "/x/passport-login/web/qrcode/poll":
			polls.Add(1)
			if r.Header.Get("Cookie") != "" || r.URL.Query().Get("qrcode_key") != "fixture-key" {
				t.Error("unexpected login request")
			}
			if code.Load() == 0 {
				http.SetCookie(w, &http.Cookie{Name: "DedeUserID", Value: "42", Domain: ".bilibili.com", Path: "/"})
				http.SetCookie(w, &http.Cookie{Name: "SESSDATA", Value: "fixture-session-qr", Domain: ".bilibili.com", Path: "/", HttpOnly: true, Secure: true})
			}
			fmt.Fprintf(w, `{"code":0,"data":{"code":%d,"url":"https://evil.invalid/?SESSDATA=private-upstream","refresh_token":"private-upstream"}}`, code.Load())
		case "/x/web-interface/nav":
			if !strings.Contains(r.Header.Get("Cookie"), "SESSDATA=fixture-session-qr") {
				t.Error("confirmed cookie was not verified")
			}
			nav(w)
		default:
			t.Error("login followed callback or expanded endpoints")
		}
	})
	writeCookie(t, s, cookieOne)
	view, err := s.BeginQR(context.Background())
	if err != nil || view.State != "WAITING_SCAN" || view.ID == "fixture-key" || view.URL == "" {
		t.Fatal(view.State, err)
	}
	_, err = s.BeginQR(context.Background())
	wantFault(t, err, "LOGIN_IN_PROGRESS")
	view, err = s.PollQR(context.Background(), view.ID)
	if err != nil || view.State != "WAITING_SCAN" {
		t.Fatal(view.State, err)
	}
	_, _ = s.PollQR(context.Background(), view.ID)
	if polls.Load() != 1 {
		t.Fatal("login poll rate limit missing")
	}
	assertCookieUnchanged(t, s)
	code.Store(86090)
	allowPoll(s)
	view, err = s.PollQR(context.Background(), view.ID)
	if err != nil || view.State != "WAITING_CONFIRM" {
		t.Fatal(view.State, err)
	}
	assertCookieUnchanged(t, s)
	code.Store(0)
	allowPoll(s)
	view, err = s.PollQR(context.Background(), view.ID)
	if err != nil || view.State != "READY" || view.URL != "" {
		t.Fatal(view.State, err)
	}
	raw, err := os.ReadFile(s.cookieFile)
	if err != nil || !strings.Contains(string(raw), "SESSDATA=fixture-session-qr") || strings.Contains(string(raw), "private-upstream") {
		t.Fatal("verified session not saved or callback/refresh token was persisted", err)
	}
	account, err := s.Check(context.Background())
	if err != nil || account.State != "READY" || account.AccountID != "bilibili-main" {
		t.Fatal(account, err)
	}
	public, _ := json.Marshal([]any{view, account})
	if strings.Contains(string(public), "SESSDATA") || strings.Contains(string(public), "private-upstream") {
		t.Fatal("credential exposed in account/login result")
	}
}

func TestQRWrongUserOrExpiredCookiesPreserveExistingSession(t *testing.T) {
	for _, scenario := range []string{"cookie-user", "nav-user", "expired", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/x/passport-login/web/qrcode/generate":
					generateQR(w)
				case "/x/passport-login/web/qrcode/poll":
					uid := "42"
					if scenario == "cookie-user" {
						uid = "43"
					}
					if scenario != "missing" {
						http.SetCookie(w, &http.Cookie{Name: "DedeUserID", Value: uid})
						c := &http.Cookie{Name: "SESSDATA", Value: "fixture-session-new"}
						if scenario == "expired" {
							c.Expires = time.Now().Add(-time.Hour)
						}
						http.SetCookie(w, c)
					}
					fmt.Fprint(w, `{"code":0,"data":{"code":0}}`)
				case "/x/web-interface/nav":
					fmt.Fprint(w, `{"code":0,"data":{"isLogin":true,"mid":43}}`)
				}
			})
			writeCookie(t, s, cookieOne)
			view, err := s.BeginQR(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			view, err = s.PollQR(context.Background(), view.ID)
			wantFault(t, err, "AUTH_REQUIRED")
			if view.State != "FAILED" || view.ReasonCode != "AUTH_REQUIRED" || view.URL != "" {
				t.Fatal(view.State, view.ReasonCode)
			}
			assertCookieUnchanged(t, s)
		})
	}
}

func TestQRCancelStopsInFlightPollWithoutSaving(t *testing.T) {
	entered := make(chan struct{})
	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/generate") {
			generateQR(w)
			return
		}
		close(entered)
		<-r.Context().Done()
	})
	writeCookie(t, s, cookieOne)
	view, err := s.BeginQR(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan QRLogin, 1)
	go func() {
		v, _ := s.PollQR(context.Background(), view.ID)
		done <- v
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("poll did not start")
	}
	cancelled, err := s.CancelQR(view.ID)
	if err != nil || cancelled.State != "CANCELLED" || cancelled.URL != "" {
		t.Fatal(cancelled.State, err)
	}
	select {
	case result := <-done:
		if result.State != "CANCELLED" {
			t.Fatal(result.State)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not stop HTTP poll")
	}
	assertCookieUnchanged(t, s)
}

func TestQRExpiredOrUnknownFlowDoesNotTouchSession(t *testing.T) {
	var calls atomic.Int32
	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); generateQR(w) })
	writeCookie(t, s, cookieOne)
	view, err := s.BeginQR(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.flow.view.ExpiresAt = time.Now().Add(-time.Second)
	s.mu.Unlock()
	view, err = s.PollQR(context.Background(), view.ID)
	if err != nil || view.State != "EXPIRED" || view.URL != "" || calls.Load() != 1 {
		t.Fatal(view.State, err)
	}
	_, err = s.PollQR(context.Background(), "unknown")
	wantFault(t, err, "AUTH_FLOW_NOT_FOUND")
	_, err = s.CancelQR("unknown")
	wantFault(t, err, "AUTH_FLOW_NOT_FOUND")
	assertCookieUnchanged(t, s)
}

func TestFirstExplicitQRBindsUIDAndEmptyConfigCannotRebind(t *testing.T) {
	var loginUID atomic.Int32
	loginUID.Store(42)
	base, server := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/passport-login/web/qrcode/generate":
			generateQR(w)
		case "/x/passport-login/web/qrcode/poll":
			http.SetCookie(w, &http.Cookie{Name: "DedeUserID", Value: fmt.Sprint(loginUID.Load())})
			http.SetCookie(w, &http.Cookie{Name: "SESSDATA", Value: "fixture-session-initial"})
			fmt.Fprint(w, `{"code":0,"data":{"code":0}}`)
		case "/x/web-interface/nav":
			c, _ := r.Cookie("DedeUserID")
			fmt.Fprintf(w, `{"code":0,"data":{"isLogin":true,"mid":%s}}`, c.Value)
		}
	})
	cfg := Config{CookieFile: base.cookieFile, IsolatedTestMode: true, APIBase: server.URL, PassportBase: server.URL}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	account, err := s.Check(context.Background())
	wantFault(t, err, "AUTH_REQUIRED")
	if account.PlatformUserID != "" {
		t.Fatal("missing new session invented an identity")
	}
	flow, err := s.BeginQR(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	flow, err = s.PollQR(context.Background(), flow.ID)
	if err != nil || flow.State != "READY" {
		t.Fatal(flow.State, err)
	}
	stored, err := s.readSession()
	if err != nil || stored.SchemaVersion != 1 || stored.UserID != "42" {
		t.Fatal("first explicit login did not persist UID", err)
	}
	before, _ := os.ReadFile(s.cookieFile)
	// A fresh process with expectedUserId omitted must keep the stored UID.
	s, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	account, err = s.Check(context.Background())
	if err != nil || account.State != "READY" || account.PlatformUserID != "42" {
		t.Fatal(account, err)
	}
	loginUID.Store(43)
	flow, err = s.BeginQR(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	flow, err = s.PollQR(context.Background(), flow.ID)
	wantFault(t, err, "AUTH_REQUIRED")
	if flow.State != "FAILED" {
		t.Fatal(flow.State)
	}
	after, _ := os.ReadFile(s.cookieFile)
	if string(before) != string(after) {
		t.Fatal("empty config allowed another UID to overwrite the binding")
	}
	cfg.ExpectedUserID = "43"
	other, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = other.Bind(context.Background())
	wantFault(t, err, "AUTH_REQUIRED")
}

func TestRawCookieCannotImplicitlyBindUnknownUID(t *testing.T) {
	base, server := newTestSession(t, func(w http.ResponseWriter, r *http.Request) { t.Error("raw cookie implicitly accessed platform") })
	writeCookie(t, base, cookieOne)
	s, err := New(Config{CookieFile: base.cookieFile, IsolatedTestMode: true, APIBase: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.Bind(context.Background())
	wantFault(t, err, "AUTH_REQUIRED")
}
