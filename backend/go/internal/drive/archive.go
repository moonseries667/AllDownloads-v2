package drive

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
)

type ArtifactFile struct {
	OutputID     string   `json:"outputId"`
	RelativePath string   `json:"relativePath"`
	SizeBytes    string   `json:"sizeBytes"`
	SHA256       string   `json:"sha256"`
	MediaKinds   []string `json:"mediaKinds"`
	Verified     bool     `json:"verified"`
}
type ArchiveRequest struct {
	SchemaVersion int            `json:"schemaVersion"`
	ProjectID     string         `json:"projectId"`
	ArchivePolicy string         `json:"archivePolicy"`
	ArchiveTarget Target         `json:"archiveTarget"`
	Files         []ArtifactFile `json:"files"`
}
type ArchivedFile struct {
	OutputID     string `json:"outputId"`
	RelativePath string `json:"relativePath"`
	SizeBytes    string `json:"sizeBytes"`
	SHA256       string `json:"sha256"`
	RemoteID     string `json:"remoteId"`
	RemoteName   string `json:"remoteName"`
	Verified     bool   `json:"verified"`
}
type ArchiveResult struct {
	ProjectID     string         `json:"projectId"`
	ArchiveTarget Target         `json:"archiveTarget"`
	Files         []ArchivedFile `json:"files"`
	Verified      bool           `json:"verified"`
}
type uploadRecord struct {
	Identity    string
	AuthBinding string `json:"authBinding,omitempty"`
	RemoteIDs   map[string]string
	Parents     map[string]string
	Folders     map[string]folderRecord
}

type archiveAuthKey struct{}

// Archive never enumerates local directories or creates a business operation.
// It creates only the remote directories implied by the immutable file list,
// transfers those files, and verifies each reserved remote ID. Lost responses
// are reconciled against persisted IDs before another write.
func (c *Client) Archive(ctx context.Context, state *executor.State, operationID, root string, req ArchiveRequest) (ArchiveResult, error) {
	return c.archive(ctx, state, operationID, root, req, true)
}

// Verify observes a persisted upload without resending bytes or reserving IDs.
func (c *Client) Verify(ctx context.Context, state *executor.State, operationID, root string, req ArchiveRequest) (ArchiveResult, error) {
	return c.archive(ctx, state, operationID, root, req, false)
}
func (c *Client) archive(ctx context.Context, state *executor.State, operationID, root string, req ArchiveRequest, allowWrite bool) (ArchiveResult, error) {
	result := ArchiveResult{ProjectID: req.ProjectID, ArchiveTarget: req.ArchiveTarget, Files: []ArchivedFile{}}
	if state == nil || operationID == "" || req.SchemaVersion != 1 || req.ProjectID == "" || req.ArchivePolicy != "AUTO" || len(req.Files) == 0 || req.ArchiveTarget.AccountID != c.AccountID {
		return result, fault("INVALID_ARCHIVE_MANIFEST")
	}
	binding := ""
	if c.auth != nil {
		stored, err := c.auth.credentials(ctx, c)
		if err != nil {
			return result, err
		}
		binding = authBinding(stored)
		ctx = context.WithValue(ctx, archiveAuthKey{}, binding)
	}
	seenID, seenPath := map[string]bool{}, map[string]bool{}
	for _, f := range req.Files {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		hash, err := hex.DecodeString(f.SHA256)
		if !f.Verified || f.OutputID == "" || seenID[f.OutputID] || seenPath[strings.ToLower(f.RelativePath)] || err != nil || len(hash) != 32 || strings.ToLower(f.SHA256) != f.SHA256 || executor.ValidateRelativePath(f.RelativePath) != nil || strings.Contains(f.RelativePath, "\\") {
			return result, fault("INVALID_ARCHIVE_MANIFEST")
		}
		if err := executor.VerifyFile(root, f.RelativePath, f.SHA256, f.SizeBytes); err != nil {
			return result, fault("LOCAL_VERIFICATION_FAILED")
		}
		seenID[f.OutputID] = true
		seenPath[strings.ToLower(f.RelativePath)] = true
	}
	if _, err := c.Validate(ctx, req.ArchiveTarget.FolderID, req.ArchiveTarget.Revision); err != nil {
		return result, err
	}
	raw, _ := json.Marshal(struct {
		Root    string
		Request ArchiveRequest
	}{filepath.Clean(root), req})
	identity := digest(raw)
	name := "upload-" + digest([]byte(operationID))
	var record uploadRecord
	readErr := state.Read(name, &record)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return result, readErr
	}
	// Do not silently adopt legacy records whose actual Google identity was
	// never pinned. They remain available to the original tokenEnv client.
	if readErr == nil && record.AuthBinding != binding {
		return result, fault("ACCOUNT_IDENTITY_CHANGED")
	}
	if errors.Is(readErr, os.ErrNotExist) {
		record = uploadRecord{Identity: identity, AuthBinding: binding, RemoteIDs: map[string]string{}, Parents: map[string]string{}}
	}
	if record.Identity != identity || record.RemoteIDs == nil || record.Parents == nil {
		return result, fault("ARCHIVE_MANIFEST_CHANGED")
	}
	if record.Folders == nil {
		record.Folders = map[string]folderRecord{}
	}
	if allowWrite {
		if err := state.Write(name, record); err != nil {
			return result, err
		}
	}
	for _, f := range req.Files {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		parent, err := c.resolveParent(ctx, state, name, &record, identity, req.ArchiveTarget.FolderID, path.Dir(f.RelativePath), allowWrite)
		if err != nil {
			return result, err
		}
		if previous := record.Parents[f.OutputID]; previous != "" && previous != parent {
			return result, fault("TARGET_CHANGED")
		}
		remoteID := record.RemoteIDs[f.OutputID]
		if remoteID == "" {
			if !allowWrite {
				return result, fault("ARCHIVE_NOT_CONFIRMED")
			}
			remoteID, err = c.reserveID(ctx)
			if err != nil {
				return result, err
			}
			record.RemoteIDs[f.OutputID] = remoteID
			record.Parents[f.OutputID] = parent
			if err = state.Write(name, record); err != nil {
				return result, err
			}
		}
		key := digest([]byte(identity + "\x00" + f.OutputID))
		object, err := c.get(ctx, remoteID)
		if isCode(err, "TARGET_NOT_FOUND") {
			if !allowWrite {
				return result, fault("ARCHIVE_NOT_CONFIRMED")
			}
			if err = c.upload(ctx, remoteID, parent, path.Base(f.RelativePath), key, filepath.Join(root, filepath.FromSlash(f.RelativePath)), f.SizeBytes); err != nil {
				return result, err
			}
			object, err = c.get(ctx, remoteID)
		}
		if err != nil {
			return result, err
		}
		if err = verifyObject(object, f, parent, key, root); err != nil {
			return result, err
		}
		result.Files = append(result.Files, ArchivedFile{f.OutputID, f.RelativePath, f.SizeBytes, f.SHA256, object.ID, object.Name, true})
	}
	// Recheck local input after streaming and remote facts before returning.
	for _, f := range req.Files {
		if err := executor.VerifyFile(root, f.RelativePath, f.SHA256, f.SizeBytes); err != nil {
			return result, fault("LOCAL_VERIFICATION_FAILED")
		}
	}
	result.Verified = true
	return result, nil
}

