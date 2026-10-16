package quark

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	p "github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms"
)

const (
	nativeDefaultBaseURL    = "https://drive-pc.quark.cn"
	nativeDefaultAccountURL = "https://pan.quark.cn/account/info"
	nativeReferer           = "https://pan.quark.cn/"
	nativeUserAgent         = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) quark-cloud-drive/3.14.2 Chrome/112.0.5615.165 Electron/24.1.3.8 Safari/537.36 Channel/pckk_other_ch"
	nativePageSize          = 50
	nativeResponseLimit     = 8 << 20
)

// NativeClient reads Quark's personal drive using a cookie supplied by the
// private session store. BaseURL and AccountURL may be overridden only with a
// loopback HTTP literal IP, so tests cannot redirect credentials to a host.
type NativeClient struct {
	AccountID      string
	ExpectedUserID string
	Cookie         func() (string, error)
	HTTP           *http.Client
	BaseURL        string
	AccountURL     string
}

// NativeResponse keeps file/sort pagination metadata beside the untouched
// JSON data. Total is -1 when an endpoint omits metadata._total.
type NativeResponse struct {
	Data     json.RawMessage
	Cookies  []*http.Cookie
	Metadata struct {
		Total int `json:"_total"`
	}
}

// nativeEntry is the common personal-drive file/sort and file/info shape.
// Fields that Quark may omit stay optional so callers can safely fill a
// single-node response from its real parent listing.
type nativeEntry struct {
	FID       json.RawMessage `json:"fid"`
	PdirFID   json.RawMessage `json:"pdir_fid"`
	FileName  string          `json:"file_name"`
	Dir       *bool           `json:"dir"`
	File      *bool           `json:"file"`
	FileType  json.RawMessage `json:"file_type"`
	UpdatedAt json.RawMessage `json:"updated_at"`
	Size      json.RawMessage `json:"size"`
	SHA1      string          `json:"sha1"`
	MD5       string          `json:"md5"`
}

type nativeEnvelope struct {
	Status   json.RawMessage `json:"status"`
	Code     json.RawMessage `json:"code"`
	Data     json.RawMessage `json:"data"`
	Metadata json.RawMessage `json:"metadata"`
}

type nativePageCursor struct {
	Page  int
	Seen  int
	Total int
}

