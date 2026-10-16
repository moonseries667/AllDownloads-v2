package quark

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFirstBindingAndCheckOnlyReadIdentity(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/account/info" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `{"status":200,"code":0,"data":{"uid":"new-test-user"}}`)
	}))
	defer server.Close()
	c := testNativeClient(server.URL)
	c.ExpectedUserID = ""
	if _, err := c.CheckSession(context.Background()); err == nil || calls != 0 {
		t.Fatal("unbound identity must not pass a business check")
	}
	uid, err := c.IdentifyForSetup(context.Background())
	if err != nil || uid != "new-test-user" || calls != 1 {
		t.Fatalf("first binding = %q, %v; calls=%d", uid, err, calls)
	}
	c.ExpectedUserID = uid
	if _, err = c.CheckSession(context.Background()); err != nil || calls != 2 {
		t.Fatalf("bound check: %v, calls=%d", err, calls)
	}
	c.ExpectedUserID = "different-user"
	if _, err = c.IdentifyForSetup(context.Background()); err == nil {
		t.Fatal("setup changed an existing binding")
	}
}
