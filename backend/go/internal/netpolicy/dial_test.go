package netpolicy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func tcpServer(t *testing.T, serve func(net.Conn)) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var workers sync.WaitGroup
	connections := map[net.Conn]bool{}
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections[conn] = true
			mu.Unlock()
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer conn.Close()
				defer func() { mu.Lock(); delete(connections, conn); mu.Unlock() }()
				serve(conn)
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		<-acceptDone
		mu.Lock()
		for conn := range connections {
			conn.Close()
		}
		mu.Unlock()
		done := make(chan struct{})
		go func() { workers.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("local test connections did not stop")
		}
	})
	return listener.Addr().String()
}

func TestCONNECTForwardsTCPAndEmptyConfigUsesPlainDialer(t *testing.T) {
	var origins, connects atomic.Int32
	origin := tcpServer(t, func(conn net.Conn) {
		origins.Add(1)
		io.Copy(conn, conn)
	})
	proxy := tcpServer(t, func(conn net.Conn) {
		buffered := bufio.NewReader(conn)
		req, err := http.ReadRequest(buffered)
		if err != nil || req.Method != http.MethodConnect || req.Host != origin || req.Header.Get("Proxy-Authorization") != "" {
			t.Error("invalid CONNECT request")
			return
		}
		connects.Add(1)
		upstream, err := net.DialTimeout("tcp", req.Host, time.Second)
		if err != nil {
			t.Error(err)
			return
		}
		defer upstream.Close()
		fmt.Fprint(conn, "HTTP/1.1 200 Connection established\r\n\r\n")
		done := make(chan struct{})
		go func() {
			io.Copy(upstream, buffered)
			upstream.Close()
			conn.Close()
			close(done)
		}()
		io.Copy(conn, upstream)
		upstream.Close()
		conn.Close()
		<-done
	})
	for _, configured := range []string{"http://" + proxy, ""} {
		dial, err := DialContext(configured)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		conn, err := dial(ctx, "tcp", origin)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		conn.SetDeadline(time.Now().Add(time.Second))
		if _, err := io.WriteString(conn, "tunneled bytes"); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len("tunneled bytes"))
		_, err = io.ReadFull(conn, got)
		conn.Close()
		if err != nil || string(got) != "tunneled bytes" {
			t.Fatal("TCP transfer failed", err)
		}
	}
	if connects.Load() != 1 || origins.Load() != 2 {
		t.Fatal("unexpected proxy/direct routing")
	}
}

func TestCONNECTRetainsBufferedBytesAndDoesNotLimitTunnel(t *testing.T) {
	payload := "prebuffered:" + strings.Repeat("x", 2*maxProxyHeader)
	proxy := tcpServer(t, func(conn net.Conn) {
		_, err := http.ReadRequest(bufio.NewReader(conn))
		if err == nil {
			io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n"+payload)
		}
	})
	dial, _ := DialContext("http://" + proxy)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := dial(ctx, "tcp6", "[::1]:443")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(time.Second))
	got, err := io.ReadAll(conn)
	if err != nil || string(got) != payload {
		t.Fatal("buffered bytes were lost or handshake size limit leaked into tunnel", err, len(got))
	}
}

func TestCONNECTClearsHandshakeDeadlineAndCancellationAfterSuccess(t *testing.T) {
	release := make(chan struct{})
	proxy := tcpServer(t, func(conn net.Conn) {
		_, err := http.ReadRequest(bufio.NewReader(conn))
		if err != nil {
			return
		}
		fmt.Fprint(conn, "HTTP/1.1 200 Connection established\r\n\r\n")
		select {
		case <-release:
			fmt.Fprint(conn, "after dial deadline")
		case <-time.After(2 * time.Second):
		}
	})
	dial, _ := DialContext("http://" + proxy)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	conn, err := dial(ctx, "tcp", "127.0.0.1:443")
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	defer conn.Close()
	<-ctx.Done()
	close(release)
	// No replacement deadline: this must outlive both the original deadline
	// and its cancellation watcher, just like a successful net.Dialer call.
	got, err := io.ReadAll(conn)
	if err != nil || string(got) != "after dial deadline" {
		t.Fatal("handshake deadline or cancel watcher survived success", err)
	}
}

