package netpolicy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProxyValidationAndRedactedErrors(t *testing.T) {
	for _, raw := range []string{
		"http://fixture-user:fixture-secret@127.0.0.1:40000", "https://127.0.0.1:40000", "socks5://127.0.0.1:40000",
		"http://external.invalid:40000", "http://localhost:40000", "http://192.0.2.1:40000", "http://127.0.0.1", "http://127.0.0.1:0", "http://127.0.0.1:65536",
		"http://127.0.0.1:40000/path", "http://127.0.0.1:40000/%2f", "http://127.0.0.1:40000?fixture-secret", "http://127.0.0.1:40000?", "http://127.0.0.1:40000#", " http://127.0.0.1:40000", "http://127.0.0.1:bad",
	} {
		_, clientErr := HTTPClient(raw, time.Second)
		_, dialErr := DialContext(raw)
		_, envErr := Environment(raw)
		for _, err := range []error{clientErr, dialErr, envErr} {
			if !errors.Is(err, ErrInvalidProxy) || strings.Contains(err.Error(), "fixture-secret") || strings.Contains(err.Error(), "127.0.0.1") {
				t.Fatal("invalid configuration was accepted or exposed", err)
			}
		}
	}
	for _, raw := range []string{"http://127.0.0.1:40000", "http://127.2.3.4:1234/", "http://[::1]:40000/"} {
		if _, err := Environment(raw); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHTTPClientForcesProxyForLoopbackAndClonesTransport(t *testing.T) {
	var originHits, proxyHits atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originHits.Add(1)
		fmt.Fprint(w, "via local proxy")
	}))
	defer origin.Close()
	direct := &http.Transport{Proxy: nil}
	defer direct.CloseIdleConnections()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits.Add(1)
		if !r.URL.IsAbs() || r.URL.String() != origin.URL+"/file" {
			t.Error("proxy did not receive absolute HTTP request")
		}
		forward := r.Clone(r.Context())
		forward.RequestURI = ""
		res, err := direct.RoundTrip(forward)
		if err != nil {
			w.WriteHeader(502)
			return
		}
		defer res.Body.Close()
		w.WriteHeader(res.StatusCode)
		io.Copy(w, res.Body)
	}))
	defer proxy.Close()
	t.Setenv("NO_PROXY", "*")
	t.Setenv("no_proxy", "*")
	base := http.DefaultTransport.(*http.Transport)
	baseProxy := reflect.ValueOf(base.Proxy).Pointer()
	client, err := HTTPClient(proxy.URL, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	other, err := HTTPClient(proxy.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer other.CloseIdleConnections()
	firstTransport := client.Transport.(*proxyTransport).transport
	secondTransport := other.Transport.(*proxyTransport).transport
	if firstTransport == secondTransport || firstTransport == base || secondTransport == base || client.Timeout != 2*time.Second {
		t.Fatal("HTTP transport was shared or timeout lost")
	}
	res, err := client.Get(origin.URL + "/file")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "via local proxy" || proxyHits.Load() != 1 || originHits.Load() != 1 {
		t.Fatal("explicit proxy was bypassed")
	}
	if http.DefaultTransport != base || reflect.ValueOf(base.Proxy).Pointer() != baseProxy || base.OnProxyConnectResponse != nil {
		t.Fatal("global transport was modified")
	}
}

func tunnel(w http.ResponseWriter, r *http.Request) {
	upstream, err := net.DialTimeout("tcp", r.Host, time.Second)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	client, buffered, err := w.(http.Hijacker).Hijack()
	if err != nil {
		upstream.Close()
		return
	}
	defer client.Close()
	defer upstream.Close()
	fmt.Fprint(buffered, "HTTP/1.1 200 Connection established\r\n\r\n")
	buffered.Flush()
	done := make(chan struct{})
	go func() {
		io.Copy(upstream, buffered)
		upstream.Close()
		client.Close()
		close(done)
	}()
	io.Copy(client, upstream)
	upstream.Close()
	client.Close()
	<-done
}

func TestHTTPClientHTTPSUsesCONNECTAndVerifiesOriginTLS(t *testing.T) {
	var connects atomic.Int32
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "TLS through CONNECT") }))
	defer origin.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || "https://"+r.Host != origin.URL {
			t.Error("expected CONNECT to TLS origin")
		}
		connects.Add(1)
		tunnel(w, r)
	}))
	defer proxy.Close()
	client, err := HTTPClient(proxy.URL, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	client.Transport.(*proxyTransport).transport.TLSClientConfig = &tls.Config{RootCAs: roots}
	res, err := client.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "TLS through CONNECT" || connects.Load() != 1 {
		t.Fatal("TLS request did not use proxy")
	}
}

