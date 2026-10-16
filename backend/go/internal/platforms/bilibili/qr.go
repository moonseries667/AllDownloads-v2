package bilibili

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
)

// QRLogin is private login UI data. URL contains the temporary scan key and
// belongs only in the current authorization UI, never media results or logs.
type QRLogin struct {
	ID         string    `json:"id"`
	State      string    `json:"state"`
	URL        string    `json:"url,omitempty"`
	ExpiresAt  time.Time `json:"expiresAt"`
	ReasonCode string    `json:"reasonCode,omitempty"`
}

type qrFlow struct {
	view     QRLogin
	key      string
	ctx      context.Context
	cancel   context.CancelFunc
	busy     bool
	nextPoll time.Time
}

func activeQR(state string) bool {
	return state == "STARTING" || state == "WAITING_SCAN" || state == "WAITING_CONFIRM"
}

func (s *Session) passportClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if !sameOrigin(req.URL, s.passport) {
			return statusResponse(req, http.StatusUnauthorized), nil
		}
		return s.transport.RoundTrip(cleanRequest(req))
	})}
}

// BeginQR only requests a scan URL. It neither confirms a login nor touches the
// existing cookie file. There can be at most one active local login flow.
func (s *Session) BeginQR(ctx context.Context) (QRLogin, error) {
	s.mu.Lock()
	if s.flow != nil && activeQR(s.flow.view.State) && time.Now().Before(s.flow.view.ExpiresAt) {
		s.mu.Unlock()
		return QRLogin{}, fault("LOGIN_IN_PROGRESS")
	}
	if s.flow != nil {
		s.flow.cancel()
	}
	flowCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(180*time.Second))
	f := &qrFlow{view: QRLogin{ID: rand.Text(), State: "STARTING", ExpiresAt: time.Now().Add(180 * time.Second)}, ctx: flowCtx, cancel: cancel, busy: true}
	s.flow = f
	s.mu.Unlock()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	var result struct {
		Code *int `json:"code"`
		Data struct {
			URL string `json:"url"`
			Key string `json:"qrcode_key"`
		} `json:"data"`
	}
	_, err := requestJSON(flowCtx, s.passportClient(), s.passport.String()+"/x/passport-login/web/qrcode/generate", &result)
	s.mu.Lock()
	defer s.mu.Unlock()
	f.busy = false
	if f.view.State == "CANCELLED" {
		return f.view, nil
	}
	if err == nil && (result.Code == nil || *result.Code != 0) {
		err = fault("PLATFORM_RESPONSE_UNKNOWN")
		if result.Code != nil && *result.Code != 0 {
			err = apiCode(*result.Code)
		}
	}
	scan := ""
	if err == nil {
		var valid bool
		scan, valid = canonicalScanURL(result.Data.URL, result.Data.Key)
		if !valid {
			err = fault("PLATFORM_RESPONSE_UNKNOWN")
		}
	}
	if err != nil {
		f.view.State, f.view.ReasonCode = "FAILED", "LOGIN_UNAVAILABLE"
		cancel()
		return f.view, err
	}
	f.key, f.view.URL, f.view.State = result.Data.Key, scan, "WAITING_SCAN"
	return f.view, nil
}