func (c *NativeClient) rpc(ctx context.Context, method, endpoint string, query url.Values, body any) (NativeResponse, error) {
	result := NativeResponse{}
	result.Metadata.Total = -1
	if c == nil || ctx == nil {
		return result, nativeFault("INVALID_INPUT", "LIST")
	}
	if err := ctx.Err(); err != nil {
		return result, p.SafeFault(err, "LIST")
	}
	method = strings.ToUpper(method)
	if method != http.MethodGet && method != http.MethodPost {
		return result, nativeFault("INVALID_INPUT", "LIST")
	}
	requestURL, err := c.requestURL(endpoint)
	if err != nil {
		return result, nativeFault("INVALID_INPUT", "LIST")
	}
	params := make(url.Values, len(query)+3)
	for key, values := range query {
		params[key] = append([]string(nil), values...)
	}
	params.Set("pr", "ucpro")
	params.Set("fr", "pc")
	params.Set("uc_param_str", "")
	requestURL.RawQuery = params.Encode()

	if c.Cookie == nil {
		return result, nativeFault("AUTH_REQUIRED", "LIST")
	}
	cookie, err := c.Cookie()
	if err != nil || strings.TrimSpace(cookie) == "" || strings.ContainsAny(cookie, "\r\n") {
		return result, nativeFault("AUTH_REQUIRED", "LIST")
	}

	var requestBody io.Reader
	if body != nil {
		encoded, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			return result, nativeFault("INVALID_INPUT", "LIST")
		}
		requestBody = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL.String(), requestBody)
	if err != nil {
		return result, nativeFault("INVALID_INPUT", "LIST")
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Referer", nativeReferer)
	req.Header.Set("User-Agent", nativeUserAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	if c.HTTP != nil {
		*client = *c.HTTP
		client.Timeout = 30 * time.Second
	}
	// A caller-supplied client may have a jar or redirect policy. Quark session
	// cookies must come only from Cookie, and redirects must never forward them.
	client.Jar = nil
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return result, p.SafeFault(err, "LIST")
	}
	defer resp.Body.Close()
	result.Cookies = resp.Cookies()
	if resp.StatusCode == http.StatusTooManyRequests {
		return result, nativeRateFault(resp.Header.Get("Retry-After"), "LIST")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, nativeHTTPFault(resp.StatusCode, "LIST")
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return result, nativeFault("PROVIDER_CHANGED", "LIST")
	}
	responseBytes, err := io.ReadAll(io.LimitReader(resp.Body, nativeResponseLimit+1))
	if err != nil {
		return result, p.SafeFault(err, "LIST")
	}
	if len(responseBytes) > nativeResponseLimit {
		return result, nativeFault("PROVIDER_CHANGED", "LIST")
	}
	var envelope nativeEnvelope
	if err := decodeOneJSON(responseBytes, &envelope); err != nil {
		return result, nativeFault("PROVIDER_CHANGED", "LIST")
	}
	status, ok := nativeInt(envelope.Status)
	if !ok {
		return result, nativeFault("PROVIDER_CHANGED", "LIST")
	}
	if status != http.StatusOK {
		return result, nativeStatusFault(int(status), resp.Header.Get("Retry-After"), "LIST")
	}
	code, ok := nativeInt(envelope.Code)
	if !ok {
		return result, nativeFault("PROVIDER_CHANGED", "LIST")
	}
	if code != 0 {
		return result, nativeStatusFault(int(code), resp.Header.Get("Retry-After"), "LIST")
	}
	if len(envelope.Data) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Data), []byte("null")) {
		return result, nativeFault("PROVIDER_CHANGED", "LIST")
	}
	result.Data = append(json.RawMessage(nil), envelope.Data...)
	if len(envelope.Metadata) != 0 && !bytes.Equal(bytes.TrimSpace(envelope.Metadata), []byte("null")) {
		var metadata struct {
			Total json.RawMessage `json:"_total"`
		}
		if err := decodeOneJSON(envelope.Metadata, &metadata); err != nil {
			return NativeResponse{}, nativeFault("PROVIDER_CHANGED", "LIST")
		}
		if len(metadata.Total) != 0 {
			total, ok := nativeInt(metadata.Total)
			if !ok || total < 0 || uint64(total) > uint64(maxInt()) {
				return NativeResponse{}, nativeFault("PROVIDER_CHANGED", "LIST")
			}
			result.Metadata.Total = int(total)
		}
	}
	return result, nil
}

func (c *NativeClient) Identity(ctx context.Context) (string, error) {
	if c == nil || c.AccountID == "" || c.ExpectedUserID == "" {
		return "", nativeFault("INVALID_INPUT", "IDENTITY")
	}
	return c.IdentifyForSetup(ctx)
}

// IdentifyForSetup performs only GET account/info. An unbound client may use it
// for first-time local setup; a bound client can never silently change owners.
// Business operations must continue to use Identity, which requires a binding.
func (c *NativeClient) IdentifyForSetup(ctx context.Context) (string, error) {
	if c == nil || c.AccountID == "" {
		return "", nativeFault("INVALID_INPUT", "IDENTITY")
	}
	accountURL, err := c.accountURL()
	if err != nil {
		return "", nativeFault("INVALID_INPUT", "IDENTITY")
	}
	query := url.Values{"platform": {"pc"}}
	response, err := c.rpc(ctx, http.MethodGet, accountURL.String(), query, nil)
	if err != nil {
		return "", nativeRestage(err, "IDENTITY")
	}
	var account struct {
		UID json.RawMessage `json:"uid"`
	}
	if err := decodeOneJSON(response.Data, &account); err != nil {
		return "", nativeFault("PROVIDER_CHANGED", "IDENTITY")
	}
	uid, ok := nativeText(account.UID)
	if !ok || uid == "" {
		return "", nativeFault("AUTH_REQUIRED", "IDENTITY")
	}
	if c.ExpectedUserID != "" && uid != c.ExpectedUserID {
		return "", nativeFault("AUTH_REQUIRED", "IDENTITY")
	}
	return uid, nil
}

