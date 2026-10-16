package quark

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
	p "github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms"
)

type TransferRequest struct {
	SchemaVersion        int    `json:"schemaVersion"`
	ShareID              string `json:"shareId"`
	URL                  string `json:"url"`
	TargetFolderID       string `json:"targetFolderId"`
	TargetFolderRevision string `json:"targetFolderRevision"`
	OriginalOperationID  string `json:"originalOperationId,omitempty"`
}
type TransferResult struct {
	ShareID              string      `json:"shareId"`
	TargetFolderID       string      `json:"targetFolderId"`
	TargetFolderRevision string      `json:"targetFolderRevision"`
	Nodes                []CloudNode `json:"nodes"`
	Complete             bool        `json:"complete"`
	SafeToResume         bool        `json:"safeToResume,omitempty"`
}

// Transfers owns only private platform identities, remote intents and receipts.
// Java remains the sole owner of scheduling, watchers and download orders.
type Transfers struct {
	State   *executor.State
	Clients map[string]*NativeClient
	mu      sync.Mutex
}
type shareEntry struct {
	ID       string       `json:"fid"`
	Token    string       `json:"share_fid_token"`
	Parent   string       `json:"pdir_fid"`
	Name     string       `json:"file_name"`
	Dir      bool         `json:"dir"`
	File     bool         `json:"file"`
	Size     json.Number  `json:"size"`
	Updated  json.Number  `json:"updated_at"`
	SHA1     string       `json:"sha1"`
	MD5      string       `json:"md5"`
	Children []shareEntry `json:"children,omitempty"`
}
type copiedEntry struct {
	Source            shareEntry
	RemoteID          string
	DirectoryRevision string
}
type managedShare struct {
	Account, UserID, TargetID, TargetRevision string
	Entries                                   map[string]copiedEntry
}
type transferStep struct {
	Source    shareEntry
	ParentID  string
	Before    []string
	Submitted bool
	TaskID    string
	Copies    []copiedEntry
}
type transferIntent struct {
	Identity, Account, UserID string
	Request                   TransferRequest
	Sources                   []shareEntry
	Steps                     map[string]*transferStep
}

