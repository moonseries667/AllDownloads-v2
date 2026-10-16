package quark

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	p "github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms"
)

const fixtureUID = "900719925474099312345"

func testNativeClient(serverURL string) *NativeClient {
	jar, _ := cookiejar.New(nil)
	return &NativeClient{
		AccountID:      "account-1",
		ExpectedUserID: fixtureUID,
		Cookie:         func() (string, error) { return "sid=synthetic-test-cookie", nil },
		HTTP:           &http.Client{Jar: jar},
		BaseURL:        serverURL,
		AccountURL:     serverURL + "/account/info",
	}
}

func TestNativeIdentityAndChildrenUseExactNumbersAndQuarkPaging(t *testing.T) {
	var cookieCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Cookie"); got != "sid=synthetic-test-cookie" {
			t.Errorf("cookie header = %q", got)
		}
		if r.Header.Get("Referer") != nativeReferer || r.Header.Get("User-Agent") != nativeUserAgent {
			t.Errorf("missing fixed browser headers: referer=%q ua=%q", r.Header.Get("Referer"), r.Header.Get("User-Agent"))
		}
		if r.URL.Query().Get("pr") != "ucpro" || r.URL.Query().Get("fr") != "pc" || r.URL.Query().Get("uc_param_str") != "" {
			t.Errorf("fixed query parameters missing: %v", r.URL.Query())
		}
		switch r.URL.Path {
		case "/account/info":
			if r.URL.Query().Get("platform") != "pc" {
				t.Errorf("account platform = %q", r.URL.Query().Get("platform"))
			}
			fmt.Fprint(w, `{"status":200,"code":0,"data":{"uid":900719925474099312345}}`)
		case "/1/clouddrive/file/sort":
			if r.URL.Query().Get("pdir_fid") != "0" || r.URL.Query().Get("_size") != "50" || r.URL.Query().Get("_fetch_total") != "1" || r.URL.Query().Get("_fetch_sub_dirs") != "0" || r.URL.Query().Get("_sort") != "file_type:asc,updated_at:desc" {
				t.Errorf("unexpected file/sort query: %v", r.URL.Query())
			}
			page, _ := strconv.Atoi(r.URL.Query().Get("_page"))
			var rows []map[string]any
			if page == 1 {
				rows = make([]map[string]any, 0, nativePageSize)
				for i := 1; i <= nativePageSize; i++ {
					name := fmt.Sprintf("file-%02d", i)
					if i == 1 || i == 2 {
						name = "同名.txt"
					}
					rows = append(rows, nativeFile(i, "0", name, true, 7, "sha-1", "md5-1"))
				}
			} else if page == 2 {
				rows = []map[string]any{nativeFile(51, "0", "最后一个.txt", true, 8, "sha-2", "md5-2")}
			} else {
				rows = []map[string]any{}
			}
			writeNativeEnvelope(t, w, map[string]any{"list": rows}, 51)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := testNativeClient(server.URL)
	client.Cookie = func() (string, error) {
		cookieCalls++
		return "sid=synthetic-test-cookie", nil
	}
	identity, err := client.Identity(context.Background())
	if err != nil || identity != fixtureUID {
		t.Fatalf("Identity() = %q, %v", identity, err)
	}
	first, err := client.Children(context.Background(), "account-1", "0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Complete || first.Next == nil || len(first.Nodes) != 50 {
		t.Fatalf("first page = count %d complete %v next %v", len(first.Nodes), first.Complete, first.Next)
	}
	if first.Nodes[0].Name != first.Nodes[1].Name || first.Nodes[0].ID == first.Nodes[1].ID {
		t.Fatalf("same-name nodes were merged: %#v %#v", first.Nodes[0], first.Nodes[1])
	}
	second, err := client.Children(context.Background(), "account-1", "0", first.Next)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Complete || second.Next != nil || len(second.Nodes) != 1 || second.Nodes[0].ID != "51" {
		t.Fatalf("second page = %#v", second)
	}
	if cookieCalls != 5 {
		t.Fatalf("cookie callback calls = %d, want 5", cookieCalls)
	}
}

