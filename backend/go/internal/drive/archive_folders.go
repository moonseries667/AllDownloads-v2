package drive

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/executor"
)

// folderRecord pins every relative directory to one Drive object. Created
// directories carry an application key so a reserved ID can be reconciled
// after a lost create response without adopting an unrelated object.
type folderRecord struct {
	ID       string `json:"id"`
	ParentID string `json:"parentId"`
	Name     string `json:"name"`
	AppKey   string `json:"appKey,omitempty"`
	Created  bool   `json:"created"`
}

func folderMapKey(parent, name string) string {
	return digest([]byte(parent + "\x00" + name))
}

func folderAppKey(identity, parent, name string) string {
	return digest([]byte(identity + "\x00folder\x00" + parent + "\x00" + name))
}

func (c *Client) resolveParent(ctx context.Context, state *executor.State, stateName string, record *uploadRecord, identity, parent, relative string, allowWrite bool) (string, error) {
	if relative == "." {
		return parent, nil
	}
	for _, name := range strings.Split(relative, "/") {
		key := folderMapKey(parent, name)
		mapping, mapped := record.Folders[key]
		if mapped {
			if mapping.ID == "" || mapping.ParentID != parent || mapping.Name != name ||
				(mapping.Created && mapping.AppKey != folderAppKey(identity, parent, name)) ||
				(!mapping.Created && mapping.AppKey != "") {
				return "", fault("ARCHIVE_MANIFEST_CHANGED")
			}
			obj, err := c.get(ctx, mapping.ID)
			if isCode(err, "TARGET_NOT_FOUND") {
				if !allowWrite || !mapping.Created {
					return "", fault("ARCHIVE_NOT_CONFIRMED")
				}
				if err = c.createFolder(ctx, mapping); err != nil {
					return "", err
				}
				obj, err = c.get(ctx, mapping.ID)
			}
			if err != nil {
				return "", err
			}
			if err = verifyFolderObject(obj, mapping); err != nil {
				return "", err
			}
			parent = mapping.ID
			continue
		}
		if !allowWrite {
			return "", fault("ARCHIVE_NOT_CONFIRMED")
		}

		objects, next, err := c.listObjects(ctx, parent, "", 2, name)
		if err != nil {
			return "", err
		}
		if next != "" || len(objects) > 1 {
			return "", fault("ARCHIVE_DIRECTORY_AMBIGUOUS")
		}
		if len(objects) == 1 {
			mapping = folderRecord{ID: objects[0].ID, ParentID: parent, Name: name}
			if err = verifyFolderObject(objects[0], mapping); err != nil {
				return "", err
			}
		} else {
			id, reserveErr := c.reserveID(ctx)
			if reserveErr != nil {
				return "", reserveErr
			}
			mapping = folderRecord{ID: id, ParentID: parent, Name: name, AppKey: folderAppKey(identity, parent, name), Created: true}
		}
		record.Folders[key] = mapping
		// Fix the selected or reserved identity before any folder create request.
		if err = state.Write(stateName, *record); err != nil {
			return "", err
		}
		if mapping.Created {
			if err = c.createFolder(ctx, mapping); err != nil {
				return "", err
			}
			obj, getErr := c.get(ctx, mapping.ID)
			if getErr != nil {
				return "", getErr
			}
			if err = verifyFolderObject(obj, mapping); err != nil {
				return "", err
			}
		}
		parent = mapping.ID
	}
	return parent, nil
}

func (c *Client) createFolder(ctx context.Context, mapping folderRecord) error {
	base, err := c.base()
	if err != nil {
		return err
	}
	metadata, err := json.Marshal(map[string]any{
		"id":       mapping.ID,
		"name":     mapping.Name,
		"mimeType": folderMIME,
		"parents":  []string{mapping.ParentID},
		"appProperties": map[string]string{
			"alldownloadsKey": mapping.AppKey,
		},
	})
	if err != nil {
		return fault("INVALID_ARCHIVE_MANIFEST")
	}
	resp, err := c.request(ctx, http.MethodPost, base+"/drive/v3/files?"+url.Values{"fields": {objectFields}}.Encode(), bytes.NewReader(metadata), http.Header{"Content-Type": {"application/json; charset=UTF-8"}})
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func verifyFolderObject(obj Object, mapping folderRecord) error {
	if err := checkFolder(obj); err != nil {
		return err
	}
	if obj.ID != mapping.ID || obj.Name != mapping.Name || len(obj.Parents) != 1 || obj.Parents[0] != mapping.ParentID {
		return fault("ARCHIVE_VERIFICATION_FAILED")
	}
	if !obj.Capabilities.CanAdd || !obj.Capabilities.CanList {
		return fault("TARGET_PERMISSION_DENIED")
	}
	if mapping.Created && obj.Properties["alldownloadsKey"] != mapping.AppKey {
		return fault("ARCHIVE_VERIFICATION_FAILED")
	}
	return nil
}
