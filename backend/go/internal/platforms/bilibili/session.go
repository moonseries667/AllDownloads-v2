// Package bilibili owns the one v2 Bilibili account and its private session.
package bilibili

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
)

const AccountID = "bilibili-main"

type Config struct {
	CookieFile     string
	ExpectedUserID string
	// Endpoint overrides are only accepted for explicit loopback isolation.
	IsolatedTestMode bool
	APIBase          string
	PassportBase     string
	Client           *http.Client
}

type Session struct {
	cookieFile, userID string
	api, passport      *url.URL
	transport          http.RoundTripper
	isolated           bool
	mu                 sync.Mutex
	flow               *qrFlow
	boundID            string
}

type storedSession struct {
	SchemaVersion int    `json:"schemaVersion"`
	UserID        string `json:"userId"`
	Cookie        string `json:"cookie"`
}

type Account struct {
	AccountID      string `json:"accountId"`
	PlatformUserID string `json:"platformUserId"`
	State          string `json:"state"`
	ReasonCode     string `json:"reasonCode,omitempty"`
}

// New performs no file access or platform requests. Optional ExpectedUserID
// further constrains the identity saved by the first explicit QR login.
func New(cfg Config) (*Session, error) {
	if (cfg.ExpectedUserID != "" && !validUID(cfg.ExpectedUserID)) || !filepath.IsAbs(cfg.CookieFile) {
		return nil, fault("INVALID_ACCOUNT_CONFIG")
	}
	api, err := endpoint(cfg.APIBase, "https://api.bilibili.com", cfg.IsolatedTestMode)
	if err != nil {
		return nil, err
	}
	passport, err := endpoint(cfg.PassportBase, "https://passport.bilibili.com", cfg.IsolatedTestMode)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport
	if cfg.Client != nil && cfg.Client.Transport != nil {
		transport = cfg.Client.Transport
	}
	return &Session{cookieFile: filepath.Clean(cfg.CookieFile), userID: cfg.ExpectedUserID, api: api, passport: passport, transport: transport, isolated: cfg.IsolatedTestMode}, nil
}

func endpoint(override, standard string, isolated bool) (*url.URL, error) {
	if override == "" {
		u, _ := url.Parse(standard)
		return u, nil
	}
	u, err := url.Parse(override)
	if !isolated || err != nil || !loopback(u) || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, fault("INVALID_ACCOUNT_CONFIG")
	}
	u.Path = ""
	return u, nil
}

func loopback(u *url.URL) bool {
	if u == nil || u.Scheme != "http" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}

func fault(code string) error {
	return media.Fail(code, "BILIBILI_SESSION", code == "NETWORK_TEMPORARY" || code == "RATE_LIMITED")
}

func validUID(value string) bool {
	uid, err := strconv.ParseUint(value, 10, 64)
	return err == nil && uid > 0 && strconv.FormatUint(uid, 10) == value
}

// Bind reloads only the explicitly configured file, verifies nav, and returns an
// immutable API client. Subsequent file replacement cannot mix a source scan's
// pages or tracks with another session. No cookie jar is inherited.
func (s *Session) Bind(ctx context.Context) (*http.Client, string, error) {
	stored, err := s.readSession()
	if err != nil {
		return nil, "", err
	}
	s.mu.Lock()
	uid := stored.UserID
	if stored.SchemaVersion == 0 {
		// A raw cookie cannot establish an unknown identity implicitly.
		uid = s.userID
	}
	valid := uid != "" && (s.userID == "" || s.userID == uid) && (s.boundID == "" || s.boundID == uid)
	s.mu.Unlock()
	if !valid {
		return nil, "", fault("AUTH_REQUIRED")
	}
	cookie, err := validateCookie(stored.Cookie, uid)
	if err != nil {
		return nil, "", err
	}
	client := s.apiClient(cookie)
	if err := s.verify(ctx, client, uid); err != nil {
		return nil, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.boundID != "" && s.boundID != uid {
		return nil, "", fault("AUTH_REQUIRED")
	}
	s.boundID = uid
	return client, uid, nil
}

func (s *Session) readSession() (storedSession, error) {
	stored := storedSession{}
	f, err := os.Open(s.cookieFile)
	if err != nil {
		return stored, fault("AUTH_REQUIRED")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 64<<10+1))
	closeErr := f.Close()
	if err != nil || closeErr != nil || len(raw) > 64<<10 {
		return stored, fault("AUTH_REQUIRED")
	}
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		if json.Unmarshal(raw, &stored) != nil || stored.SchemaVersion != 1 || !validUID(stored.UserID) {
			return storedSession{}, fault("AUTH_REQUIRED")
		}
	} else {
		stored.Cookie = string(raw)
	}
	return stored, nil
}

func validateCookie(raw, uid string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\r\n") {
		return "", fault("AUTH_REQUIRED")
	}
	cookies, err := http.ParseCookie(raw)
	if err != nil {
		return "", fault("AUTH_REQUIRED")
	}
	seen := map[string]string{}
	for _, c := range cookies {
		if _, exists := seen[c.Name]; exists || c.Valid() != nil {
			return "", fault("AUTH_REQUIRED")
		}
		seen[c.Name] = c.Value
	}
	if seen["SESSDATA"] == "" || !validUID(uid) || seen["DedeUserID"] != uid {
		return "", fault("AUTH_REQUIRED")
	}
	return raw, nil
}

