package quark

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"hash"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	p "github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms"
)

type checkedBody struct {
	io.ReadCloser
	cancel    context.CancelFunc
	remaining int64
	hash      hash.Hash
	expected  string
}

func (b *checkedBody) Read(buf []byte) (int, error) {
	n, e := b.ReadCloser.Read(buf)
	b.remaining -= int64(n)
	if b.hash != nil {
		b.hash.Write(buf[:n])
	}
	if b.remaining < 0 {
		return n, p.NewFault("VERIFICATION_FAILED", "QUARK_DOWNLOAD")
	}
	if e == io.EOF && b.remaining != 0 {
		return n, p.NewFault("VERIFICATION_FAILED", "QUARK_DOWNLOAD")
	}
	if e == io.EOF && b.hash != nil && !strings.EqualFold(hex.EncodeToString(b.hash.Sum(nil)), b.expected) {
		return n, p.NewFault("VERIFICATION_FAILED", "QUARK_DOWNLOAD")
	}
	return n, e
}
func (b *checkedBody) Close() error { b.cancel(); return b.ReadCloser.Close() }
func (t *Transfers) OpenNode(ctx context.Context, account string, want CloudNode) (io.ReadCloser, error) {
	c, e := t.client(account)
	if e != nil {
		return nil, e
	}
	user, e := c.Identity(ctx)
	if e != nil {
		return nil, e
	}
	registry, e := t.loadShare(account, want.ShareID)
	if e != nil {
		return nil, e
	}
	if registry.UserID != user {
		return nil, p.NewFault("ACCOUNT_IDENTITY_CHANGED", "QUARK_DOWNLOAD")
	}
	managed := false
	for _, entry := range registry.Entries {
		if entry.RemoteID == want.ExternalID {
			managed = true
			break
		}
	}
	if !managed || want.ID != want.ExternalID {
		return nil, p.NewFault("PERMISSION_DENIED", "QUARK_DOWNLOAD")
	}
	current, e := c.Node(ctx, account, want.ExternalID)
	if e != nil {
		return nil, e
	}
	if current.Revision != want.Version || current.Kind != "FILE" || current.Availability != "AVAILABLE" || want.SizeBytes == nil || current.SizeBytes == nil || *current.SizeBytes != *want.SizeBytes {
		return nil, p.NewFault("VERSION_CHANGED", "QUARK_DOWNLOAD")
	}
	actual, e := c.remoteEntry(ctx, want.ExternalID)
	if e != nil {
		return nil, e
	}
	var checksum hash.Hash
	var expectedHash string
	if actual.SHA1 != "" {
		checksum = sha1.New()
		expectedHash = actual.SHA1
	} else if actual.MD5 != "" {
		checksum = md5.New()
		expectedHash = actual.MD5
	} else {
		return nil, p.NewFault("VERIFICATION_FAILED", "QUARK_DOWNLOAD")
	}
	response, e := c.rpc(ctx, http.MethodPost, "/1/clouddrive/file/download", nil, map[string]any{"fids": []string{want.ExternalID}})
	if e != nil {
		return nil, e
	}
	var entries []struct {
		ID  string `json:"fid"`
		URL string `json:"download_url"`
	}
	if json.Unmarshal(response.Data, &entries) != nil || len(entries) != 1 || entries[0].ID != want.ExternalID {
		return nil, p.NewFault("PROVIDER_CHANGED", "QUARK_DOWNLOAD")
	}
	u, e := url.Parse(entries[0].URL)
	loopback := u != nil && u.Scheme == "http" && net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsLoopback() && c.BaseURL != ""
	if e != nil || u.User != nil || u.Fragment != "" || (!loopback && (u.Scheme != "https" || !(u.Hostname() == "quark.cn" || strings.HasSuffix(u.Hostname(), ".quark.cn") || strings.HasSuffix(u.Hostname(), ".uc.cn")))) {
		return nil, p.NewFault("PROVIDER_CHANGED", "QUARK_DOWNLOAD")
	}
	cookie, e := c.Cookie()
	if e != nil || cookie == "" {
		return nil, p.NewFault("AUTH_REQUIRED", "QUARK_DOWNLOAD")
	}
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	req, e := http.NewRequestWithContext(requestCtx, http.MethodGet, u.String(), nil)
	if e != nil {
		cancel()
		return nil, p.NewFault("PROVIDER_CHANGED", "QUARK_DOWNLOAD")
	}
	req.Header.Set("Cookie", cookie)
	for _, value := range response.Cookies {
		req.AddCookie(value)
	}
	req.Header.Set("Referer", "https://pan.quark.cn/")
	req.Header.Set("User-Agent", nativeUserAgent)
	client := &http.Client{Timeout: 30 * time.Minute}
	if c.HTTP != nil {
		*client = *c.HTTP
		client.Timeout = 30 * time.Minute
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, e := client.Do(req)
	if e != nil {
		cancel()
		return nil, p.SafeFault(e, "QUARK_DOWNLOAD")
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return nil, p.NewFault("AUTH_REQUIRED", "QUARK_DOWNLOAD")
		}
		return nil, p.NewFault("NETWORK_TEMPORARY", "QUARK_DOWNLOAD")
	}
	expected, e := strconv.ParseInt(*want.SizeBytes, 10, 64)
	if e != nil || expected < 0 || resp.ContentLength >= 0 && resp.ContentLength != expected {
		resp.Body.Close()
		cancel()
		return nil, p.NewFault("VERSION_CHANGED", "QUARK_DOWNLOAD")
	}
	return &checkedBody{ReadCloser: resp.Body, cancel: cancel, remaining: expected, hash: checksum, expected: expectedHash}, nil
}
