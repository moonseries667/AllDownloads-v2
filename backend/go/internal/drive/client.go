// Package drive implements bounded Google Drive v3 I/O. Authentication remains
// private to the runner; directory browsing performs only GET requests.
package drive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
)

const folderMIME = "application/vnd.google-apps.folder"
const objectFields = "id,name,mimeType,parents,trashed,driveId,version,size,md5Checksum,sha256Checksum,appProperties,capabilities(canListChildren,canAddChildren)"

type Object struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	MimeType     string            `json:"mimeType"`
	Parents      []string          `json:"parents"`
	Trashed      bool              `json:"trashed"`
	DriveID      string            `json:"driveId"`
	Version      string            `json:"version"`
	Size         string            `json:"size"`
	MD5          string            `json:"md5Checksum"`
	SHA256       string            `json:"sha256Checksum"`
	Properties   map[string]string `json:"appProperties"`
	Capabilities struct {
		CanList bool `json:"canListChildren"`
		CanAdd  bool `json:"canAddChildren"`
	} `json:"capabilities"`
}

type Folder struct {
	ID                 string  `json:"id"`
	AccountID          string  `json:"accountId"`
	ParentID           *string `json:"parentId"`
	Name               string  `json:"name"`
	Kind               string  `json:"kind"`
	Revision           string  `json:"revision"`
	Selectable         bool    `json:"selectable"`
	Availability       string  `json:"availability"`
	AvailabilityReason *string `json:"availabilityReason"`
	Path               string  `json:"path"`
}
type Target struct {
	AccountID string `json:"accountId"`
	FolderID  string `json:"folderId"`
	Revision  string `json:"revision"`
	Name      string `json:"name"`
	Path      string `json:"path"`
}
type Page struct {
	AccountID   string   `json:"accountId"`
	ParentID    *string  `json:"parentId"`
	Items       []Folder `json:"items"`
	Parent      Folder   `json:"parent"`
	Breadcrumbs []Folder `json:"breadcrumbs"`
	NextCursor  *string  `json:"nextCursor"`
}
type Client struct {
	AccountID string
	RootID    string
	HTTP      *http.Client
	// Token is resolved by the process at request time, never serialized.
	Token func() string
	// BaseURL is a private loopback-only fixture seam. Empty uses Google.
	BaseURL string
}