func verifyObject(o Object, f ArtifactFile, parent, key, root string) error {
	if o.Trashed || o.ID == "" || o.Name != path.Base(f.RelativePath) || o.Size != f.SizeBytes || len(o.Parents) != 1 || o.Parents[0] != parent || o.MimeType == folderMIME || o.Properties["alldownloadsKey"] != key {
		return fault("ARCHIVE_VERIFICATION_FAILED")
	}
	if o.SHA256 != "" {
		if !strings.EqualFold(o.SHA256, f.SHA256) {
			return fault("ARCHIVE_VERIFICATION_FAILED")
		}
		return nil
	}
	if o.MD5 == "" {
		return fault("REMOTE_CHECKSUM_UNAVAILABLE")
	}
	file, err := os.Open(filepath.Join(root, filepath.FromSlash(f.RelativePath)))
	if err != nil {
		return err
	}
	h := md5.New()
	_, copyErr := io.Copy(h, file)
	closeErr := file.Close()
	if err = errors.Join(copyErr, closeErr); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), o.MD5) {
		return fault("ARCHIVE_VERIFICATION_FAILED")
	}
	return nil
}

func (c *Client) reserveID(ctx context.Context) (string, error) {
	base, err := c.base()
	if err != nil {
		return "", err
	}
	resp, err := c.request(ctx, "GET", base+"/drive/v3/files/generateIds?count=1&space=drive&type=files", nil, nil)
	if err != nil {
		return "", err
	}
	var ids struct {
		IDs []string `json:"ids"`
	}
	if err = decodeResponse(resp, &ids); err != nil {
		return "", err
	}
	if len(ids.IDs) != 1 || ids.IDs[0] == "" {
		return "", fault("PROVIDER_CHANGED")
	}
	return ids.IDs[0], nil
}
func (c *Client) upload(ctx context.Context, id, parent, name, key, filePath, size string) error {
	base, err := c.base()
	if err != nil {
		return err
	}
	metadata, _ := json.Marshal(map[string]any{"id": id, "name": name, "parents": []string{parent}, "appProperties": map[string]string{"alldownloadsKey": key}})
	resp, err := c.request(ctx, "POST", base+"/upload/drive/v3/files?uploadType=resumable", bytes.NewReader(metadata), http.Header{"Content-Type": {"application/json; charset=UTF-8"}, "X-Upload-Content-Type": {"application/octet-stream"}, "X-Upload-Content-Length": {size}})
	if err != nil {
		return err
	}
	location := resp.Header.Get("Location")
	resp.Body.Close()
	u, parseErr := url.Parse(location)
	origin, _ := url.Parse(base)
	if parseErr != nil || u.User != nil || u.Scheme != origin.Scheme || u.Host != origin.Host || !strings.HasPrefix(u.Path, "/upload/drive/v3/files") {
		return fault("INVALID_UPLOAD_SESSION")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	count, err := strconv.ParseInt(size, 10, 64)
	if err != nil || count < 0 {
		return fault("INVALID_ARCHIVE_MANIFEST")
	}
	resp, err = c.request(ctx, "PUT", location, io.LimitReader(file, count), http.Header{"Content-Type": {"application/octet-stream"}, "Content-Length": {size}})
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