func TestHTTPProxyFailureNeverFallsBackToOrigin(t *testing.T) {
	var originHits atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { originHits.Add(1) }))
	defer origin.Close()
	for _, scenario := range []string{"missing", "407-http", "407-connect", "502-connect", "dropped"} {
		t.Run(scenario, func(t *testing.T) {
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch scenario {
				case "407-http", "407-connect":
					w.WriteHeader(http.StatusProxyAuthRequired)
				case "502-connect":
					w.WriteHeader(http.StatusBadGateway)
				default:
					conn, _, _ := w.(http.Hijacker).Hijack()
					conn.Close()
				}
			}))
			defer proxy.Close()
			if scenario == "missing" {
				proxy.Close()
			}
			client, err := HTTPClient(proxy.URL, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			address := origin.URL
			want := ErrProxyUnavailable
			if strings.HasSuffix(scenario, "connect") {
				address = strings.Replace(address, "http:", "https:", 1)
			}
			if strings.HasPrefix(scenario, "407") {
				want = ErrProxyAuthentication
			}
			if scenario == "502-connect" {
				want = ErrProxyHandshake
			}
			_, err = client.Get(address)
			if !errors.Is(err, want) {
				t.Fatal("wrong proxy failure", err)
			}
		})
	}
	if originHits.Load() != 0 {
		t.Fatal("proxy failure fell back to direct connection")
	}
}

func TestEmptyPolicyRetainsDefaultsAndEnvironmentIsChildOnly(t *testing.T) {
	t.Setenv("HTTP_PROXY", "fixture-parent-http")
	t.Setenv("HTTPS_PROXY", "fixture-parent-https")
	t.Setenv("ALL_PROXY", "fixture-parent-all")
	t.Setenv("NO_PROXY", "fixture-parent-bypass")
	before := os.Environ()
	client, err := HTTPClient("", time.Second)
	if err != nil || client.Transport == http.DefaultTransport || client.Transport.(*http.Transport).Proxy == nil {
		t.Fatal("empty policy lost default HTTP semantics", err)
	}
	overrides, err := Environment("http://127.0.0.1:40000/")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		if overrides[name] != "http://127.0.0.1:40000" {
			t.Fatal("missing child proxy override")
		}
	}
	for _, name := range []string{"NO_PROXY", "no_proxy"} {
		if value, ok := overrides[name]; !ok || value != "" {
			t.Fatal("child inherited proxy bypass")
		}
	}
	empty, err := Environment("")
	if err != nil || len(empty) != 0 || !reflect.DeepEqual(before, os.Environ()) {
		t.Fatal("parent environment changed", err)
	}
	_, err = HTTPClient("http://127.0.0.1:bad", time.Second)
	if err == nil || !reflect.DeepEqual(before, os.Environ()) {
		t.Fatal("invalid policy changed environment")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dial, err := DialContext("")
	if err != nil {
		t.Fatal(err)
	}
	_, err = dial(ctx, "tcp", "127.0.0.1:1")
	if !errors.Is(err, context.Canceled) {
		t.Fatal("plain dialer did not preserve cancellation", err)
	}
}
