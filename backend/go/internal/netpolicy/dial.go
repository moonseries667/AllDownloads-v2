package netpolicy

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	connectTimeout = 15 * time.Second
	maxProxyHeader = 64 << 10
)

// DialContext returns a plain dialer for empty configuration, otherwise a TCP
// dialer using HTTP CONNECT. Context governs establishment, not the lifetime of
// a successfully returned connection, matching net.Dialer.DialContext.
func DialContext(proxyURL string) (func(context.Context, string, string) (net.Conn, error), error) {
	u, err := proxyAddress(proxyURL)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return (&net.Dialer{}).DialContext, nil
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" && network != "tcp4" && network != "tcp6" {
			return nil, ErrUnsupportedNetwork
		}
		if !validTarget(address) {
			return nil, ErrInvalidTarget
		}
		deadline := time.Now().Add(connectTimeout)
		if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
			deadline = end
		}
		// The proxy hop uses its own IP family. tcp6 targets can therefore be
		// tunneled through a local IPv4 proxy (and vice versa).
		conn, err := (&net.Dialer{Deadline: deadline}).DialContext(ctx, "tcp", u.Host)
		if err != nil {
			return nil, safeNetworkError(ctx, err, ErrProxyUnavailable)
		}
		success := false
		defer func() {
			if !success {
				conn.Close()
			}
		}()
		stop := context.AfterFunc(ctx, func() { conn.Close() })
		defer stop()
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, safeNetworkError(ctx, err, ErrProxyHandshake)
		}
		req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: address}, Host: address, Header: make(http.Header)}
		if err := req.Write(conn); err != nil {
			return nil, safeNetworkError(ctx, err, ErrProxyHandshake)
		}
		limited := &headerReader{reader: conn, remaining: maxProxyHeader, limited: true}
		buffered := bufio.NewReader(limited)
		res, err := http.ReadResponse(buffered, req)
		if err != nil {
			return nil, safeNetworkError(ctx, err, ErrProxyHandshake)
		}
		if res.StatusCode != http.StatusOK {
			// Close the socket before Body.Close, so an untrusted failure body
			// cannot keep the handshake open while being drained.
			conn.Close()
			res.Body.Close()
			if res.StatusCode == http.StatusProxyAuthRequired {
				return nil, ErrProxyAuthentication
			}
			return nil, ErrProxyHandshake
		}
		// CONNECT success starts a raw tunnel; ignore response-body framing
		// and retain bytes ReadResponse may already have buffered.
		limited.limited = false
		if err := conn.SetDeadline(time.Time{}); err != nil {
			return nil, safeNetworkError(ctx, err, ErrProxyHandshake)
		}
		if !stop() {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return nil, context.Canceled
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		success = true
		return &bufferedConn{Conn: conn, reader: buffered}, nil
	}, nil
}

func validTarget(address string) bool {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || strings.ContainsAny(host, " \t\r\n/#?@\x00") {
		return false
	}
	n, err := strconv.ParseUint(port, 10, 16)
	return err == nil && n > 0
}

type headerReader struct {
	reader    io.Reader
	remaining int
	limited   bool
}

func (r *headerReader) Read(p []byte) (int, error) {
	if r.limited {
		if r.remaining == 0 {
			return 0, ErrProxyHandshake
		}
		if len(p) > r.remaining {
			p = p[:r.remaining]
		}
	}
	n, err := r.reader.Read(p)
	if r.limited {
		r.remaining -= n
	}
	return n, err
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