func (c *NativeClient) Root(ctx context.Context, account string) (Node, error) {
	if err := c.checkAccount(account); err != nil {
		return Node{}, err
	}
	identity, err := c.Identity(ctx)
	if err != nil {
		return Node{}, err
	}
	node := Node{ID: "0", Name: "我的云盘", Kind: "FOLDER", Availability: "AVAILABLE", Writable: true}
	node.Revision = nativeRevision(identity, node, "", "", "")
	return node, nil
}

func (c *NativeClient) Node(ctx context.Context, account, id string) (Node, error) {
	if err := c.checkAccount(account); err != nil {
		return Node{}, err
	}
	if id == "" {
		return Node{}, nativeFault("INVALID_INPUT", "LIST")
	}
	identity, err := c.Identity(ctx)
	if err != nil {
		return Node{}, err
	}
	if id == "0" {
		root := Node{ID: "0", Name: "我的云盘", Kind: "FOLDER", Availability: "AVAILABLE", Writable: true}
		root.Revision = nativeRevision(identity, root, "", "", "")
		return root, nil
	}
	query := url.Values{"fid": {id}, "_fetch_full_path": {"0"}}
	response, err := c.rpc(ctx, http.MethodGet, "/1/clouddrive/file/info", query, nil)
	if err != nil {
		return Node{}, err
	}
	var entry nativeEntry
	if err := decodeOneJSON(response.Data, &entry); err != nil {
		return Node{}, nativeFault("PROVIDER_CHANGED", "LIST")
	}
	entryID, ok := nativeText(entry.FID)
	if !ok {
		// file/info ties its data to the requested fid, even on response shapes
		// that omit a redundant fid member.
		entryID = id
		entry.FID = mustJSONText(id)
	}
	if entryID != id {
		return Node{}, nativeFault("PROVIDER_CHANGED", "LIST")
	}
	parentID, ok := nativeText(entry.PdirFID)
	if !ok || parentID == "" || entry.FileName == "" {
		return Node{}, nativeFault("PROVIDER_CHANGED", "LIST")
	}
	if nativeEntryHasKind(entry) {
		node, err := mapNativeEntry(identity, entry)
		if err != nil {
			return Node{}, err
		}
		if node.ID != id || node.ParentID == nil || *node.ParentID != parentID {
			return Node{}, nativeFault("PROVIDER_CHANGED", "LIST")
		}
		return node, nil
	}
	// file/info responses can omit type and version columns. Resolve those from
	// the node's actual parent listing instead of inferring a kind or parent.
	var cursor *string
	for {
		page, err := c.childrenPage(ctx, identity, parentID, cursor)
		if err != nil {
			return Node{}, err
		}
		for _, child := range page.Nodes {
			if child.ID != id {
				continue
			}
			if child.ParentID == nil || *child.ParentID != parentID || child.Name != entry.FileName {
				return Node{}, nativeFault("VERSION_CHANGED", "LIST")
			}
			return child, nil
		}
		if page.Complete {
			return Node{}, nativeFault("RESOURCE_DELETED", "LIST")
		}
		cursor = page.Next
	}
}

func (c *NativeClient) Children(ctx context.Context, account, parent string, cursor *string) (Page, error) {
	if err := c.checkAccount(account); err != nil {
		return Page{}, err
	}
	if parent == "" {
		return Page{}, nativeFault("INVALID_INPUT", "LIST")
	}
	identity, err := c.Identity(ctx)
	if err != nil {
		return Page{}, err
	}
	return c.childrenPage(ctx, identity, parent, cursor)
}

