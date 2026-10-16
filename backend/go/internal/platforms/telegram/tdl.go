package telegram

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
	p "github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms"
)

const TDLVersion = "0.20.4"

// TDLAccount uses a newly authorized, private tdl namespace. PeerID pins the
// Saved Messages owner; changing a session cannot redirect an accepted plan.
type TDLAccount struct {
	Namespace  string `json:"namespace"`
	StorageDir string `json:"storageDir"`
	PeerID     string `json:"peerId"`
}
type TDL struct {
	Executable string
	State      *executor.State
	WorkDir    string
	Accounts   map[string]TDLAccount
	Timeout    time.Duration
	// Run is a private protocol-test seam, never populated from runtime JSON.
	Run func(context.Context, string, time.Duration, ...string) ([]byte, error)
	mu  sync.Mutex
}
type savedSnapshot struct {
	PeerID  string
	Through string
	Groups  map[string][]RawMessage
}
type scanSnapshot struct {
	Account  string
	PeerID   string
	After    string
	Messages []RawMessage
	Groups   map[string][]RawMessage
}

func telHash(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func (t *TDL) command(ctx context.Context, args ...string) ([]byte, error) {
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	var out, diag []byte
	var err error
	if t.Run != nil {
		out, err = t.Run(ctx, t.Executable, timeout, args...)
	} else {
		toolHome := filepath.Join(t.WorkDir, "tool-home")
		if !filepath.IsAbs(toolHome) {
			return nil, p.NewFault("INVALID_INPUT", "CONFIG")
		}
		if err := os.MkdirAll(toolHome, 0700); err != nil {
			return nil, p.SafeFault(err, "CONFIG")
		}
		out, diag, err = media.RunControlledEnvironment(ctx, t.Executable, timeout, map[string]string{"USERPROFILE": toolHome, "TDL_DEBUG": "false"}, args...)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, p.SafeFault(ctx.Err(), "TELEGRAM")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, p.NewFault("NETWORK_TEMPORARY", "TELEGRAM")
		}
		text := string(diag) + string(out)
		flood := regexp.MustCompile(`(?:FLOOD_WAIT_|FLOOD_PREMIUM_WAIT_)([0-9]+)`).FindStringSubmatch(text)
		if len(flood) == 2 {
			delay, _ := strconv.Atoi(flood[1])
			f := p.NewFault("RATE_LIMITED", "TELEGRAM")
			f.RetryAfterSeconds = &delay
			return nil, f
		}
		for _, code := range []string{"AUTH_KEY_UNREGISTERED", "SESSION_REVOKED", "SESSION_EXPIRED", "USER_DEACTIVATED", "not authorized"} {
			if strings.Contains(text, code) {
				return nil, p.NewFault("AUTH_REQUIRED", "TELEGRAM")
			}
		}
		return nil, p.NewFault("AUTH_OR_TOOL_REQUIRED", "TELEGRAM")
	}
	return out, nil
}

// Check is local-only: ordinary startup never opens a Telegram session.
func (t *TDL) Check(ctx context.Context) error {
	if t.State == nil || !filepath.IsAbs(t.WorkDir) || !filepath.IsAbs(t.Executable) {
		return p.NewFault("INVALID_INPUT", "CONFIG")
	}
	out, err := t.command(ctx, "version")
	if err != nil {
		return err
	}
	if !strings.Contains(string(out), "Version: "+TDLVersion) && !strings.Contains(string(out), "Version: v"+TDLVersion) {
		return p.NewFault("TOOL_VERSION_MISMATCH", "CONFIG")
	}
	for id, a := range t.Accounts {
		if id == "" || a.Namespace == "" || strings.ContainsAny(a.Namespace, "/\\,\x00") || !filepath.IsAbs(a.StorageDir) || strings.Contains(a.StorageDir, ",") {
			return p.NewFault("INVALID_INPUT", "CONFIG")
		}
		if n, e := strconv.ParseUint(a.PeerID, 10, 64); e != nil || n == 0 {
			return p.NewFault("INVALID_INPUT", "CONFIG")
		}
	}
	return os.MkdirAll(t.WorkDir, 0700)
}
func (t *TDL) account(id string) (TDLAccount, error) {
	a, ok := t.Accounts[id]
	if !ok {
		return a, p.NewFault("AUTH_REQUIRED", "TELEGRAM")
	}
	if info, err := os.Stat(a.StorageDir); err != nil || !info.IsDir() {
		return a, p.NewFault("AUTH_REQUIRED", "TELEGRAM")
	}
	return a, nil
}
func (t *TDL) args(a TDLAccount) []string {
	return []string{"--ns", a.Namespace, "--storage", "type=bolt,path=" + a.StorageDir, "--reconnect-timeout", "30s", "--pool", "1", "--threads", "1", "--limit", "1", "--delay", "500ms", "--disable-progress-ps"}
}
func (t *TDL) export(ctx context.Context, a TDLAccount, from, to string) (TDLExport, error) {
	empty := TDLExport{}
	dir, err := os.MkdirTemp(t.WorkDir, "export-")
	if err != nil {
		return empty, p.SafeFault(err, "SCAN")
	}
	defer os.RemoveAll(dir)
	output := filepath.Join(dir, "saved.json")
	args := append(t.args(a), "chat", "export", "--all", "--raw", "--with-content", "--type", "id", "--input", from+","+to, "--output", output)
	if _, err = t.command(ctx, args...); err != nil {
		return empty, err
	}
	f, err := os.Open(output)
	if err != nil {
		return empty, p.NewFault("PROVIDER_CHANGED", "SCAN")
	}
	defer f.Close()
	result, err := ParseTDLExport(f)
	if err != nil {
		return empty, err
	}
	if result.PeerID != a.PeerID {
		return empty, p.NewFault("ACCOUNT_IDENTITY_CHANGED", "SCAN")
	}
	return result, nil
}