func qhash(v any) string {
	raw, _ := json.Marshal(v)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func entryKey(s shareEntry) string {
	if s.Dir {
		return qhash([]any{s.ID, s.Parent, s.Name, "FOLDER"})
	}
	return qhash([]any{s.ID, s.Parent, s.Name, s.Size, s.Updated, strings.ToLower(s.SHA1), strings.ToLower(s.MD5)})
}
func (t *Transfers) client(account string) (*NativeClient, error) {
	c := t.Clients[account]
	if c == nil || c.AccountID != account || t.State == nil {
		return nil, p.NewFault("AUTH_REQUIRED", "QUARK")
	}
	if c.Cookie == nil {
		return nil, p.NewFault("AUTH_REQUIRED", "QUARK")
	}
	cookie, err := c.Cookie()
	if err != nil || strings.TrimSpace(cookie) == "" {
		return nil, p.NewFault("AUTH_REQUIRED", "QUARK")
	}
	// Pin one session for the whole operation. Re-reading a rotated cookie
	// between UID validation and save could otherwise redirect a root target.
	bound := *c
	bound.Cookie = func() (string, error) { return cookie, nil }
	return &bound, nil
}
func (t *Transfers) Root(ctx context.Context, account string) (Node, error) {
	c, e := t.client(account)
	if e != nil {
		return Node{}, e
	}
	return c.Root(ctx, account)
}
func (t *Transfers) Node(ctx context.Context, account, id string) (Node, error) {
	c, e := t.client(account)
	if e != nil {
		return Node{}, e
	}
	return c.Node(ctx, account, id)
}
func (t *Transfers) Children(ctx context.Context, account, id string, cursor *string) (Page, error) {
	c, e := t.client(account)
	if e != nil {
		return Page{}, e
	}
	return c.Children(ctx, account, id, cursor)
}
func (t *Transfers) loadShare(account, share string) (managedShare, error) {
	var m managedShare
	err := t.State.Read("quark-share-"+qhash([]string{account, share}), &m)
	if err != nil {
		return m, p.NewFault("RESULT_UNKNOWN", "QUARK_TREE")
	}
	if m.Account != account || m.Entries == nil {
		return m, p.NewFault("VERSION_CHANGED", "QUARK_TREE")
	}
	return m, nil
}
func (t *Transfers) ShareRoot(ctx context.Context, account, share string) (Node, error) {
	m, e := t.loadShare(account, share)
	if e != nil {
		return Node{}, e
	}
	n, e := t.Node(ctx, account, m.TargetID)
	if e == nil && n.Revision != m.TargetRevision {
		return Node{}, p.NewFault("VERSION_CHANGED", "QUARK_TREE")
	}
	return n, e
}

type managedClient struct {
	*Transfers
	account, share string
	allowed        map[string]bool
	root           string
}

func (m *managedClient) Children(ctx context.Context, account, id string, cursor *string) (Page, error) {
	if account != m.account || id != m.root && !m.allowed[id] {
		return Page{}, p.NewFault("PERMISSION_DENIED", "QUARK_TREE")
	}
	page, e := m.Transfers.Children(ctx, account, id, cursor)
	if e != nil {
		return page, e
	}
	nodes := []Node{}
	for _, n := range page.Nodes {
		if m.allowed[n.ID] {
			nodes = append(nodes, n)
		}
	}
	page.Nodes = nodes
	return page, nil
}

// ForShare prevents unrelated files in a user-selected target from acquiring a
// fabricated share identity. Only proved transfer mappings belong to this tree.
func (t *Transfers) ForShare(account, share string) (Client, error) {
	m, e := t.loadShare(account, share)
	if e != nil {
		return nil, e
	}
	allowed := map[string]bool{}
	for _, v := range m.Entries {
		allowed[v.RemoteID] = true
	}
	return &managedClient{t, account, share, allowed, m.TargetID}, nil
}
func allChildren(ctx context.Context, c *NativeClient, account, parent string) ([]Node, error) {
	nodes := []Node{}
	var cursor *string
	seen := map[string]bool{}
	for {
		page, e := c.Children(ctx, account, parent, cursor)
		if e != nil {
			return nil, e
		}
		nodes = append(nodes, page.Nodes...)
		if page.Complete {
			return nodes, nil
		}
		if page.Next == nil || seen[*page.Next] {
			return nil, p.NewFault("PROVIDER_CHANGED", "QUARK")
		}
		seen[*page.Next] = true
		cursor = page.Next
	}
}
func decodeEntries(raw json.RawMessage) ([]shareEntry, error) {
	var data struct {
		List []json.RawMessage `json:"list"`
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	if d.Decode(&data) != nil || data.List == nil {
		return nil, p.NewFault("PROVIDER_CHANGED", "QUARK_SHARE")
	}
	entries := make([]shareEntry, 0, len(data.List))
	for _, rawEntry := range data.List {
		var s shareEntry
		var common nativeEntry
		d := json.NewDecoder(strings.NewReader(string(rawEntry)))
		d.UseNumber()
		if d.Decode(&s) != nil || decodeOneJSON(rawEntry, &common) != nil {
			return nil, p.NewFault("PROVIDER_CHANGED", "QUARK_SHARE")
		}
		kind, ok := nativeKind(common)
		if !ok {
			return nil, p.NewFault("PROVIDER_CHANGED", "QUARK_SHARE")
		}
		s.Dir, s.File = kind == "FOLDER", kind == "FILE"
		if s.ID == "" || s.Name == "" || strings.ContainsAny(s.Name, "/\\\x00") || s.Name == "." || s.Name == ".." {
			return nil, p.NewFault("PROVIDER_CHANGED", "QUARK_SHARE")
		}
		entries = append(entries, s)
	}
	return entries, nil
}
func (c *NativeClient) shareTree(ctx context.Context, pwd, token, parent string, seen map[string]bool) ([]shareEntry, error) {
	out := []shareEntry{}
	for page := 1; page <= 100000; page++ {
		response, e := c.rpc(ctx, http.MethodGet, "/1/clouddrive/share/sharepage/detail", url.Values{"pwd_id": {pwd}, "stoken": {token}, "pdir_fid": {parent}, "_page": {strconv.Itoa(page)}, "_size": {"50"}, "_fetch_total": {"1"}, "_sort": {"file_type:asc,updated_at:desc"}, "_fetch_share": {"1"}, "ver": {"2"}}, nil)
		if e != nil {
			return nil, e
		}
		entries, e := decodeEntries(response.Data)
		if e != nil {
			return nil, e
		}
		if response.Metadata.Total < 0 {
			return nil, p.NewFault("PROVIDER_CHANGED", "QUARK_SHARE")
		}
		if len(entries) == 0 && len(out) < response.Metadata.Total {
			return nil, p.NewFault("PROVIDER_CHANGED", "QUARK_SHARE")
		}
		for i := range entries {
			s := &entries[i]
			if seen[s.ID] || s.Token == "" {
				return nil, p.NewFault("PROVIDER_CHANGED", "QUARK_SHARE")
			}
			seen[s.ID] = true
			if s.Parent != "" && s.Parent != parent {
				return nil, p.NewFault("PROVIDER_CHANGED", "QUARK_SHARE")
			}
			s.Parent = parent
			if s.Dir {
				s.Children, e = c.shareTree(ctx, pwd, token, s.ID, seen)
				if e != nil {
					return nil, e
				}
			}
		}
		out = append(out, entries...)
		if len(out) >= response.Metadata.Total {
			sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
			return out, nil
		}
	}
	return nil, p.NewFault("PROVIDER_CHANGED", "QUARK_SHARE")
}
func parseShare(address string) (string, string, error) {
	u, e := url.Parse(address)
	if e != nil || u.Scheme != "https" || u.Host != "pan.quark.cn" || u.User != nil {
		return "", "", p.NewFault("INVALID_INPUT", "QUARK_SHARE")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "s" || parts[1] == "" {
		return "", "", p.NewFault("INVALID_INPUT", "QUARK_SHARE")
	}
	for _, r := range parts[1] {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return "", "", p.NewFault("INVALID_INPUT", "QUARK_SHARE")
		}
	}
	code := u.Query().Get("pwd")
	if code == "" {
		code = u.Query().Get("passcode")
	}
	return parts[1], code, nil
}
func (c *NativeClient) shareToken(ctx context.Context, pwd, code string) (string, error) {
	r, e := c.rpc(ctx, http.MethodPost, "/1/clouddrive/share/sharepage/token", nil, map[string]string{"pwd_id": pwd, "passcode": code})
	if e != nil {
		return "", e
	}
	var d struct {
		Token string `json:"stoken"`
	}
	if json.Unmarshal(r.Data, &d) != nil || d.Token == "" {
		return "", p.NewFault("PROVIDER_CHANGED", "QUARK_SHARE")
	}
	return d.Token, nil
}
func unknown() error {
	f := p.NewFault("RESULT_UNKNOWN", "TRANSFER")
	f.PendingEffects = []string{"quark transfer requires read-only reconciliation"}
	return f
}

var safeRemainder = errors.New("all submitted effects confirmed; remaining steps were never submitted")

func (c *NativeClient) remoteEntry(ctx context.Context, id string) (shareEntry, error) {
	r, e := c.rpc(ctx, http.MethodGet, "/1/clouddrive/file/info", url.Values{"fid": {id}, "_fetch_full_path": {"0"}}, nil)
	if e != nil {
		return shareEntry{}, e
	}
	var s shareEntry
	d := json.NewDecoder(strings.NewReader(string(r.Data)))
	d.UseNumber()
	if d.Decode(&s) != nil || s.ID != "" && s.ID != id {
		return s, p.NewFault("PROVIDER_CHANGED", "TRANSFER_VERIFY")
	}
	s.ID = id
	if s.Parent == "" {
		return s, p.NewFault("PROVIDER_CHANGED", "TRANSFER_VERIFY")
	}
	if s.Dir == s.File || s.File && (s.Size == "" || s.SHA1 == "" && s.MD5 == "") {
		for page := 1; page <= 100000; page++ {
			r, e := c.rpc(ctx, http.MethodGet, "/1/clouddrive/file/sort", url.Values{"pdir_fid": {s.Parent}, "_page": {strconv.Itoa(page)}, "_size": {"50"}, "_fetch_total": {"1"}, "_sort": {"file_type:asc,updated_at:desc"}}, nil)
			if e != nil {
				return s, e
			}
			entries, e := decodeEntries(r.Data)
			if e != nil {
				return s, e
			}
			for _, entry := range entries {
				if entry.ID == id {
					if entry.Name != s.Name || entry.Parent != s.Parent {
						return s, p.NewFault("VERSION_CHANGED", "TRANSFER_VERIFY")
					}
					return entry, nil
				}
			}
			if page*50 >= r.Metadata.Total || len(entries) == 0 {
				break
			}
		}
		return s, p.NewFault("PROVIDER_CHANGED", "TRANSFER_VERIFY")
	}
	return s, nil
}
func sameContent(a, b shareEntry) bool {
	if a.Dir != b.Dir || a.File != b.File {
		return false
	}
	if a.Dir {
		return true
	}
	if a.Size == "" || a.Size != b.Size {
		return false
	}
	if a.SHA1 != "" && b.SHA1 != "" {
		return strings.EqualFold(a.SHA1, b.SHA1)
	}
	return a.MD5 != "" && b.MD5 != "" && strings.EqualFold(a.MD5, b.MD5)
}

// certifyCopy uses a task-returned stable ID (or a unique new identity after a
// lost response), then verifies the entire immutable source subtree. Name and
// size alone never prove a copied file's content.
func (c *NativeClient) certifyCopy(ctx context.Context, source shareEntry, id, parent string, nameRequired bool) ([]copiedEntry, error) {
	actual, e := c.remoteEntry(ctx, id)
	if e != nil {
		return nil, e
	}
	if actual.Parent != parent || nameRequired && actual.Name != source.Name || !sameContent(source, actual) {
		return nil, unknown()
	}
	result := []copiedEntry{{Source: source, RemoteID: id}}
	if source.Dir {
		directory, e := c.Node(ctx, c.AccountID, id)
		if e != nil {
			return nil, e
		}
		result[0].DirectoryRevision = directory.Revision
		children, e := allChildren(ctx, c, c.AccountID, id)
		if e != nil {
			return nil, e
		}
		if len(children) != len(source.Children) {
			return nil, unknown()
		}
		used := map[string]bool{}
		for _, s := range source.Children {
			matches := []Node{}
			for _, n := range children {
				if n.Name == s.Name && !used[n.ID] {
					matches = append(matches, n)
				}
			}
			if len(matches) != 1 {
				return nil, unknown()
			}
			used[matches[0].ID] = true
			proof, e := c.certifyCopy(ctx, s, matches[0].ID, id, true)
			if e != nil {
				return nil, e
			}
			result = append(result, proof...)
		}
	}
	return result, nil
}

// A multi-step transfer rechecks the selected target and every managed parent
// immediately before each new write. Folder revisions exclude child changes,
// so our own earlier copies cannot invalidate this identity check.
func (t *Transfers) validateWriteParent(ctx context.Context, account string, target Target, registry managedShare, parent string) error {
	if _, err := ValidateTarget(ctx, t, account, target); err != nil {
		return err
	}
	c, err := t.client(account)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for depth := 0; depth < 256; depth++ {
		if parent == target.FolderID {
			return nil
		}
		if seen[parent] {
			return p.NewFault("VERSION_CHANGED", "TRANSFER_TARGET")
		}
		seen[parent] = true
		node, err := c.Node(ctx, account, parent)
		if err != nil {
			return err
		}
		if node.Kind != "FOLDER" || node.ParentID == nil || !node.Writable || node.Availability != "AVAILABLE" {
			return p.NewFault("VERSION_CHANGED", "TRANSFER_TARGET")
		}
		matched := false
		for _, copy := range registry.Entries {
			if copy.RemoteID != parent || !copy.Source.Dir {
				continue
			}
			if copy.DirectoryRevision == "" || copy.DirectoryRevision != node.Revision {
				return p.NewFault("VERSION_CHANGED", "TRANSFER_TARGET")
			}
			matched = true
		}
		if !matched {
			return p.NewFault("VERSION_CHANGED", "TRANSFER_TARGET")
		}
		parent = *node.ParentID
	}
	return p.NewFault("VERSION_CHANGED", "TRANSFER_TARGET")
}
func (t *Transfers) reconcileStep(ctx context.Context, c *NativeClient, step *transferStep) ([]copiedEntry, error) {
	if len(step.Copies) > 0 {
		return c.certifyCopy(ctx, step.Source, step.Copies[0].RemoteID, step.ParentID, false)
	}
	if step.TaskID != "" {
		r, e := c.rpc(ctx, http.MethodGet, "/1/clouddrive/task", url.Values{"task_id": {step.TaskID}, "retry_index": {"0"}}, nil)
		if e != nil {
			return nil, unknown()
		}
		var task struct {
			Status int `json:"status"`
			Save   struct {
				IDs []string `json:"save_as_top_fids"`
			} `json:"save_as"`
		}
		if json.Unmarshal(r.Data, &task) != nil || task.Status != 2 || len(task.Save.IDs) != 1 {
			return nil, unknown()
		}
		return c.certifyCopy(ctx, step.Source, task.Save.IDs[0], step.ParentID, false)
	}
	if !step.Submitted {
		return nil, unknown()
	}
	children, e := allChildren(ctx, c, c.AccountID, step.ParentID)
	if e != nil {
		return nil, unknown()
	}
	before := map[string]bool{}
	for _, id := range step.Before {
		before[id] = true
	}
	var found []copiedEntry
	for _, n := range children {
		if before[n.ID] || n.Name != step.Source.Name {
			continue
		}
		copies, e := c.certifyCopy(ctx, step.Source, n.ID, step.ParentID, true)
		if e == nil {
			if found != nil {
				return nil, unknown()
			}
			found = copies
		}
	}
	if found == nil {
		return nil, unknown()
	}
	return found, nil
}
func (t *Transfers) Transfer(ctx context.Context, operationID, account string, req TransferRequest, readOnly bool) (TransferResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := TransferResult{ShareID: req.ShareID, TargetFolderID: req.TargetFolderID, TargetFolderRevision: req.TargetFolderRevision, Nodes: []CloudNode{}}
	c, e := t.client(account)
	if e != nil {
		return result, e
	}
	if req.SchemaVersion != 1 || req.ShareID == "" || operationID == "" {
		return result, p.NewFault("INVALID_INPUT", "TRANSFER")
	}
	boundSession := &Transfers{State: t.State, Clients: map[string]*NativeClient{account: c}}
	if req.OriginalOperationID != "" {
		operationID = req.OriginalOperationID
		req.OriginalOperationID = ""
		readOnly = true
	}
	user, e := c.Identity(ctx)
	if e != nil {
		return result, e
	}
	if _, e = ValidateTarget(ctx, boundSession, account, Target{req.TargetFolderID, req.TargetFolderRevision}); e != nil {
		return result, e
	}
	pwd, code, e := parseShare(req.URL)
	if e != nil {
		return result, e
	}
	identity := qhash([]any{account, user, req})
	intent := transferIntent{Identity: identity, Account: account, UserID: user, Request: req, Steps: map[string]*transferStep{}}
	journalName := "quark-transfer-" + qhash(operationID)
	loadErr := t.State.Read(journalName, &intent)
	if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
		return result, p.SafeFault(loadErr, "TRANSFER")
	}
	if intent.Identity != identity || intent.Account != account || intent.UserID != user || intent.Steps == nil {
		return result, p.NewFault("ACCOUNT_IDENTITY_CHANGED", "TRANSFER")
	}
	if errors.Is(loadErr, os.ErrNotExist) {
		if readOnly {
			return result, unknown()
		}
		token, e := c.shareToken(ctx, pwd, code)
		if e != nil {
			return result, e
		}
		intent.Sources, e = c.shareTree(ctx, pwd, token, "0", map[string]bool{})
		if e != nil {
			return result, e
		}
		if e = t.State.Write(journalName, intent); e != nil {
			return result, p.SafeFault(e, "TRANSFER")
		}
	}
	registry := managedShare{Account: account, UserID: user, TargetID: req.TargetFolderID, TargetRevision: req.TargetFolderRevision, Entries: map[string]copiedEntry{}}
	registryName := "quark-share-" + qhash([]string{account, req.ShareID})
	if err := t.State.Read(registryName, &registry); err != nil && !errors.Is(err, os.ErrNotExist) {
		return result, p.SafeFault(err, "TRANSFER")
	}
	if registry.Account != account || registry.UserID != user || registry.TargetID != req.TargetFolderID || registry.TargetRevision != req.TargetFolderRevision || registry.Entries == nil {
		return result, p.NewFault("VERSION_CHANGED", "TRANSFER")
	}
	if readOnly {
		// Reconcile every submitted intent before declaring any remainder safe.
		for _, step := range intent.Steps {
			if !step.Submitted {
				continue
			}
			proof, err := t.reconcileStep(ctx, c, step)
			if err != nil {
				return result, unknown()
			}
			step.Copies = proof
			for _, copy := range proof {
				registry.Entries[entryKey(copy.Source)] = copy
			}
		}
		if e = t.State.Write(journalName, intent); e != nil {
			return result, unknown()
		}
		if e = t.State.Write(registryName, registry); e != nil {
			return result, unknown()
		}
	}
	var freshToken string
	freshByKey := map[string]shareEntry{}
	loadFresh := func() error {
		if freshToken != "" {
			return nil
		}
		token, err := c.shareToken(ctx, pwd, code)
		if err != nil {
			return err
		}
		fresh, err := c.shareTree(ctx, pwd, token, "0", map[string]bool{})
		if err != nil {
			return err
		}
		var visit func([]shareEntry)
		visit = func(entries []shareEntry) {
			for _, entry := range entries {
				freshByKey[entryKey(entry)] = entry
				visit(entry.Children)
			}
		}
		visit(fresh)
		freshToken = token
		return nil
	}
	var copyEntries func([]shareEntry, string) error
	copyEntries = func(entries []shareEntry, parent string) error {
		for _, source := range entries {
			if err := ctx.Err(); err != nil {
				return p.SafeFault(err, "TRANSFER")
			}
			key := entryKey(source)
			if known, ok := registry.Entries[key]; ok {
				node, e := c.Node(ctx, account, known.RemoteID)
				if e != nil {
					return e
				}
				if node.ParentID == nil || *node.ParentID != parent {
					return p.NewFault("VERSION_CHANGED", "TRANSFER")
				}
				if source.Dir {
					if e = copyEntries(source.Children, known.RemoteID); e != nil {
						return e
					}
				} else {
					actual, e := c.remoteEntry(ctx, known.RemoteID)
					if e != nil {
						return e
					}
					if !sameContent(source, actual) {
						return p.NewFault("VERSION_CHANGED", "TRANSFER")
					}
				}
				continue
			}
			step := intent.Steps[key]
			if step == nil {
				if readOnly {
					return safeRemainder
				}
				children, e := allChildren(ctx, c, account, parent)
				if e != nil {
					return e
				}
				before := []string{}
				for _, n := range children {
					before = append(before, n.ID)
				}
				step = &transferStep{Source: source, ParentID: parent, Before: before}
				intent.Steps[key] = step
				if e = t.State.Write(journalName, intent); e != nil {
					return p.SafeFault(e, "TRANSFER")
				}
			}
			if !step.Submitted {
				if readOnly {
					return safeRemainder
				}
				// Resolve one fresh source snapshot for this execution, rather than
				// traversing the complete share again for every copied file. Each
				// resulting copy is still verified against the immutable source.
				if e = loadFresh(); e != nil {
					return e
				}
				latest, exists := freshByKey[key]
				if !exists || qhash(stripTokens(latest)) != qhash(stripTokens(source)) {
					return p.NewFault("VERSION_CHANGED", "TRANSFER")
				}
				if e = boundSession.validateWriteParent(ctx, account, Target{req.TargetFolderID, req.TargetFolderRevision}, registry, parent); e != nil {
					return e
				}
				step.Submitted = true
				if e = t.State.Write(journalName, intent); e != nil {
					return p.SafeFault(e, "TRANSFER")
				}
				response, e := c.rpc(ctx, http.MethodPost, "/1/clouddrive/share/sharepage/save", nil, map[string]any{"fid_list": []string{source.ID}, "fid_token_list": []string{latest.Token}, "to_pdir_fid": parent, "pwd_id": pwd, "stoken": freshToken, "pdir_fid": source.Parent, "scene": "link"})
				if e != nil {
					var fault *p.Fault
					if errors.As(e, &fault) && (fault.Code == "RATE_LIMITED" || fault.Code == "AUTH_REQUIRED" || fault.Code == "PERMISSION_DENIED" || fault.Code == "RESOURCE_DELETED") {
						step.Submitted = false
						if t.State.Write(journalName, intent) != nil {
							return unknown()
						}
						return fault
					}
					checkCtx, cancelCheck := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
					proof, check := t.reconcileStep(checkCtx, c, step)
					cancelCheck()
					if check != nil {
						return unknown()
					}
					step.Copies = proof
				} else {
					var saved struct {
						TaskID string `json:"task_id"`
					}
					if json.Unmarshal(response.Data, &saved) != nil || saved.TaskID == "" {
						return unknown()
					}
					step.TaskID = saved.TaskID
				}
				if e = t.State.Write(journalName, intent); e != nil {
					return unknown()
				}
			}
			var proof []copiedEntry
			var err error
			for poll := 0; poll < 20; poll++ {
				proof, err = t.reconcileStep(ctx, c, step)
				if err == nil {
					break
				}
				if readOnly || step.TaskID == "" {
					return unknown()
				}
				timer := time.NewTimer(500 * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					return unknown()
				case <-timer.C:
				}
			}
			if err != nil {
				return unknown()
			}
			step.Copies = proof
			if e = t.State.Write(journalName, intent); e != nil {
				return unknown()
			}
			for _, copy := range proof {
				registry.Entries[entryKey(copy.Source)] = copy
			}
			if e = t.State.Write(registryName, registry); e != nil {
				return unknown()
			}
		}
		return nil
	}
	if e = copyEntries(intent.Sources, req.TargetFolderID); e != nil {
		if errors.Is(e, safeRemainder) {
			result.SafeToResume = true
			return result, nil
		}
		return result, e
	}
	if e = t.State.Write(registryName, registry); e != nil {
		return result, p.SafeFault(e, "TRANSFER")
	}
	scoped, e := boundSession.ForShare(account, req.ShareID)
	if e != nil {
		return result, e
	}
	listed, e := List(ctx, scoped, ListRequest{SchemaVersion: 1, AccountID: account, ShareID: &req.ShareID})
	if e != nil {
		return result, e
	}
	result.Nodes, result.Complete = listed.Nodes, listed.Complete
	return result, nil
}
func stripTokens(s shareEntry) shareEntry {
	s.Token = ""
	children := make([]shareEntry, len(s.Children))
	for i := range s.Children {
		children[i] = stripTokens(s.Children[i])
	}
	s.Children = children
	return s
}