func (c *NativeClient) childrenPage(ctx context.Context, identity, parent string, cursor *string) (Page, error) {
	page := 1
	seen := 0
	expectedTotal := -1
	if cursor != nil {
		parsed, ok := parseNativeCursor(*cursor)
		if !ok {
			return Page{}, nativeFault("INVALID_INPUT", "LIST")
		}
		page, seen, expectedTotal = parsed.Page, parsed.Seen, parsed.Total
	}
	query := url.Values{
		"pdir_fid":        {parent},
		"_page":           {strconv.Itoa(page)},
		"_size":           {strconv.Itoa(nativePageSize)},
		"_fetch_total":    {"1"},
		"_fetch_sub_dirs": {"0"},
		"_sort":           {"file_type:asc,updated_at:desc"},
	}
	response, err := c.rpc(ctx, http.MethodGet, "/1/clouddrive/file/sort", query, nil)
	if err != nil {
		return Page{}, err
	}
	if response.Metadata.Total < 0 || (expectedTotal >= 0 && response.Metadata.Total != expectedTotal) {
		return Page{}, nativeFault("PROVIDER_CHANGED", "LIST")
	}
	var data struct {
		List []json.RawMessage `json:"list"`
	}
	if err := decodeOneJSON(response.Data, &data); err != nil || data.List == nil || len(data.List) > nativePageSize {
		return Page{}, nativeFault("PROVIDER_CHANGED", "LIST")
	}
	if seen > response.Metadata.Total || len(data.List) > response.Metadata.Total-seen || (len(data.List) == 0 && seen < response.Metadata.Total) {
		return Page{}, nativeFault("PROVIDER_CHANGED", "LIST")
	}
	nodes := make([]Node, 0, len(data.List))
	for _, raw := range data.List {
		node, err := mapNativeRaw(identity, raw)
		if err != nil {
			return Page{}, err
		}
		if node.ParentID == nil || *node.ParentID != parent {
			return Page{}, nativeFault("PROVIDER_CHANGED", "LIST")
		}
		nodes = append(nodes, node)
	}
	seen += len(data.List)
	if seen == response.Metadata.Total {
		return Page{Nodes: nodes, Complete: true}, nil
	}
	if page == maxInt() {
		return Page{}, nativeFault("PROVIDER_CHANGED", "LIST")
	}
	next := formatNativeCursor(nativePageCursor{Page: page + 1, Seen: seen, Total: response.Metadata.Total})
	return Page{Nodes: nodes, Next: &next, Complete: false}, nil
}

func (c *NativeClient) checkAccount(account string) error {
	if c == nil || c.AccountID == "" || account == "" || account != c.AccountID {
		return nativeFault("INVALID_INPUT", "LIST")
	}
	return nil
}

func (c *NativeClient) requestURL(endpoint string) (*url.URL, error) {
	base, err := c.baseURL()
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(endpoint, "/") {
		return url.Parse(strings.TrimRight(base.String(), "/") + endpoint)
	}
	account, err := c.accountURL()
	if err != nil || endpoint != account.String() {
		return nil, errors.New("invalid endpoint")
	}
	return account, nil
}

func (c *NativeClient) baseURL() (*url.URL, error) {
	raw := c.BaseURL
	if raw == "" {
		raw = nativeDefaultBaseURL
	}
	raw = strings.TrimRight(raw, "/")
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid base url")
	}
	if raw != nativeDefaultBaseURL && !isLoopbackHTTP(u) {
		return nil, errors.New("non-loopback test url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("invalid scheme")
	}
	return u, nil
}