func TestNativeNodeUsesRealParentAndListingVersionFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/account/info":
			fmt.Fprint(w, `{"status":200,"code":0,"data":{"uid":"`+fixtureUID+`"}}`)
		case "/1/clouddrive/file/info":
			if r.URL.Query().Get("fid") != "n-1" || r.URL.Query().Get("_fetch_full_path") != "0" {
				t.Errorf("unexpected file/info query: %v", r.URL.Query())
			}
			fmt.Fprint(w, `{"status":200,"code":0,"data":{"fid":"n-1","pdir_fid":"p-1","file_name":"report.bin"}}`)
		case "/1/clouddrive/file/sort":
			if r.URL.Query().Get("pdir_fid") != "p-1" {
				t.Errorf("parent query = %q", r.URL.Query().Get("pdir_fid"))
			}
			rows := []map[string]any{nativeFile("n-1", "p-1", "report.bin", true, 9007199254740993, "sha1-abc", "md5-def")}
			writeNativeEnvelope(t, w, map[string]any{"list": rows}, 1)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	node, err := testNativeClient(server.URL).Node(context.Background(), "account-1", "n-1")
	if err != nil {
		t.Fatal(err)
	}
	if node.ID != "n-1" || node.ParentID == nil || *node.ParentID != "p-1" || node.Kind != "FILE" || node.Name != "report.bin" {
		t.Fatalf("node mapping = %#v", node)
	}
	if node.SizeBytes == nil || *node.SizeBytes != "9007199254740993" {
		t.Fatalf("size lost precision: %#v", node.SizeBytes)
	}
	if node.ModifiedAt == nil || *node.ModifiedAt != "1970-01-02T10:17:36.789Z" {
		t.Fatalf("updated_at = %#v", node.ModifiedAt)
	}
	if node.Revision == "" {
		t.Fatal("revision is empty")
	}
}

