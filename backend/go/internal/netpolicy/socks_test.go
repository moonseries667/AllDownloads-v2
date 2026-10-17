package netpolicy

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func socksFixtureGreeting(conn net.Conn) error {
	var greeting [3]byte
	if _, err := io.ReadFull(conn, greeting[:]); err != nil {
		return err
	}
	if greeting != [3]byte{5, 1, 0} {
		return errors.New("SOCKS client did not request only anonymous authentication")
	}
	_, err := conn.Write([]byte{5, 0})
	return err
}

func socksFixtureTarget(conn net.Conn) (string, error) {
	var request [4]byte
	if _, err := io.ReadFull(conn, request[:]); err != nil {
		return "", err
	}
	if request[0] != 5 || request[1] != 1 || request[2] != 0 {
		return "", errors.New("SOCKS client did not request CONNECT")
	}
	var host string
	switch request[3] {
	case 1, 4:
		size := net.IPv4len
		if request[3] == 4 {
			size = net.IPv6len
		}
		address := make([]byte, size)
		if _, err := io.ReadFull(conn, address); err != nil {
			return "", err
		}
		host = net.IP(address).String()
	case 3:
		var size [1]byte
		if _, err := io.ReadFull(conn, size[:]); err != nil {
			return "", err
		}
		address := make([]byte, int(size[0]))
		if _, err := io.ReadFull(conn, address); err != nil {
			return "", err
		}
		host = string(address)
	default:
		return "", errors.New("SOCKS client sent an unknown address type")
	}
	var port [2]byte
	if _, err := io.ReadFull(conn, port[:]); err != nil {
		return "", err
	}
	return net.JoinHostPort(host, fmt.Sprint(binary.BigEndian.Uint16(port[:]))), nil
}

func socksFixtureSuccess(conn net.Conn) error {
	_, err := conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
	return err
}

func socksFixtureForward(t *testing.T, target string, hits *atomic.Int32) string {
	t.Helper()
	return tcpServer(t, func(conn net.Conn) {
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		if err := socksFixtureGreeting(conn); err != nil {
			t.Error(err)
			return
		}
		address, err := socksFixtureTarget(conn)
		if err != nil || address != target {
			t.Errorf("unexpected SOCKS target %q: %v", address, err)
			return
		}
		if hits != nil {
			hits.Add(1)
		}
		upstream, err := net.DialTimeout("tcp", target, time.Second)
		if err != nil {
			t.Error(err)
			return
		}
		defer upstream.Close()
		if err := socksFixtureSuccess(conn); err != nil {
			t.Error(err)
			return
		}
		done := make(chan struct{})
		go func() {
			io.Copy(upstream, conn)
			upstream.Close()
			conn.Close()
			close(done)
		}()
		io.Copy(conn, upstream)
		upstream.Close()
		conn.Close()
		<-done
	})
}

func TestSOCKS5AnonymousConnectForwardsTCP(t *testing.T) {
	origin := tcpServer(t, func(conn net.Conn) { io.Copy(conn, conn) })
	proxy := socksFixtureForward(t, origin, nil)
	dial, err := DialContext("socks5://" + proxy)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	conn, err := dial(ctx, "tcp", origin)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := io.WriteString(conn, "anonymous SOCKS tunnel"); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("anonymous SOCKS tunnel"))
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != "anonymous SOCKS tunnel" {
		t.Fatal("SOCKS tunnel did not forward bytes after dial context cancellation", err)
	}
}

func TestSOCKS5PreservesTargetsAndEarlyTunnelBytes(t *testing.T) {
	for _, target := range []string{"synthetic.invalid:443", "[2001:db8::20]:443"} {
		t.Run(target, func(t *testing.T) {
			proxy := tcpServer(t, func(conn net.Conn) {
				if err := socksFixtureGreeting(conn); err != nil {
					t.Error(err)
					return
				}
				address, err := socksFixtureTarget(conn)
				if err != nil || address != target {
					t.Errorf("target was resolved locally or changed: %q %v", address, err)
					return
				}
				conn.Write(append([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}, []byte("early tunnel bytes")...))
			})
			dial, err := DialContext("socks5://" + proxy)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			conn, err := dial(ctx, "tcp6", target)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			got, err := io.ReadAll(conn)
			if err != nil || string(got) != "early tunnel bytes" {
				t.Fatal("SOCKS reply consumed tunnel bytes", err)
			}
		})
	}
}