func (c *NativeClient) accountURL() (*url.URL, error) {
	raw := c.AccountURL
	if raw == "" {
		raw = nativeDefaultAccountURL
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid account url")
	}
	if raw != nativeDefaultAccountURL && !isLoopbackHTTP(u) {
		return nil, errors.New("non-loopback test url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("invalid scheme")
	}
	return u, nil
}

func isLoopbackHTTP(u *url.URL) bool {
	if u == nil || u.Scheme != "http" || u.Host == "" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}

func parseNativeNode(raw json.RawMessage) (Node, error) {
	return mapNativeRaw("", raw)
}

func mapNativeRaw(identity string, raw json.RawMessage) (Node, error) {
	var entry nativeEntry
	if err := decodeOneJSON(raw, &entry); err != nil {
		return Node{}, nativeFault("PROVIDER_CHANGED", "LIST")
	}
	return mapNativeEntry(identity, entry)
}

func mapNativeEntry(identity string, entry nativeEntry) (Node, error) {
	id, ok := nativeText(entry.FID)
	if !ok || id == "" {
		return Node{}, nativeFault("PROVIDER_CHANGED", "LIST")
	}
	parentID, ok := nativeText(entry.PdirFID)
	if !ok || parentID == "" || entry.FileName == "" {
		return Node{}, nativeFault("PROVIDER_CHANGED", "LIST")
	}
	kind, ok := nativeKind(entry)
	if !ok {
		return Node{}, nativeFault("PROVIDER_CHANGED", "LIST")
	}
	modifiedAt, updatedRaw, ok := nativeModifiedAt(entry.UpdatedAt)
	if !ok {
		return Node{}, nativeFault("PROVIDER_CHANGED", "LIST")
	}
	size, ok := nativeSize(entry.Size)
	if !ok {
		return Node{}, nativeFault("PROVIDER_CHANGED", "LIST")
	}
	parent := parentID
	node := Node{ID: id, ParentID: &parent, Name: entry.FileName, Kind: kind, Availability: "AVAILABLE", Writable: true, SizeBytes: size, ModifiedAt: modifiedAt}
	node.Revision = nativeRevision(identity, node, entry.SHA1, entry.MD5, updatedRaw)
	if !validNode(node) {
		return Node{}, nativeFault("PROVIDER_CHANGED", "LIST")
	}
	return node, nil
}

func nativeEntryHasKind(entry nativeEntry) bool {
	if entry.Dir != nil || entry.File != nil {
		return true
	}
	return len(entry.FileType) != 0 && !bytes.Equal(bytes.TrimSpace(entry.FileType), []byte("null"))
}

func nativeKind(entry nativeEntry) (string, bool) {
	if entry.Dir != nil || entry.File != nil {
		if entry.Dir != nil && entry.File != nil && *entry.Dir == *entry.File {
			return "", false
		}
		if entry.Dir != nil {
			if *entry.Dir {
				return "FOLDER", true
			}
			return "FILE", true
		}
		if *entry.File {
			return "FILE", true
		}
		return "FOLDER", true
	}
	fileType, ok := nativeInt(entry.FileType)
	if !ok {
		return "", false
	}
	switch fileType {
	case 0:
		return "FOLDER", true
	case 1:
		return "FILE", true
	default:
		return "", false
	}
}

func nativeRevision(identity string, node Node, sha1Value, md5Value, updated string) string {
	parent := "<root>"
	if node.ParentID != nil {
		parent = *node.ParentID
	}
	parts := []string{identity, node.ID, parent, node.Name, node.Kind, strconv.FormatBool(node.Writable), node.Availability}
	if node.Kind == "FILE" {
		sizeKnown, sizeValue := "false", ""
		if node.SizeBytes != nil {
			sizeKnown, sizeValue = "true", *node.SizeBytes
		}
		parts = append(parts, strings.ToLower(sha1Value), strings.ToLower(md5Value), updated, sizeKnown, sizeValue)
	}
	serialized, _ := json.Marshal(parts)
	digest := sha256.Sum256(serialized)
	return hex.EncodeToString(digest[:])
}

func nativeCursorString(cursor nativePageCursor) string {
	return fmt.Sprintf("%d:%d:%d", cursor.Page, cursor.Seen, cursor.Total)
}

func formatNativeCursor(cursor nativePageCursor) string { return nativeCursorString(cursor) }

func parseNativeCursor(raw string) (nativePageCursor, bool) {
	var cursor nativePageCursor
	parts := strings.Split(raw, ":")
	if len(parts) != 3 {
		return cursor, false
	}
	values := []*int{&cursor.Page, &cursor.Seen, &cursor.Total}
	for i, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return nativePageCursor{}, false
		}
		*values[i] = value
	}
	if cursor.Page < 2 || cursor.Total < 0 || cursor.Seen <= 0 || cursor.Seen > cursor.Total {
		return nativePageCursor{}, false
	}
	return cursor, true
}

func nativeText(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value, true
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", false
	}
	for _, b := range trimmed {
		if b < '0' || b > '9' {
			return "", false
		}
	}
	return string(trimmed), true
}

