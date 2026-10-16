package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/tg"
	p "github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/telegram"
)

func TestIdentityResolverUsesHTTPConnectForTelegramTransport(t *testing.T) {
	const target = "192.0.2.20:443"
	handshake := make(chan bool, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != target || r.Header.Get("Proxy-Authorization") != "" {
			t.Errorf("unexpected proxy request %s %s", r.Method, r.Host)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		conn, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		if _, err := fmt.Fprint(buffered, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil || buffered.Flush() != nil {
			return
		}
		var marker [4]byte
		_, err = io.ReadFull(buffered, marker[:])
		handshake <- err == nil
	}))
	defer proxy.Close()
	options, err := identityClientOptions(proxy.URL)
	if err != nil || !options.NoUpdates {
		t.Fatalf("identity options: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := options.Resolver.Primary(ctx, 2, dcs.List{Options: []tg.DCOption{{ID: 2, IPAddress: "192.0.2.20", Port: 443}}})
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	select {
	case ok := <-handshake:
		if !ok {
			t.Fatal("Telegram transport did not travel through the CONNECT tunnel")
		}
	case <-ctx.Done():
		t.Fatal("proxy did not receive the Telegram transport handshake")
	}
}

func TestIdentityProxyFailureNeverDialsTargetDirectly(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	var directCalls, proxyCalls atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := target.Accept()
		if err == nil {
			directCalls.Add(1)
			_ = conn.Close()
		}
	}()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		if r.Method != http.MethodConnect || r.Host != target.Addr().String() {
			t.Errorf("unexpected proxy request %s %s", r.Method, r.Host)
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	options, err := identityClientOptions(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, portText, _ := net.SplitHostPort(target.Addr().String())
	port, _ := strconv.Atoi(portText)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := options.Resolver.Primary(ctx, 2, dcs.List{Options: []tg.DCOption{{ID: 2, IPAddress: host, Port: port}}})
	if conn != nil {
		_ = conn.Close()
	}
	_ = target.Close()
	<-done
	if err == nil || proxyCalls.Load() == 0 || directCalls.Load() != 0 {
		t.Fatalf("proxy failure bypassed: err=%v proxy=%d direct=%d", err, proxyCalls.Load(), directCalls.Load())
	}
}

func TestIdentityExtensionRejectsMissingChangedOrCredentialedProxy(t *testing.T) {
	for _, tc := range []struct{ expected, forwarded string }{
		{"http://127.0.0.1:40000", ""},
		{"http://127.0.0.1:40000", "http://127.0.0.1:40001"},
		{"http://synthetic-user:synthetic-password@127.0.0.1:40000", "http://synthetic-user:synthetic-password@127.0.0.1:40000"},
	} {
		raw, err := json.Marshal(extensionEnvironment{Name: telegram.IdentityExtension, Namespace: "alldownloads-v2", AppID: 123, AppHash: "synthetic-app", Session: []byte("synthetic-session"), Proxy: tc.forwarded})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "synthetic-extension.json")
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("TDL_EXTENSION", path)
		t.Setenv("TDL_PROXY", tc.expected)
		var output bytes.Buffer
		err = runIdentityExtension(context.Background(), &output)
		var fault *p.Fault
		if !errors.As(err, &fault) || fault.Code != "INVALID_INPUT" || output.Len() != 0 || strings.Contains(err.Error(), "synthetic-") {
			t.Fatalf("unsafe extension proxy accepted: %v", err)
		}
	}
}