func canonicalScanURL(raw, key string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || key == "" || len(key) > 256 || strings.ContainsAny(key, "\r\n") || u.Scheme != "https" || u.User != nil || strings.Contains(raw, "#") || u.RawPath != "" {
		return "", false
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil || query.Get("qrcode_key") != key {
		return "", false
	}
	for _, values := range query {
		if len(values) != 1 {
			return "", false
		}
	}
	if callback, exists := query["callback"]; exists && callback[0] != "close" {
		return "", false
	}
	parameters := url.Values{"navhide": {"1"}, "from": {""}, "qrcode_key": {key}}
	switch {
	case u.Host == "passport.bilibili.com" && u.Path == "/h5-app/passport/login/scan":
	case u.Host == "account.bilibili.com" && u.Path == "/h5/account-h5/auth/scan-web" && query.Get("callback") == "close":
		parameters.Set("callback", "close")
	default:
		return "", false
	}
	// Rebuild the approved route rather than copying an upstream callback or
	// other query fields into the public QR content. The URL is never fetched.
	scan := url.URL{Scheme: "https", Host: u.Host, Path: u.Path, RawQuery: parameters.Encode()}
	return scan.String(), true
}

// PollQR returns only login state. Set-Cookie is accepted only after the user
// confirms on Bilibili and nav proves the bound UID; refresh_token and callback
// URLs are never returned or persisted. Polling is user-flow driven, not a job.
func (s *Session) PollQR(ctx context.Context, id string) (QRLogin, error) {
	s.mu.Lock()
	f := s.flow
	if f == nil || id == "" || f.view.ID != id {
		s.mu.Unlock()
		return QRLogin{}, fault("AUTH_FLOW_NOT_FOUND")
	}
	if !activeQR(f.view.State) {
		view := f.view
		s.mu.Unlock()
		return view, nil
	}
	if time.Now().After(f.view.ExpiresAt) {
		f.view.State, f.view.URL, f.key = "EXPIRED", "", ""
		f.cancel()
		view := f.view
		s.mu.Unlock()
		return view, nil
	}
	if f.busy || time.Now().Before(f.nextPoll) {
		view := f.view
		s.mu.Unlock()
		return view, nil
	}
	f.busy, f.nextPoll = true, time.Now().Add(2*time.Second)
	key, flowCtx := f.key, f.ctx
	s.mu.Unlock()
	opCtx, cancel := context.WithCancel(flowCtx)
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	defer cancel()
	var result struct {
		Code *int `json:"code"`
		Data struct {
			Code *int `json:"code"`
		} `json:"data"`
	}
	res, err := requestJSON(opCtx, s.passportClient(), s.passport.String()+"/x/passport-login/web/qrcode/poll?qrcode_key="+url.QueryEscape(key), &result)
	cookie, uid := "", ""
	state := ""
	if err == nil {
		if result.Code == nil || *result.Code != 0 || result.Data.Code == nil {
			err = fault("PLATFORM_RESPONSE_UNKNOWN")
			if result.Code != nil && *result.Code != 0 {
				err = apiCode(*result.Code)
			}
		} else {
			switch *result.Data.Code {
			case 86101:
				state = "WAITING_SCAN"
			case 86090:
				state = "WAITING_CONFIRM"
			case 86038:
				state = "EXPIRED"
			case 0:
				cookie, uid, err = loginCookies(res.Cookies())
				if err == nil {
					err = s.verify(opCtx, s.apiClient(cookie), uid)
				}
				state = "READY"
			default:
				err = fault("PLATFORM_RESPONSE_UNKNOWN")
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f.busy = false
	if f.view.State == "CANCELLED" {
		return f.view, nil
	}
	if time.Now().After(f.view.ExpiresAt) {
		state, err = "EXPIRED", nil
	}
	if err == nil && state != "EXPIRED" && opCtx.Err() != nil {
		err = opCtx.Err()
	}
	if err != nil {
		if media.Classify(err, "BILIBILI_SESSION").Code == "AUTH_REQUIRED" {
			f.view.State, f.view.ReasonCode, f.view.URL, f.key = "FAILED", "AUTH_REQUIRED", "", ""
			f.cancel()
		}
		return f.view, err
	}
	if state == "READY" {
		if err := s.saveLogin(cookie, uid); err != nil {
			if media.Classify(err, "").Code == "AUTH_REQUIRED" {
				f.view.State, f.view.ReasonCode, f.view.URL, f.key = "FAILED", "AUTH_REQUIRED", "", ""
				f.cancel()
			}
			return f.view, err
		}
	}
	f.view.State = state
	if !activeQR(state) {
		f.view.URL, f.key = "", ""
		f.cancel()
	}
	return f.view, nil
}

func (s *Session) CancelQR(id string) (QRLogin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.flow == nil || id == "" || s.flow.view.ID != id {
		return QRLogin{}, fault("AUTH_FLOW_NOT_FOUND")
	}
	f := s.flow
	if activeQR(f.view.State) {
		f.view.State, f.view.URL, f.key = "CANCELLED", "", ""
		f.cancel()
	}
	return f.view, nil
}

func loginCookies(cookies []*http.Cookie) (string, string, error) {
	values := map[string]string{}
	for _, c := range cookies {
		if c.Domain != "" && c.Domain != ".bilibili.com" && c.Domain != "bilibili.com" && c.Domain != "passport.bilibili.com" {
			return "", "", fault("AUTH_REQUIRED")
		}
		if c.MaxAge < 0 || (!c.Expires.IsZero() && time.Now().After(c.Expires)) || c.Valid() != nil {
			return "", "", fault("AUTH_REQUIRED")
		}
		switch c.Name {
		case "SESSDATA", "DedeUserID", "DedeUserID__ckMd5", "bili_jct":
			if _, exists := values[c.Name]; exists {
				return "", "", fault("AUTH_REQUIRED")
			}
			values[c.Name] = c.Value
		}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, (&http.Cookie{Name: name, Value: values[name]}).String())
	}
	cookie, err := validateCookie(strings.Join(parts, "; "), values["DedeUserID"])
	return cookie, values["DedeUserID"], err
}

// saveLogin is called with s.mu held. Clearing config cannot erase the identity
// saved in the private session or already verified by this process.
func (s *Session) saveLogin(cookie, uid string) error {
	if (s.userID != "" && s.userID != uid) || (s.boundID != "" && s.boundID != uid) {
		return fault("AUTH_REQUIRED")
	}
	stored, err := s.readSession()
	if err != nil {
		if _, statErr := os.Stat(s.cookieFile); !os.IsNotExist(statErr) {
			return fault("AUTH_REQUIRED")
		}
	} else {
		oldUID := stored.UserID
		if oldUID == "" {
			cookies, parseErr := http.ParseCookie(strings.TrimSpace(stored.Cookie))
			if parseErr != nil {
				return fault("AUTH_REQUIRED")
			}
			for _, c := range cookies {
				if c.Name == "DedeUserID" {
					oldUID = c.Value
				}
			}
		}
		if !validUID(oldUID) || oldUID != uid {
			return fault("AUTH_REQUIRED")
		}
	}
	if err := saveSession(s.cookieFile, storedSession{SchemaVersion: 1, UserID: uid, Cookie: cookie}); err != nil {
		return err
	}
	s.boundID = uid
	return nil
}

func saveSession(path string, session storedSession) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fault("SESSION_WRITE_FAILED")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".bilibili-login-*")
	if err != nil {
		return fault("SESSION_WRITE_FAILED")
	}
	defer os.Remove(f.Name())
	raw, err := json.Marshal(session)
	if err != nil {
		f.Close()
		return fault("SESSION_WRITE_FAILED")
	}
	_, writeErr := f.Write(raw)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return fault("SESSION_WRITE_FAILED")
	}
	if os.Rename(f.Name(), path) != nil {
		return fault("SESSION_WRITE_FAILED")
	}
	return nil
}