func (s *Session) Check(ctx context.Context) (Account, error) {
	account := Account{AccountID: AccountID, PlatformUserID: s.userID, State: "READY"}
	_, uid, err := s.Bind(ctx)
	if uid != "" {
		account.PlatformUserID = uid
	}
	if err != nil {
		account.State, account.ReasonCode = "UNAVAILABLE", media.Classify(err, "BILIBILI_SESSION").Code
		if account.ReasonCode == "AUTH_REQUIRED" {
			account.State = "AUTH_REQUIRED"
		}
	}
	return account, err
}

func (s *Session) verify(ctx context.Context, client *http.Client, uid string) error {
	var result struct {
		Code *int `json:"code"`
		Data struct {
			IsLogin bool        `json:"isLogin"`
			MID     json.Number `json:"mid"`
		} `json:"data"`
	}
	if _, err := requestJSON(ctx, client, s.api.String()+"/x/web-interface/nav", &result); err != nil {
		return err
	}
	if result.Code == nil {
		return fault("PLATFORM_RESPONSE_UNKNOWN")
	}
	if *result.Code != 0 {
		return apiCode(*result.Code)
	}
	if !result.Data.IsLogin || string(result.Data.MID) != uid {
		return fault("AUTH_REQUIRED")
	}
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func cleanRequest(req *http.Request) *http.Request {
	r := req.Clone(req.Context())
	r.Header.Del("Cookie")
	r.Header.Del("Authorization")
	r.Header.Del("Proxy-Authorization")
	r.Header.Del(media.BilibiliIdentityHeader)
	r.Header.Set("Referer", "https://www.bilibili.com/")
	r.Header.Set("User-Agent", "Mozilla/5.0")
	return r
}

func sameOrigin(a, b *url.URL) bool {
	return a != nil && a.User == nil && a.Scheme == b.Scheme && strings.EqualFold(a.Host, b.Host)
}

func statusResponse(req *http.Request, code int) *http.Response {
	return &http.Response{StatusCode: code, Status: http.StatusText(code), Header: make(http.Header), Body: http.NoBody, Request: req}
}

func (s *Session) apiClient(cookie string) *http.Client {
	return &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if !sameOrigin(req.URL, s.api) {
			return statusResponse(req, http.StatusUnauthorized), nil
		}
		r := cleanRequest(req)
		r.Header.Set("Cookie", cookie)
		res, err := s.transport.RoundTrip(r)
		if err == nil && res.StatusCode >= 300 && res.StatusCode < 400 {
			res.Body.Close()
			return statusResponse(req, http.StatusUnauthorized), nil
		}
		return res, err
	})}
}

// DownloadClient authenticates each new request (including redirects) against
// the snapshot UID. API cookies never go to CDN/subtitle/comment hosts. The
// signed media locator was obtained by that UID; it is not a second login.
func (s *Session) DownloadClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Minute, Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		identity := req.Header.Get(media.BilibiliIdentityHeader)
		if identity == "" {
			return statusResponse(req, http.StatusUnauthorized), nil
		}
		if !s.mediaURL(req.URL) {
			return statusResponse(req, http.StatusForbidden), nil
		}
		client, uid, err := s.Bind(req.Context())
		if err == nil && uid != identity {
			err = fault("AUTH_REQUIRED")
		}
		if err != nil {
			if req.Context().Err() != nil {
				return nil, req.Context().Err()
			}
			// Downloader maps HTTP statuses to controlled media failures; returning
			// a transport error would erase AUTH_REQUIRED as NETWORK_ERROR.
			code := http.StatusServiceUnavailable
			switch media.Classify(err, "DOWNLOAD").Code {
			case "AUTH_REQUIRED":
				code = http.StatusUnauthorized
			case "RATE_LIMITED":
				code = http.StatusTooManyRequests
			}
			return statusResponse(req, code), nil
		}
		if sameOrigin(req.URL, s.api) {
			return client.Transport.RoundTrip(req)
		}
		return s.transport.RoundTrip(cleanRequest(req))
	})}
}

func (s *Session) mediaURL(u *url.URL) bool {
	if u == nil || u.User != nil || u.Fragment != "" {
		return false
	}
	if s.isolated && loopback(u) {
		return true
	}
	if u.Scheme != "https" || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "api.bilibili.com" || host == "comment.bilibili.com" {
		return true
	}
	for _, domain := range []string{"bilivideo.com", "bilivideo.cn", "bilivideo.net", "hdslb.com"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func apiCode(code int) error {
	switch code {
	case -101:
		return fault("AUTH_REQUIRED")
	case -412, -352, -509:
		return fault("RATE_LIMITED")
	default:
		return fault("PLATFORM_RESPONSE_UNKNOWN")
	}
}

func requestJSON(ctx context.Context, client *http.Client, address string, result any) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, fault("INVALID_PLATFORM_REQUEST")
	}
	req.Header.Set("Referer", "https://www.bilibili.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	res, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fault("NETWORK_TEMPORARY")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, media.HTTPFailure(res.StatusCode, "BILIBILI_SESSION")
	}
	decoder := json.NewDecoder(io.LimitReader(res.Body, 1<<20))
	decoder.UseNumber()
	if decoder.Decode(result) != nil {
		return nil, fault("PLATFORM_RESPONSE_UNKNOWN")
	}
	return res, nil
}