func TestSOCKS5FailuresStayAnonymousAndNeverDialTarget(t *testing.T) {
	var directHits atomic.Int32
	origin := tcpServer(t, func(net.Conn) { directHits.Add(1) })
	for _, mode := range []string{"authentication", "no-method", "version", "refused", "eof", "missing"} {
		t.Run(mode, func(t *testing.T) {
			proxy := tcpServer(t, func(conn net.Conn) {
				conn.SetDeadline(time.Now().Add(time.Second))
				var greeting [3]byte
				if _, err := io.ReadFull(conn, greeting[:]); err != nil || greeting != [3]byte{5, 1, 0} {
					t.Error("SOCKS greeting advertised authentication credentials")
					return
				}
				switch mode {
				case "eof":
					return
				case "no-method":
					conn.Write([]byte{5, 255})
					return
				case "version":
					conn.Write([]byte{6, 0})
					return
				case "authentication":
					conn.Write([]byte{5, 2})
				default:
					conn.Write([]byte{5, 0})
				}
				if _, err := socksFixtureTarget(conn); err != nil {
					return
				}
				if mode == "authentication" {
					_ = socksFixtureSuccess(conn)
				} else {
					conn.Write([]byte{5, 5, 0, 1, 127, 0, 0, 1, 0, 0})
				}
			})
			if mode == "missing" {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				proxy = listener.Addr().String()
				listener.Close()
			}
			dial, err := DialContext("socks5://" + proxy)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			conn, err := dial(ctx, "tcp", origin)
			if conn != nil {
				conn.Close()
			}
			want := ErrProxyHandshake
			if mode == "authentication" || mode == "no-method" {
				want = ErrProxyAuthentication
			}
			if mode == "missing" {
				want = ErrProxyUnavailable
			}
			if err != want {
				t.Fatalf("SOCKS failure was accepted or exposed raw details: wanted=%v err=%v", want, err)
			}
		})
	}
	if directHits.Load() != 0 {
		t.Fatal("SOCKS failure fell back to the target")
	}
}

func TestSOCKS5CancellationAndDeadlineCloseHandshakeConnection(t *testing.T) {
	for _, phase := range []string{"method", "connect"} {
		for _, deadline := range []bool{false, true} {
			t.Run(fmt.Sprint(phase, deadline), func(t *testing.T) {
				entered, stopped := make(chan struct{}), make(chan struct{})
				proxy := tcpServer(t, func(conn net.Conn) {
					defer close(stopped)
					var greeting [3]byte
					if _, err := io.ReadFull(conn, greeting[:]); err != nil {
						return
					}
					if phase == "connect" {
						conn.Write([]byte{5, 0})
						if _, err := socksFixtureTarget(conn); err != nil {
							return
						}
					}
					close(entered)
					io.Copy(io.Discard, conn)
				})
				dial, err := DialContext("socks5://" + proxy)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				if deadline {
					cancel()
					ctx, cancel = context.WithTimeout(context.Background(), 150*time.Millisecond)
				}
				defer cancel()
				done := make(chan error, 1)
				go func() { _, err := dial(ctx, "tcp", "192.0.2.20:443"); done <- err }()
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("SOCKS handshake did not start")
				}
				want := context.DeadlineExceeded
				if !deadline {
					cancel()
					want = context.Canceled
				}
				select {
				case err := <-done:
					if !errors.Is(err, want) {
						t.Fatal("SOCKS handshake ignored cancellation/deadline", err)
					}
				case <-time.After(time.Second):
					t.Fatal("SOCKS handshake remained blocked")
				}
				select {
				case <-stopped:
				case <-time.After(time.Second):
					t.Fatal("cancelled SOCKS proxy connection was not closed")
				}
			})
		}
	}
}

func TestSOCKS5SuccessOutlivesEstablishmentDeadline(t *testing.T) {
	release := make(chan struct{})
	proxy := tcpServer(t, func(conn net.Conn) {
		if socksFixtureGreeting(conn) != nil {
			return
		}
		if _, err := socksFixtureTarget(conn); err != nil || socksFixtureSuccess(conn) != nil {
			return
		}
		select {
		case <-release:
			io.WriteString(conn, "after establishment deadline")
		case <-time.After(2 * time.Second):
		}
	})
	dial, err := DialContext("socks5://" + proxy)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	conn, err := dial(ctx, "tcp", "192.0.2.20:443")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	<-ctx.Done()
	close(release)
	got, err := io.ReadAll(conn)
	if err != nil || string(got) != "after establishment deadline" {
		t.Fatal("SOCKS dial watcher or deadline survived success", err)
	}
}

func TestSOCKS5HTTPClientForcesProxyAndPreservesOriginTLS(t *testing.T) {
	t.Setenv("NO_PROXY", "*")
	t.Setenv("no_proxy", "*")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprint(secure), func(t *testing.T) {
			var origins, connects atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				origins.Add(1)
				if req.Header.Get("Proxy-Authorization") != "" || req.RequestURI != "/file" {
					t.Error("origin received proxy framing or credentials")
				}
				fmt.Fprint(w, "HTTP through anonymous SOCKS")
			})
			var origin *httptest.Server
			if secure {
				origin = httptest.NewTLSServer(handler)
			} else {
				origin = httptest.NewServer(handler)
			}
			defer origin.Close()
			u, _ := url.Parse(origin.URL)
			proxy := socksFixtureForward(t, u.Host, &connects)
			client, err := HTTPClient("socks5://"+proxy, 2*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			if secure {
				roots := x509.NewCertPool()
				roots.AddCert(origin.Certificate())
				client.Transport.(*proxyTransport).transport.TLSClientConfig = &tls.Config{RootCAs: roots}
			}
			res, err := client.Get(origin.URL + "/file")
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(res.Body)
			res.Body.Close()
			if err != nil || string(body) != "HTTP through anonymous SOCKS" || origins.Load() != 1 || connects.Load() != 1 {
				t.Fatal("HTTP bypassed SOCKS or origin TLS failed", err)
			}
		})
	}
}