func nativeOptionalText(raw json.RawMessage) (*string, string, bool) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, "", true
	}
	value, ok := nativeText(raw)
	if !ok {
		return nil, "", false
	}
	return &value, value, true
}

// Quark's updated_at in file metadata is Unix milliseconds. OpenList's Quark
// driver applies time.UnixMilli to this field; keep that protocol explicit so
// small valid millisecond values are never guessed to be seconds. A textual
// RFC3339 value is also accepted and normalized for display, while the exact
// JSON value remains part of a file revision.
func nativeModifiedAt(raw json.RawMessage) (*string, string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, string(trimmed), true
	}

	value := string(trimmed)
	quoted := trimmed[0] == '"'
	if quoted {
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return nil, string(trimmed), false
		}
	}
	instant, err := strconv.ParseInt(value, 10, 64)
	var timestamp time.Time
	if err == nil {
		timestamp = time.UnixMilli(instant).UTC()
	} else if quoted {
		timestamp, err = time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return nil, string(trimmed), false
		}
		timestamp = timestamp.UTC()
	} else {
		return nil, string(trimmed), false
	}
	if timestamp.Year() < 0 || timestamp.Year() > 9999 {
		return nil, string(trimmed), false
	}
	formatted := timestamp.Format(time.RFC3339Nano)
	return &formatted, string(trimmed), true
}

func nativeSize(raw json.RawMessage) (*string, bool) {
	value, _, ok := nativeOptionalText(raw)
	if !ok {
		return nil, false
	}
	if value == nil {
		return nil, true
	}
	parsed, err := strconv.ParseUint(*value, 10, 64)
	if err != nil {
		return nil, false
	}
	canonical := strconv.FormatUint(parsed, 10)
	return &canonical, true
}

func nativeInt(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, false
	}
	value := strings.TrimSpace(string(raw))
	if strings.HasPrefix(value, "\"") {
		if json.Unmarshal(raw, &value) != nil {
			return 0, false
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	return parsed, err == nil
}

func decodeOneJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple json values")
		}
		return err
	}
	return nil
}

func nativeStatusFault(status int, retryAfter, stage string) *p.Fault {
	switch status {
	case http.StatusUnauthorized:
		return nativeFault("AUTH_REQUIRED", stage)
	case http.StatusForbidden:
		return nativeFault("PERMISSION_DENIED", stage)
	case http.StatusNotFound:
		return nativeFault("RESOURCE_DELETED", stage)
	case http.StatusTooManyRequests:
		return nativeRateFault(retryAfter, stage)
	case http.StatusRequestTimeout, http.StatusTooEarly:
		return nativeFault("NETWORK_TEMPORARY", stage)
	default:
		if status >= 500 {
			return nativeFault("NETWORK_TEMPORARY", stage)
		}
		return nativeFault("PROVIDER_CHANGED", stage)
	}
}

func nativeHTTPFault(status int, stage string) *p.Fault {
	return nativeStatusFault(status, "", stage)
}

func nativeRateFault(header, stage string) *p.Fault {
	fault := nativeFault("RATE_LIMITED", stage)
	if seconds, ok := retryAfterSeconds(header, time.Now()); ok {
		fault.RetryAfterSeconds = &seconds
	}
	return fault
}

func retryAfterSeconds(value string, now time.Time) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseInt(value, 10, 32); err == nil && seconds >= 0 {
		return int(seconds), true
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	duration := when.Sub(now)
	if duration < 0 {
		return 0, true
	}
	seconds := int(math.Ceil(duration.Seconds()))
	return seconds, true
}

func nativeFault(code, stage string) *p.Fault {
	return p.NewFault(code, stage)
}

func nativeRestage(err error, stage string) error {
	var fault *p.Fault
	if errors.As(err, &fault) {
		copy := p.NewFault(fault.Code, stage)
		copy.RetryAfterSeconds = fault.RetryAfterSeconds
		return copy
	}
	return p.SafeFault(err, stage)
}

func mustJSONText(value string) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

func maxInt() int {
	return int(^uint(0) >> 1)
}