func TestNativeRootAndFolderRevisionIgnoreFolderContentsAndTimestamp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":200,"code":0,"data":{"uid":"`+fixtureUID+`"}}`)
	}))
	defer server.Close()

	root, err := testNativeClient(server.URL).Root(context.Background(), "account-1")
	if err != nil {
		t.Fatal(err)
	}
	if root.ID != "0" || root.ParentID != nil || root.Name != "我的云盘" || root.Revision == "" {
		t.Fatalf("root = %#v", root)
	}
	first, err := parseNativeNode(json.RawMessage(`{"fid":"f-1","pdir_fid":"0","file_name":"目录","dir":true,"file":false,"updated_at":100,"size":0,"sha1":"a","md5":"b"}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := parseNativeNode(json.RawMessage(`{"fid":"f-1","pdir_fid":"0","file_name":"目录","dir":true,"file":false,"updated_at":200,"size":9,"sha1":"x","md5":"y"}`))
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != second.Revision {
		t.Fatalf("folder revision changed with folder contents/time: %s != %s", first.Revision, second.Revision)
	}
}

func TestNativeStatusErrorsAndRetryAfterAreStructured(t *testing.T) {
	cases := []struct {
		status int
		code   string
	}{
		{http.StatusUnauthorized, "AUTH_REQUIRED"},
		{http.StatusForbidden, "PERMISSION_DENIED"},
		{http.StatusNotFound, "RESOURCE_DELETED"},
		{http.StatusTooManyRequests, "RATE_LIMITED"},
	}
	for _, testCase := range cases {
		t.Run(testCase.code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if testCase.status == http.StatusTooManyRequests {
					w.Header().Set("Retry-After", "17")
				}
				w.WriteHeader(testCase.status)
			}))
			defer server.Close()
			rawClient := testNativeClient(server.URL)
			_, err := rawClient.Identity(context.Background())
			fault, ok := err.(*p.Fault)
			if !ok || fault.Code != testCase.code || fault.Stage != "IDENTITY" {
				t.Fatalf("Identity error = %#v, want %s/IDENTITY", err, testCase.code)
			}
			if testCase.status == http.StatusTooManyRequests && (fault.RetryAfterSeconds == nil || *fault.RetryAfterSeconds != 17) {
				t.Fatalf("RetryAfterSeconds = %#v", fault.RetryAfterSeconds)
			}
		})
	}
}

func TestNativeClientRejectsNonLoopbackOverridesAndRedirects(t *testing.T) {
	called := false
	client := testNativeClient("http://example.com")
	client.Cookie = func() (string, error) {
		called = true
		return "sid=synthetic-test-cookie", nil
	}
	if _, err := client.Identity(context.Background()); err == nil {
		t.Fatal("non-loopback HTTP override was accepted")
	}
	if called {
		t.Fatal("cookie callback ran for rejected endpoint")
	}

	var redirected atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirected.Store(true)
		fmt.Fprint(w, `{"status":200,"code":0,"data":{"uid":"`+fixtureUID+`"}}`)
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/account/info", http.StatusFound)
	}))
	defer redirector.Close()
	_, err := testNativeClient(redirector.URL).Identity(context.Background())
	fault, ok := err.(*p.Fault)
	if !ok || fault.Code != "PROVIDER_CHANGED" {
		t.Fatalf("redirect error = %#v", err)
	}
	if redirected.Load() {
		t.Fatal("redirect was followed")
	}
}

func TestNativeChildrenRejectsEmptyPageWithRemainingTotal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/account/info" {
			fmt.Fprint(w, `{"status":200,"code":0,"data":{"uid":"`+fixtureUID+`"}}`)
			return
		}
		writeNativeEnvelope(t, w, map[string]any{"list": []any{}}, 1)
	}))
	defer server.Close()
	_, err := testNativeClient(server.URL).Children(context.Background(), "account-1", "0", nil)
	fault, ok := err.(*p.Fault)
	if !ok || fault.Code != "PROVIDER_CHANGED" {
		t.Fatalf("empty page error = %#v", err)
	}
}

func nativeFile(id any, parent, name string, file bool, size any, sha1Value, md5Value string) map[string]any {
	return map[string]any{
		"fid":        id,
		"pdir_fid":   parent,
		"file_name":  name,
		"dir":        !file,
		"file":       file,
		"updated_at": 123456789,
		"size":       size,
		"sha1":       sha1Value,
		"md5":        md5Value,
	}
}

func writeNativeEnvelope(t *testing.T, w http.ResponseWriter, data any, total int) {
	t.Helper()
	encoded, err := json.Marshal(struct {
		Status   int `json:"status"`
		Code     int `json:"code"`
		Data     any `json:"data"`
		Metadata struct {
			Total int `json:"_total"`
		} `json:"metadata"`
	}{Status: http.StatusOK, Code: 0, Data: data, Metadata: struct {
		Total int `json:"_total"`
	}{Total: total}})
	if err != nil {
		t.Fatal(err)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(encoded)
}

func TestNativeQueryDoesNotMutateCallerValues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("pr"); got != "ucpro" {
			t.Errorf("pr = %q", got)
		}
		if got := r.URL.Query().Get("custom"); got != "kept" {
			t.Errorf("custom = %q", got)
		}
		fmt.Fprint(w, `{"status":200,"code":0,"data":{"uid":"`+fixtureUID+`"}}`)
	}))
	defer server.Close()
	client := testNativeClient(server.URL)
	query := url.Values{"custom": {"kept"}, "pr": {"overridden"}}
	if _, err := client.rpc(context.Background(), http.MethodGet, client.AccountURL, query, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(query.Get("pr"), "overridden") {
		t.Fatalf("rpc mutated caller query: %v", query)
	}
}

func TestNativeModifiedAtUsesQuarkMillisecondsAndNormalizesUTC(t *testing.T) {
	cases := []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{name: "small millisecond timestamp", raw: json.RawMessage(`123456789`), want: "1970-01-02T10:17:36.789Z"},
		{name: "numeric string remains milliseconds", raw: json.RawMessage(`"1700000000"`), want: "1970-01-20T16:13:20Z"},
		{name: "large millisecond timestamp", raw: json.RawMessage(`1700000000000`), want: "2023-11-14T22:13:20Z"},
		{name: "RFC3339 offset is normalized", raw: json.RawMessage(`"2026-10-15T12:30:00+08:00"`), want: "2026-10-15T04:30:00Z"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, original, ok := nativeModifiedAt(testCase.raw)
			if !ok || got == nil || *got != testCase.want {
				t.Fatalf("nativeModifiedAt(%s) = %#v, %q, %v; want %q", testCase.raw, got, original, ok, testCase.want)
			}
			if original != string(testCase.raw) {
				t.Fatalf("revision timestamp input = %q, want exact raw value %q", original, testCase.raw)
			}
		})
	}

	for _, raw := range []json.RawMessage{nil, json.RawMessage(`null`)} {
		got, _, ok := nativeModifiedAt(raw)
		if !ok || got != nil {
			t.Fatalf("missing timestamp %s = %#v, %v; want nil", raw, got, ok)
		}
	}
	for _, raw := range []json.RawMessage{json.RawMessage(`"not-a-timestamp"`), json.RawMessage(`9223372036854775808`)} {
		if _, _, ok := nativeModifiedAt(raw); ok {
			t.Fatalf("invalid or overflowing timestamp %s was accepted", raw)
		}
	}
}

func TestNativeFileRevisionIncludesSizeAndUnknownSizeMarker(t *testing.T) {
	parse := func(raw string) Node {
		t.Helper()
		node, err := parseNativeNode(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("parse file node: %v", err)
		}
		return node
	}
	base := parse(`{"fid":"file-1","pdir_fid":"0","file_name":"item.bin","dir":false,"file":true,"updated_at":123456789,"size":10,"sha1":"same-sha1","md5":"same-md5"}`)
	changedSize := parse(`{"fid":"file-1","pdir_fid":"0","file_name":"item.bin","dir":false,"file":true,"updated_at":123456789,"size":11,"sha1":"same-sha1","md5":"same-md5"}`)
	unknownSize := parse(`{"fid":"file-1","pdir_fid":"0","file_name":"item.bin","dir":false,"file":true,"updated_at":123456789,"sha1":"same-sha1","md5":"same-md5"}`)
	unknownSizeAgain := parse(`{"fid":"file-1","pdir_fid":"0","file_name":"item.bin","dir":false,"file":true,"updated_at":123456789,"sha1":"same-sha1","md5":"same-md5"}`)
	if base.Revision == changedSize.Revision {
		t.Fatal("file size change did not change revision")
	}
	if base.Revision == unknownSize.Revision {
		t.Fatal("unknown size reused a known-size revision")
	}
	if unknownSize.Revision != unknownSizeAgain.Revision {
		t.Fatal("stable unknown size did not produce a stable revision")
	}
}
