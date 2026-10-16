package bilibili

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
)

const cookieOne = "DedeUserID=42; SESSDATA=fixture-session-one; bili_jct=fixture-csrf"
const cookieTwo = "DedeUserID=42; SESSDATA=fixture-session-two"

func newTestSession(t *testing.T, handler http.HandlerFunc) (*Session, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	s, err := New(Config{CookieFile: filepath.Join(t.TempDir(), "new-v2", "bilibili.cookie"), ExpectedUserID: "42", IsolatedTestMode: true, APIBase: server.URL, PassportBase: server.URL, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return s, server
}

func writeCookie(t *testing.T, s *Session, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(s.cookieFile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.cookieFile, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

func wantFault(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil || media.Classify(err, "").Code != code || strings.Contains(err.Error(), "fixture-session") || strings.Contains(err.Error(), "private-upstream") {
		t.Fatalf("expected %s; error=%v", code, err)
	}
}

func nav(w http.ResponseWriter) {
	fmt.Fprint(w, `{"code":0,"data":{"isLogin":true,"mid":42,"wbi_img":{"img_url":"https://example/abcdefghijklmnopqrstuvwxyz012345.png","sub_url":"https://example/ABCDEFGHIJKLMNOPQRSTUVWXYZ678901.png"}}}`)
}

func TestConfigDoesNotReadSessionOrPermitProductionOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.cookie")
	if _, err := New(Config{CookieFile: path, ExpectedUserID: "42"}); err != nil {
		t.Fatal(err)
	}
	for _, cfg := range []Config{
		{CookieFile: "relative", ExpectedUserID: "42"},
		{CookieFile: path, ExpectedUserID: "042"},
		{CookieFile: path, ExpectedUserID: "42", APIBase: "http://127.0.0.1:9000"},
		{CookieFile: path, ExpectedUserID: "42", IsolatedTestMode: true, APIBase: "https://external.invalid"},
		{CookieFile: path, ExpectedUserID: "42", IsolatedTestMode: true, PassportBase: "http://127.0.0.1/path"},
	} {
		_, err := New(cfg)
		wantFault(t, err, "INVALID_ACCOUNT_CONFIG")
	}
}

func TestMissingInvalidAndWrongIdentityNeverRequestAnonymous(t *testing.T) {
	var requests atomic.Int32
	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) { requests.Add(1); nav(w) })
	account, err := s.Check(context.Background())
	wantFault(t, err, "AUTH_REQUIRED")
	if account.AccountID != AccountID || account.State != "AUTH_REQUIRED" {
		t.Fatal(account)
	}
	for _, value := range []string{"", "SESSDATA=fixture-session-one", "DedeUserID=43; SESSDATA=fixture-session-one", "DedeUserID=42; SESSDATA=x; SESSDATA=y", "DedeUserID=42\r\nSESSDATA=x", strings.Repeat("x", 64<<10+1)} {
		writeCookie(t, s, value)
		_, _, err := s.Bind(context.Background())
		wantFault(t, err, "AUTH_REQUIRED")
	}
	if requests.Load() != 0 {
		t.Fatal("invalid session attempted a platform request")
	}
}

func TestNavRejectsExpiryIdentityAndMalformedResponses(t *testing.T) {
	for _, body := range []string{
		`{"code":-101,"data":{"isLogin":false,"wbi_img":{"img_url":"still-present"}},"message":"private-upstream"}`,
		`{"code":0,"data":{"isLogin":false,"mid":42}}`,
		`{"code":0,"data":{"isLogin":true,"mid":43}}`,
		`{"code":0,"data":{"isLogin":true,"mid":9007199254740993}}`,
	} {
		s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
		writeCookie(t, s, cookieOne)
		_, _, err := s.Bind(context.Background())
		wantFault(t, err, "AUTH_REQUIRED")
	}
	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"data":{"isLogin":true,"mid":42}}`) })
	writeCookie(t, s, cookieOne)
	_, _, err := s.Bind(context.Background())
	wantFault(t, err, "PLATFORM_RESPONSE_UNKNOWN")
}

func TestBoundSessionDoesNotReloadBetweenMetadataRequests(t *testing.T) {
	var seen atomic.Int32
	s, server := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != cookieOne || r.Header.Get(media.BilibiliIdentityHeader) != "" || r.Header.Get("Authorization") != "" {
			t.Error("incorrect API session or leaked private header")
		}
		seen.Add(1)
		nav(w)
	})
	writeCookie(t, s, cookieOne)
	client, uid, err := s.Bind(context.Background())
	if err != nil || uid != "42" {
		t.Fatal(uid, err)
	}
	writeCookie(t, s, cookieTwo)
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/x/web-interface/view", nil)
	req.Header.Set("Cookie", "not-the-session")
	req.Header.Set("Authorization", "not-an-api-token")
	req.Header.Set(media.BilibiliIdentityHeader, "42")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if seen.Load() != 2 {
		t.Fatal(seen.Load())
	}
}

func TestMetadataAndDownloadUseOneIdentityWithoutPersistingCookies(t *testing.T) {
	var s *Session
	var mediaGets atomic.Int32
	var server *httptest.Server
	s, server = newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/subtitle" && r.Header.Get("Cookie") != cookieOne {
			t.Error("metadata session changed in the middle of source refresh")
		}
		switch r.URL.Path {
		case "/x/web-interface/nav":
			nav(w)
		case "/x/v3/fav/resource/list":
			writeCookie(t, s, cookieTwo)
			fmt.Fprint(w, `{"code":0,"data":{"info":{"title":"收藏"},"medias":[{"bvid":"BVfixture"}],"has_more":false}}`)
		case "/x/web-interface/view":
			fmt.Fprint(w, `{"code":0,"data":{"bvid":"BVfixture","title":"标题","pages":[{"cid":11,"part":"标题"}]}}`)
		case "/x/player/wbi/playurl":
			fmt.Fprint(w, `{"code":0,"data":{"dash":{"video":[],"audio":[]}}}`)
		case "/x/player/wbi/v2":
			fmt.Fprintf(w, `{"code":0,"data":{"subtitle":{"subtitles":[{"id":1,"lan":"zh","type":0,"ai_type":0,"subtitle_url":"%s/subtitle"}]}}}`, server.URL)
		case "/x/v2/dm/web/view":
			w.Write([]byte{8, 0})
		default:
			mediaGets.Add(1)
			t.Error("metadata downloaded media")
		}
	})
	writeCookie(t, s, cookieOne)
	provider := &media.Bilibili{Session: s, APIBase: server.URL, CommentBase: server.URL}
	items, title, err := provider.FetchSource(context.Background(), "FAVORITE", "ml1", "https://www.bilibili.com/medialist/detail/ml1")
	if err != nil || title != "收藏" || len(items) != 1 {
		t.Fatal(title, items, err)
	}
	track := items[0].Units[0].Tracks[0]
	if track.Source.Headers.Get(media.BilibiliIdentityHeader) != "42" || !strings.HasPrefix(track.Source.Version, "bilibili:42:") || track.Source.Headers.Get("Cookie") != "" {
		t.Fatal("snapshot identity missing or credential persisted")
	}
	raw, _ := json.Marshal(items)
	if strings.Contains(string(raw), "fixture-session") || mediaGets.Load() != 0 {
		t.Fatal("credentials or implicit media I/O in metadata")
	}
}

func TestDownloadReauthenticatesAndStripsCredentialsAcrossRedirects(t *testing.T) {
	var navCount, hits atomic.Int32
	var expired atomic.Bool
	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) {
		navCount.Add(1)
		if r.Header.Get("Cookie") != cookieTwo {
			t.Error("download did not reload the new session for the same UID")
		}
		if expired.Load() {
			fmt.Fprint(w, `{"code":-101,"message":"private-upstream"}`)
		} else {
			nav(w)
		}
	})
	writeCookie(t, s, cookieTwo)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" || r.Header.Get(media.BilibiliIdentityHeader) != "" {
			t.Error("credentials or private identity leaked to CDN/redirect")
		}
		fmt.Fprint(w, "subtitle")
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			t.Error("cookie leaked to media origin")
		}
		http.Redirect(w, r, target.URL+"/final", http.StatusFound)
	}))
	defer redirect.Close()
	source := media.Source{URL: redirect.URL, Version: "bilibili:42:subtitle", Headers: http.Header{media.BilibiliIdentityHeader: {"42"}, "Cookie": {cookieOne}, "Authorization": {"fixture-auth"}, "Proxy-Authorization": {"fixture-proxy"}}}
	download := media.Downloader{Client: s.DownloadClient()}
	if err := download.Fetch(context.Background(), source, filepath.Join(t.TempDir(), "subtitle.part")); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 || navCount.Load() != 2 {
		t.Fatal("download/redirect were not independently authenticated", hits.Load(), navCount.Load())
	}
	expired.Store(true)
	err := download.Fetch(context.Background(), source, filepath.Join(t.TempDir(), "expired.part"))
	wantFault(t, err, "AUTH_REQUIRED")
	for _, uid := range []string{"43", ""} {
		source.Headers.Set(media.BilibiliIdentityHeader, uid)
		wantFault(t, download.Fetch(context.Background(), source, filepath.Join(t.TempDir(), "wrong.part")), "AUTH_REQUIRED")
	}
	if hits.Load() != 1 {
		t.Fatal("expired or different identity reached media origin")
	}
}

func TestAPIRedirectCannotLeakSession(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()
	s, _ := newTestSession(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) })
	writeCookie(t, s, cookieOne)
	_, _, err := s.Bind(context.Background())
	wantFault(t, err, "AUTH_REQUIRED")
	if hits.Load() != 0 {
		t.Fatal("API followed a redirect with session")
	}
	for _, address := range []string{"https://api.bilibili.com.evil.invalid/file", "http://api.bilibili.com/file", "https://evil.invalid/file", "https://user:password@api.bilibili.com/file"} {
		req, _ := http.NewRequest(http.MethodGet, address, nil)
		req.Header.Set(media.BilibiliIdentityHeader, "42")
		res, err := s.DownloadClient().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Fatal("unexpected media origin accepted")
		}
	}
}