func TestCONNECTCancellationAndHandshakeDeadline(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprint(deadline), func(t *testing.T) {
			entered := make(chan struct{})
			proxy := tcpServer(t, func(conn net.Conn) {
				if _, err := http.ReadRequest(bufio.NewReader(conn)); err == nil {
					close(entered)
					io.Copy(io.Discard, conn)
				}
			})
			dial, _ := DialContext("http://" + proxy)
			ctx, cancel := context.WithCancel(context.Background())
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 150*time.Millisecond)
			}
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := dial(ctx, "tcp", "127.0.0.1:443"); done <- err }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("CONNECT did not start")
			}
			want := context.DeadlineExceeded
			if !deadline {
				cancel()
				want = context.Canceled
			}
			select {
			case err := <-done:
				if !errors.Is(err, want) {
					t.Fatal("wrong cancellation error", err)
				}
			case <-time.After(time.Second):
				t.Fatal("CONNECT ignored context cancellation/deadline")
			}
		})
	}
}

func TestCONNECTFailuresDoNotDialTargetAndDoNotExposeResponse(t *testing.T) {
	var directHits atomic.Int32
	origin := tcpServer(t, func(conn net.Conn) { directHits.Add(1) })
	for _, scenario := range []string{"407", "502", "eof", "large-header", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			proxy := tcpServer(t, func(conn net.Conn) {
				if _, err := http.ReadRequest(bufio.NewReader(conn)); err != nil {
					return
				}
				switch scenario {
				case "407", "502":
					fmt.Fprintf(conn, "HTTP/1.1 %s private-upstream-secret\r\nContent-Length: 0\r\n\r\n", scenario)
				case "large-header":
					io.WriteString(conn, "HTTP/1.1 200 OK\r\nX-Long: "+strings.Repeat("x", 2*maxProxyHeader)+"\r\n\r\n")
				}
			})
			if scenario == "missing" {
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				proxy = ln.Addr().String()
				ln.Close()
			}
			dial, err := DialContext("http://" + proxy)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err = dial(ctx, "tcp", origin)
			want := ErrProxyHandshake
			if scenario == "407" {
				want = ErrProxyAuthentication
			}
			if scenario == "missing" {
				want = ErrProxyUnavailable
			}
			if !errors.Is(err, want) || strings.Contains(err.Error(), "private-upstream") || strings.Contains(err.Error(), proxy) || strings.Contains(err.Error(), origin) {
				t.Fatal("uncontrolled CONNECT error", err)
			}
		})
	}
	if directHits.Load() != 0 {
		t.Fatal("failed CONNECT fell back to target")
	}
}

func TestCONNECTRejectsNonTCPAndInvalidTargetsBeforeDialing(t *testing.T) {
	var hits atomic.Int32
	proxy := tcpServer(t, func(conn net.Conn) { hits.Add(1) })
	dial, _ := DialContext("http://" + proxy)
	for _, network := range []string{"udp", "udp4", "unix", "TCP", ""} {
		_, err := dial(context.Background(), network, "127.0.0.1:80")
		if !errors.Is(err, ErrUnsupportedNetwork) {
			t.Fatal(err)
		}
	}
	for _, target := range []string{"", ":80", "127.0.0.1", "host:0", "host:65536", "host:80\r\nX-Secret: value", "user@host:80", "http://host:80"} {
		_, err := dial(context.Background(), "tcp", target)
		if !errors.Is(err, ErrInvalidTarget) {
			t.Fatal("invalid target accepted", err)
		}
	}
	if hits.Load() != 0 {
		t.Fatal("invalid network/target contacted proxy")
	}
}