// SavedPage exports every type in the requested ID interval. A durable group
// index contains all earlier successfully observed members, including photos.
// Losing the private index requires one read-only history repair, never guessed
// group completeness based on neighboring messages or timestamps.
func (t *TDL) SavedPage(ctx context.Context, account, after string, continuation *string) (Page, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	a, err := t.account(account)
	if err != nil {
		return Page{}, err
	}
	n, err := messageNumber(after)
	if err != nil || n > 2147483647 {
		return Page{}, p.NewFault("INVALID_INPUT", "SCAN")
	}
	snapshot := scanSnapshot{}
	offset := 0
	name := "tel-scan-" + telHash(account)
	if continuation != nil {
		parts := strings.Split(*continuation, ":")
		if len(parts) != 2 {
			return Page{}, p.NewFault("INVALID_INPUT", "SCAN")
		}
		offset, err = strconv.Atoi(parts[1])
		if err != nil || offset < 0 || t.State.Read(name, &snapshot) != nil || snapshot.Account != account || snapshot.PeerID != a.PeerID {
			return Page{}, p.NewFault("VERSION_CHANGED", "SCAN")
		}
		raw, _ := json.Marshal(snapshot)
		if parts[0] != telHash(string(raw)) {
			return Page{}, p.NewFault("VERSION_CHANGED", "SCAN")
		}
	} else {
		previous := savedSnapshot{PeerID: a.PeerID, Through: "0", Groups: map[string][]RawMessage{}}
		e := t.State.Read("tel-groups-"+telHash(account), &previous)
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return Page{}, p.SafeFault(e, "SCAN")
		}
		if previous.PeerID != a.PeerID {
			return Page{}, p.NewFault("ACCOUNT_IDENTITY_CHANGED", "SCAN")
		}
		from := strconv.FormatUint(n+1, 10)
		priorThrough, _ := messageNumber(previous.Through)
		if errors.Is(e, os.ErrNotExist) || priorThrough < n {
			from = "1"
			previous.Groups = map[string][]RawMessage{}
		}
		exported, err := t.export(ctx, a, from, "2147483647")
		if err != nil {
			return Page{}, err
		}
		if previous.Groups == nil {
			previous.Groups = map[string][]RawMessage{}
		}
		snapshot = scanSnapshot{Account: account, PeerID: a.PeerID, After: after, Messages: []RawMessage{}, Groups: previous.Groups}
		high := n
		for _, m := range exported.Messages {
			id, _ := messageNumber(m.ID)
			if id > high {
				high = id
			}
			if id > n {
				snapshot.Messages = append(snapshot.Messages, m)
			}
			if m.GroupID != nil {
				members := snapshot.Groups[*m.GroupID]
				found := false
				for i := range members {
					if members[i].ID == m.ID {
						members[i] = m
						found = true
						break
					}
				}
				if !found {
					members = append(members, m)
				}
				snapshot.Groups[*m.GroupID] = members
			}
		}
		sort.Slice(snapshot.Messages, func(i, j int) bool {
			x, _ := messageNumber(snapshot.Messages[i].ID)
			y, _ := messageNumber(snapshot.Messages[j].ID)
			return x < y
		})
		if err = t.State.Write("tel-groups-"+telHash(account), savedSnapshot{a.PeerID, strconv.FormatUint(high, 10), snapshot.Groups}); err != nil {
			return Page{}, p.SafeFault(err, "SCAN")
		}
		if err = t.State.Write(name, snapshot); err != nil {
			return Page{}, p.SafeFault(err, "SCAN")
		}
	}
	if offset > len(snapshot.Messages) {
		return Page{}, p.NewFault("VERSION_CHANGED", "SCAN")
	}
	end := offset + 200
	if end > len(snapshot.Messages) {
		end = len(snapshot.Messages)
	}
	through := after
	if end > offset {
		through = snapshot.Messages[end-1].ID
	}
	result := Page{Messages: append([]RawMessage{}, snapshot.Messages[offset:end]...), Through: through, Complete: end == len(snapshot.Messages)}
	if !result.Complete {
		raw, _ := json.Marshal(snapshot)
		next := telHash(string(raw)) + ":" + strconv.Itoa(end)
		result.Next = &next
	}
	return result, nil
}
func (t *TDL) SavedGroup(ctx context.Context, account, group string) (Group, error) {
	if err := ctx.Err(); err != nil {
		return Group{}, p.SafeFault(err, "SCAN_GROUP")
	}
	a, err := t.account(account)
	if err != nil {
		return Group{}, err
	}
	var snapshot scanSnapshot
	if t.State.Read("tel-scan-"+telHash(account), &snapshot) != nil || snapshot.PeerID != a.PeerID || len(snapshot.Groups[group]) == 0 {
		return Group{}, p.NewFault("GROUP_INCOMPLETE", "SCAN_GROUP")
	}
	return Group{Messages: snapshot.Groups[group], Complete: true}, nil
}

