// Package netpolicy applies an explicit local HTTP proxy to selected clients.
// It never changes process environment, system settings or default transports.
package netpolicy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidProxy        = errors.New("netpolicy: invalid local HTTP proxy")
	ErrProxyUnavailable    = errors.New("netpolicy: proxy connection failed")
	ErrProxyAuthentication = errors.New("netpolicy: proxy authentication required")
	ErrProxyHandshake      = errors.New("netpolicy: proxy CONNECT failed")
	ErrProxyTimeout        = errors.New("netpolicy: proxy request timed out")
	ErrUnsupportedNetwork  = errors.New("netpolicy: CONNECT requires TCP")
	ErrInvalidTarget       = errors.New("netpolicy: invalid CONNECT target")
	ErrHTTPTransport       = errors.New("netpolicy: default HTTP transport cannot be cloned")
)

func proxyAddress(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") || (u.Path != "" && u.Path != "/") || u.RawPath != "" {
		return nil, ErrInvalidProxy
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		return nil, ErrInvalidProxy
	}
	ip := net.ParseIP(host)
	n, err := strconv.ParseUint(port, 10, 16)
	if ip == nil || !ip.IsLoopback() || err != nil || n == 0 {
		return nil, ErrInvalidProxy
	}
	return &url.URL{Scheme: "http", Host: net.JoinHostPort(ip.String(), strconv.FormatUint(n, 10))}, nil
}

// HTTPClient clones the default transport for each client. A nonempty proxy
// overrides environment/NO_PROXY, including loopback targets; it never retries
// without that proxy. Empty configuration retains default transport semantics.
func HTTPClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	u, err := proxyAddress(proxyURL)
	if err != nil {
		return nil, err
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, ErrHTTPTransport
	}
	transport := base.Clone()
	client := &http.Client{Timeout: timeout, Transport: transport}
	if u == nil {
		return client, nil
	}
	transport.Proxy = http.ProxyURL(u)
	transport.OnProxyConnectResponse = func(_ context.Context, _ *url.URL, _ *http.Request, res *http.Response) error {
		if res.StatusCode == http.StatusProxyAuthRequired {
			return ErrProxyAuthentication
		}
		if res.StatusCode != http.StatusOK {
			return ErrProxyHandshake
		}
		return nil
	}
	client.Transport = &proxyTransport{transport}
	return client, nil
}

type proxyTransport struct{ transport *http.Transport }

func (p *proxyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := p.transport.RoundTrip(req)
	if err != nil {
		if res != nil && res.Body != nil {
			res.Body.Close()
		}
		if errors.Is(err, ErrProxyAuthentication) {
			return nil, ErrProxyAuthentication
		}
		return nil, safeNetworkError(req.Context(), err, ErrProxyUnavailable)
	}
	if res.StatusCode == http.StatusProxyAuthRequired {
		res.Body.Close()
		return nil, ErrProxyAuthentication
	}
	return res, nil
}

func (p *proxyTransport) CloseIdleConnections() { p.transport.CloseIdleConnections() }

func safeNetworkError(ctx context.Context, err, fallback error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, ErrProxyHandshake) {
		return ErrProxyHandshake
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
			return context.DeadlineExceeded
		}
		return ErrProxyTimeout
	}
	return fallback
}

// Environment returns child-only overrides. Applying an empty result changes
// nothing. Lowercase aliases are included because some tools prefer them even
// when uppercase NO_PROXY is empty; inherited bypasses must not win.
func Environment(proxyURL string) (map[string]string, error) {
	u, err := proxyAddress(proxyURL)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, nil
	}
	result := make(map[string]string, 8)
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
		result[name], result[strings.ToLower(name)] = u.String(), u.String()
	}
	result["NO_PROXY"], result["no_proxy"] = "", ""
	return result, nil
}