func fault(code string) error  { return media.Fail(code, "DRIVE", false) }
func digest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func strptr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
func (c *Client) base() (string, error) {
	if c.BaseURL == "" {
		return "https://www.googleapis.com", nil
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.User != nil || u.Scheme != "http" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() || u.RawQuery != "" || (u.Path != "" && u.Path != "/") {
		return "", fault("INVALID_DRIVE_ENDPOINT")
	}
	return strings.TrimSuffix(c.BaseURL, "/"), nil
}
func (c *Client) request(ctx context.Context, method, address string, body io.Reader, headers http.Header) (*http.Response, error) {
	if c.Token == nil || c.AccountID == "" {
		return nil, fault("AUTH_REQUIRED")
	}
	token := c.Token()
	if token == "" {
		return nil, fault("AUTH_REQUIRED")
	}
	req, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return nil, fault("INVALID_DRIVE_REQUEST")
	}
	req.Header = headers.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if n := req.Header.Get("Content-Length"); n != "" {
		req.ContentLength, err = strconv.ParseInt(n, 10, 64)
		if err != nil {
			return nil, fault("INVALID_DRIVE_REQUEST")
		}
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := copyClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, media.Fail("NETWORK_ERROR", "DRIVE", true)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	// Never return raw provider error bodies (they can contain locators).
	switch resp.StatusCode {
	case 400:
		return nil, fault("INVALID_CURSOR")
	case 401:
		return nil, fault("AUTH_REQUIRED")
	case 403:
		return nil, fault("TARGET_PERMISSION_DENIED")
	case 404, 410:
		return nil, fault("TARGET_NOT_FOUND")
	case 409:
		return nil, fault("REMOTE_CONFLICT")
	case 429:
		return nil, media.Fail("RATE_LIMITED", "DRIVE", true)
	}
	return nil, media.Fail("DRIVE_REQUEST_FAILED", "DRIVE", resp.StatusCode >= 500)
}
func decodeResponse(resp *http.Response, out any) error {
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(raw) > 8<<20 || json.Unmarshal(raw, out) != nil {
		return fault("PROVIDER_CHANGED")
	}
	return nil
}
func (c *Client) get(ctx context.Context, id string) (Object, error) {
	if id == "" {
		return Object{}, fault("TARGET_NOT_FOUND")
	}
	base, err := c.base()
	if err != nil {
		return Object{}, err
	}
	resp, err := c.request(ctx, "GET", base+"/drive/v3/files/"+url.PathEscape(id)+"?"+url.Values{"fields": {objectFields}}.Encode(), nil, nil)
	if err != nil {
		return Object{}, err
	}
	var obj Object
	if err = decodeResponse(resp, &obj); err != nil {
		return obj, err
	}
	if obj.ID == "" || obj.Name == "" || (id != "root" && obj.ID != id) {
		return obj, fault("PROVIDER_CHANGED")
	}
	return obj, nil
}
func checkFolder(obj Object) error {
	if obj.Trashed {
		return fault("TARGET_NOT_FOUND")
	}
	if obj.MimeType != folderMIME {
		return fault("TARGET_NOT_FOLDER")
	}
	if obj.DriveID != "" {
		return fault("SHARED_DRIVE_UNSUPPORTED")
	}
	return nil
}
func (c *Client) root(ctx context.Context) (Object, error) {
	id := c.RootID
	if id == "" {
		id = "root"
	}
	obj, err := c.get(ctx, id)
	if err == nil {
		err = checkFolder(obj)
	}
	return obj, err
}

func (c *Client) folder(obj Object, parent *Folder) Folder {
	f := Folder{ID: obj.ID, AccountID: c.AccountID, Name: obj.Name, Kind: "FOLDER", Selectable: obj.Capabilities.CanAdd, Availability: "AVAILABLE"}
	if !f.Selectable {
		f.AvailabilityReason = strptr("TARGET_PERMISSION_DENIED")
	}
	if parent != nil {
		f.ParentID = &parent.ID
		f.Path = obj.Name
		if parent.Path != "" {
			f.Path = parent.Path + "/" + obj.Name
		}
	}
	// Child uploads can change a folder's provider version. Target revision
	// freezes its own identity, ancestry and permissions, not its contents.
	raw, _ := json.Marshal(struct {
		ID, Name, Drive string
		List, Add       bool
		Parent          *Folder
	}{obj.ID, obj.Name, obj.DriveID, obj.Capabilities.CanList, obj.Capabilities.CanAdd, parent})
	f.Revision = digest(raw)
	return f
}
func (c *Client) chain(ctx context.Context, id string) ([]Folder, Object, error) {
	root, err := c.root(ctx)
	if err != nil {
		return nil, Object{}, err
	}
	if id == "" || id == "root" {
		id = root.ID
	}
	objects := []Object{}
	seen := map[string]bool{}
	for len(objects) < 128 {
		if seen[id] {
			return nil, Object{}, fault("INVALID_PARENT_CHAIN")
		}
		seen[id] = true
		obj := root
		if id != root.ID {
			obj, err = c.get(ctx, id)
			if err != nil {
				return nil, Object{}, err
			}
		}
		if err = checkFolder(obj); err != nil {
			return nil, Object{}, err
		}
		objects = append(objects, obj)
		if obj.ID == root.ID {
			break
		}
		if len(obj.Parents) != 1 {
			return nil, Object{}, fault("TARGET_OUTSIDE_ALLOWED_ROOT")
		}
		id = obj.Parents[0]
	}
	if len(objects) == 0 || objects[len(objects)-1].ID != root.ID {
		return nil, Object{}, fault("INVALID_PARENT_CHAIN")
	}
	chain := make([]Folder, 0, len(objects))
	for i := len(objects) - 1; i >= 0; i-- {
		var parent *Folder
		if len(chain) > 0 {
			parent = &chain[len(chain)-1]
		}
		chain = append(chain, c.folder(objects[i], parent))
	}
	return chain, objects[0], nil
}
func (c *Client) List(ctx context.Context, parentID, cursor string, pageSize int) (Page, error) {
	page := Page{AccountID: c.AccountID, ParentID: strptr(parentID), Items: []Folder{}}
	if pageSize < 1 || pageSize > 200 {
		return page, fault("INVALID_INPUT")
	}
	chain, obj, err := c.chain(ctx, parentID)
	if err != nil {
		return page, err
	}
	if !obj.Capabilities.CanList {
		return page, fault("TARGET_PERMISSION_DENIED")
	}
	page.Breadcrumbs = chain
	page.Parent = chain[len(chain)-1]
	objects, next, err := c.listObjects(ctx, obj.ID, cursor, pageSize, "")
	if err != nil {
		return page, err
	}
	seen := map[string]bool{}
	for _, child := range objects {
		if checkFolder(child) != nil || len(child.Parents) != 1 || child.Parents[0] != obj.ID || child.ID == "" || child.Name == "" || seen[child.ID] {
			return page, fault("PROVIDER_CHANGED")
		}
		seen[child.ID] = true
		page.Items = append(page.Items, c.folder(child, &page.Parent))
	}
	page.NextCursor = strptr(next)
	return page, nil
}
func quote(value string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(value, "\\", "\\\\"), "'", "\\'") + "'"
}
func (c *Client) listObjects(ctx context.Context, parentID, cursor string, pageSize int, name string) ([]Object, string, error) {
	base, err := c.base()
	if err != nil {
		return nil, "", err
	}
	query := quote(parentID) + " in parents and trashed = false and mimeType = '" + folderMIME + "'"
	if name != "" {
		query += " and name = " + quote(name)
	}
	params := url.Values{"q": {query}, "spaces": {"drive"}, "fields": {"nextPageToken,incompleteSearch,files(" + objectFields + ")"}, "orderBy": {"name"}, "pageSize": {strconv.Itoa(pageSize)}}
	if cursor != "" {
		params.Set("pageToken", cursor)
	}
	resp, err := c.request(ctx, "GET", base+"/drive/v3/files?"+params.Encode(), nil, nil)
	if err != nil {
		return nil, "", err
	}
	var raw struct {
		Files      []Object `json:"files"`
		Next       string   `json:"nextPageToken"`
		Incomplete bool     `json:"incompleteSearch"`
	}
	if err = decodeResponse(resp, &raw); err != nil {
		return nil, "", err
	}
	if raw.Incomplete {
		return nil, "", fault("SNAPSHOT_INCOMPLETE")
	}
	if cursor != "" && cursor == raw.Next {
		return nil, "", fault("INVALID_CURSOR")
	}
	return raw.Files, raw.Next, nil
}
func (c *Client) Validate(ctx context.Context, id, revision string) (Target, error) {
	chain, _, err := c.chain(ctx, id)
	if err != nil {
		return Target{}, err
	}
	f := chain[len(chain)-1]
	if !f.Selectable {
		return Target{}, fault("TARGET_PERMISSION_DENIED")
	}
	if revision == "" || f.Revision != revision {
		return Target{}, fault("TARGET_CHANGED")
	}
	return Target{c.AccountID, f.ID, f.Revision, f.Name, f.Path}, nil
}

func isCode(err error, code string) bool {
	var f *media.Failure
	return errors.As(err, &f) && f.Code == code
}