type tempAttachment struct {
	*os.File
	dir string
}

func (f *tempAttachment) Close() error { return errors.Join(f.File.Close(), os.RemoveAll(f.dir)) }
func (t *TDL) OpenAttachment(ctx context.Context, account string, messageIDs []string, want DownloadAttachment) (io.ReadCloser, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	a, err := t.account(account)
	if err != nil {
		return nil, err
	}
	if n, e := messageNumber(want.MessageID); e != nil || n == 0 {
		return nil, p.NewFault("INVALID_INPUT", "DOWNLOAD")
	}
	verify := func() error {
		exported, e := t.export(ctx, a, want.MessageID, want.MessageID)
		if e != nil {
			return e
		}
		if len(exported.Messages) != 1 || exported.Messages[0].ID != want.MessageID {
			return p.NewFault("RESOURCE_DELETED", "DOWNLOAD")
		}
		for _, m := range exported.Messages[0].Media {
			if m.ExternalID == want.ExternalID && m.Name == want.Name && Classify(m) == want.MediaType && (want.SizeBytes == nil || (m.SizeBytes != nil && *m.SizeBytes == *want.SizeBytes)) {
				return nil
			}
		}
		return p.NewFault("VERSION_CHANGED", "DOWNLOAD")
	}
	if err = verify(); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(t.WorkDir, "attachment-")
	if err != nil {
		return nil, p.SafeFault(err, "DOWNLOAD")
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(dir)
		}
	}()
	input := filepath.Join(dir, "input.json")
	peer, err := strconv.ParseInt(a.PeerID, 10, 64)
	if err != nil {
		return nil, p.NewFault("INVALID_INPUT", "DOWNLOAD")
	}
	id, _ := strconv.Atoi(want.MessageID)
	// A one-message export and no --group prevents a single-item cancel or retry
	// from downloading unrelated members. The output name is an internal constant.
	raw, _ := json.Marshal(map[string]any{"id": peer, "messages": []any{map[string]any{"id": id, "type": "message", "file": want.Name}}})
	if err = os.WriteFile(input, raw, 0600); err != nil {
		return nil, p.SafeFault(err, "DOWNLOAD")
	}
	args := append(t.args(a), "download", "--file", input, "--dir", dir, "--template", "payload", "--continue")
	if _, err = t.command(ctx, args...); err != nil {
		return nil, err
	}
	if err = verify(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, p.SafeFault(err, "DOWNLOAD")
	}
	var output string
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasPrefix(entry.Name(), "payload") {
			if output != "" {
				return nil, p.NewFault("PROVIDER_CHANGED", "DOWNLOAD")
			}
			output = filepath.Join(dir, entry.Name())
		}
	}
	if output == "" {
		return nil, p.NewFault("VERIFICATION_FAILED", "DOWNLOAD")
	}
	file, err := os.Open(output)
	if err != nil {
		return nil, p.SafeFault(err, "DOWNLOAD")
	}
	if want.SizeBytes != nil {
		stat, e := file.Stat()
		if e != nil || strconv.FormatInt(stat.Size(), 10) != *want.SizeBytes {
			file.Close()
			return nil, p.NewFault("VERIFICATION_FAILED", "DOWNLOAD")
		}
	}
	success = true
	return &tempAttachment{file, dir}, nil
}