func TestSOCKS5HTTPFailuresStayControlledWithoutDirectFallback(t *testing.T) {
	var originHits atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { originHits.Add(1) }))
	defer origin.Close()
	for _, mode := range []string{"authentication", "refused"} {
		t.Run(mode, func(t *testing.T) {
			proxy := tcpServer(t, func(conn net.Conn) {
				conn.SetDeadline(time.Now().Add(time.Second))
				var greeting [3]byte
				if _, err := io.ReadFull(conn, greeting[:]); err != nil {
					return
				}
				if mode == "authentication" {
					conn.Write([]byte{5, 2})
				} else {
					conn.Write([]byte{5, 0})
				}
				if _, err := socksFixtureTarget(conn); err != nil {
					return
				}
				if mode == "refused" {
					conn.Write([]byte{5, 5, 0, 1, 127, 0, 0, 1, 0, 0})
					return
				}
				_ = socksFixtureSuccess(conn)
				if _, err := http.ReadRequest(bufio.NewReader(conn)); err == nil {
					io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
				}
			})
			client, err := HTTPClient("socks5://"+proxy, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			res, err := client.Get(origin.URL)
			if res != nil {
				res.Body.Close()
			}
			want := ErrProxyHandshake
			if mode == "authentication" {
				want = ErrProxyAuthentication
			}
			var urlErr *url.Error
			if !errors.As(err, &urlErr) || urlErr.Err != want {
				t.Fatalf("HTTP exposed or accepted a SOCKS handshake failure: wanted=%v err=%v", want, err)
			}
		})
	}
	if originHits.Load() != 0 {
		t.Fatal("failed SOCKS HTTP request bypassed the proxy")
	}
}

func TestSOCKS5ValidationAndEnvironmentRemainChildOnly(t *testing.T) {
	for _, suffix := range []string{"fixture-user:fixture-secret@127.0.0.1:40000", "external.invalid:40000", "localhost:40000", "192.0.2.1:40000", "127.0.0.1", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:40000/path", "127.0.0.1:40000?", "127.0.0.1:40000#", "127.0.0.1:40000?key=fixture-secret"} {
		configuration := "socks5://" + suffix
		_, clientErr := HTTPClient(configuration, time.Second)
		_, dialErr := DialContext(configuration)
		_, envErr := Environment(configuration)
		for _, err := range []error{clientErr, dialErr, envErr} {
			if err != ErrInvalidProxy {
				t.Fatal("invalid SOCKS configuration was accepted or exposed", err)
			}
		}
	}
	for _, configuration := range []string{"socks5://127.0.0.1:40000/", "socks5://[::1]:40000/"} {
		t.Setenv("ALL_PROXY", "synthetic-parent-proxy")
		t.Setenv("NO_PROXY", "synthetic-parent-bypass")
		before := os.Environ()
		overrides, err := Environment(configuration)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
			if overrides[key] != strings.TrimSuffix(configuration, "/") {
				t.Fatal("SOCKS child proxy was changed to another scheme")
			}
		}
		if overrides["NO_PROXY"] != "" || overrides["no_proxy"] != "" || !reflect.DeepEqual(before, os.Environ()) {
			t.Fatal("SOCKS environment changed the parent or retained a bypass")
		}
	}
}

func TestSOCKS5RejectsNonTCPAndInvalidTargetsBeforeDialing(t *testing.T) {
	var hits atomic.Int32
	proxy := tcpServer(t, func(net.Conn) { hits.Add(1) })
	dial, err := DialContext("socks5://" + proxy)
	if err != nil {
		t.Fatal(err)
	}
	for _, network := range []string{"udp", "udp4", "unix", "TCP", ""} {
		if _, err := dial(context.Background(), network, "192.0.2.20:443"); err != ErrUnsupportedNetwork {
			t.Fatal(err)
		}
	}
	for _, target := range []string{"", ":80", "127.0.0.1", "host:0", "host:65536", "host:80\r\nX-Secret: value", "user@host:80", "http://host:80"} {
		if _, err := dial(context.Background(), "tcp", target); err != ErrInvalidTarget {
			t.Fatal("invalid SOCKS target was accepted", err)
		}
	}
	if hits.Load() != 0 {
		t.Fatal("invalid network or target contacted the SOCKS proxy")
	}
}
