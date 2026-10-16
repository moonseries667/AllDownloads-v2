package quark

import (
	"context"
	"reflect"
	"sort"
	"strconv"
	"strings"

	p "github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms"
)

// Node is a provider fact, not a local filesystem path. Revision must change
// with identity, parent, permissions or content; size/name are not versions.
type Node struct {
	ID                                 string
	ParentID                           *string
	Name, Kind, Revision, Availability string
	AvailabilityReason                 *string
	Writable                           bool
	SizeBytes                          *string
	ModifiedAt                         *string
}

type Page struct {
	Nodes    []Node
	Next     *string
	Complete bool
}
type Client interface {
	Root(context.Context, string) (Node, error)
	Node(context.Context, string, string) (Node, error)
	Children(context.Context, string, string, *string) (Page, error)
	// ShareRoot resolves only shares already managed by this application.
	ShareRoot(context.Context, string, string) (Node, error)
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

type CloudNode struct {
	ID                 string         `json:"id"`
	ShareID            string         `json:"shareId"`
	ParentID           *string        `json:"parentId"`
	ExternalID         string         `json:"externalId"`
	Version            string         `json:"version"`
	Name               string         `json:"name"`
	Kind               string         `json:"kind"`
	RelativePath       string         `json:"relativePath"`
	SizeBytes          *string        `json:"sizeBytes"`
	ModifiedAt         *string        `json:"modifiedAt"`
	Availability       string         `json:"availability"`
	AvailabilityReason *string        `json:"availabilityReason"`
	DownloadSummary    map[string]any `json:"downloadSummary"`
}

type ListRequest struct {
	SchemaVersion int     `json:"schemaVersion"`
	AccountID     string  `json:"accountId"`
	ParentID      *string `json:"parentId"`
	ShareID       *string `json:"shareId"`
}
type ListResult struct {
	AccountID string      `json:"accountId"`
	ParentID  *string     `json:"parentId"`
	Folders   []Folder    `json:"folders"`
	Nodes     []CloudNode `json:"nodes"`
	Complete  bool        `json:"complete"`
}

func validNode(n Node) bool {
	if n.ID == "" || n.Revision == "" || (n.Kind != "FILE" && n.Kind != "FOLDER") || strings.ContainsAny(n.Name, "/\\\x00") || (n.ParentID != nil && (n.Name == "" || *n.ParentID == "")) {
		return false
	}
	switch n.Availability {
	case "AVAILABLE", "REMOVED", "UNAVAILABLE", "UNKNOWN":
	default:
		return false
	}
	if n.SizeBytes != nil {
		v, e := strconv.ParseUint(*n.SizeBytes, 10, 64)
		if e != nil || strconv.FormatUint(v, 10) != *n.SizeBytes {
			return false
		}
	}
	return true
}

func available(n Node, stage string) error {
	switch n.Availability {
	case "AVAILABLE":
		return nil
	case "REMOVED":
		return p.NewFault("RESOURCE_DELETED", stage)
	case "UNAVAILABLE":
		return p.NewFault("PERMISSION_DENIED", stage)
	default:
		return p.NewFault("PROVIDER_CHANGED", stage)
	}
}

// ancestry resolves IDs all the way to the provider's root. Equal names never
// merge directories; a moved node yields a new chain/revision, not a fallback.
func ancestry(ctx context.Context, c Client, account string, node Node, root Node) ([]Node, error) {
	chain := []Node{}
	seen := map[string]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return nil, p.SafeFault(err, "LIST")
		}
		if !validNode(node) || seen[node.ID] || len(chain) >= 256 {
			return nil, p.NewFault("PROVIDER_CHANGED", "LIST")
		}
		seen[node.ID] = true
		chain = append(chain, node)
		if node.ID == root.ID {
			if !reflect.DeepEqual(node, root) || node.ParentID != nil {
				return nil, p.NewFault("VERSION_CHANGED", "LIST")
			}
			break
		}
		if node.ParentID == nil {
			return nil, p.NewFault("VERSION_CHANGED", "LIST")
		}
		parentID := *node.ParentID
		parent, err := c.Node(ctx, account, parentID)
		if err != nil {
			return nil, p.SafeFault(err, "LIST")
		}
		if parent.ID != parentID || parent.Kind != "FOLDER" {
			return nil, p.NewFault("PROVIDER_CHANGED", "LIST")
		}
		node = parent
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain, nil
}

func folder(account string, n Node, path string) Folder {
	name := n.Name
	if n.ParentID == nil {
		name = "我的云盘"
	}
	return Folder{n.ID, account, n.ParentID, name, "FOLDER", n.Revision, n.Writable && n.Availability == "AVAILABLE", n.Availability, n.AvailabilityReason, path}
}

func join(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "/" + name
}

// List consumes provider pagination before marking the snapshot complete.
// Directory-picker requests list one level; managed-share requests walk the
// complete subtree so Java can freeze folders even when the UI has not expanded.
func List(ctx context.Context, c Client, req ListRequest) (ListResult, error) {
	result := ListResult{AccountID: req.AccountID, ParentID: req.ParentID, Folders: []Folder{}, Nodes: []CloudNode{}}
	if req.SchemaVersion != 1 || req.AccountID == "" || (req.ParentID != nil && *req.ParentID == "") || (req.ShareID != nil && *req.ShareID == "") {
		return result, p.NewFault("INVALID_INPUT", "LIST")
	}
	if err := ctx.Err(); err != nil {
		return result, p.SafeFault(err, "LIST")
	}
	if c == nil {
		return result, p.NewFault("PROVIDER_UNAVAILABLE", "LIST")
	}
	root, err := c.Root(ctx, req.AccountID)
	if err != nil {
		return result, p.SafeFault(err, "LIST")
	}
	if !validNode(root) || root.ParentID != nil || root.Kind != "FOLDER" {
		return result, p.NewFault("PROVIDER_CHANGED", "LIST")
	}
	parent := root
	var shareRoot Node
	if req.ShareID != nil {
		shareRoot, err = c.ShareRoot(ctx, req.AccountID, *req.ShareID)
		if err != nil {
			return result, p.SafeFault(err, "LIST")
		}
		parent = shareRoot
	}
	if req.ParentID != nil {
		parent, err = c.Node(ctx, req.AccountID, *req.ParentID)
		if err != nil {
			return result, p.SafeFault(err, "LIST")
		}
		if parent.ID != *req.ParentID {
			return result, p.NewFault("PROVIDER_CHANGED", "LIST")
		}
	}
	if parent.Kind != "FOLDER" {
		return result, p.NewFault("INVALID_INPUT", "LIST")
	}
	if err := available(parent, "LIST"); err != nil {
		return result, err
	}
	chain, err := ancestry(ctx, c, req.AccountID, parent, root)
	if err != nil {
		return result, err
	}
	path, relative := "", ""
	inside := false
	for i, n := range chain {
		if i > 0 {
			path = join(path, n.Name)
		}
		result.Folders = append(result.Folders, folder(req.AccountID, n, path))
		if req.ShareID != nil {
			if inside {
				relative = join(relative, n.Name)
			}
			if n.ID == shareRoot.ID {
				if n.Revision != shareRoot.Revision {
					return result, p.NewFault("VERSION_CHANGED", "LIST")
				}
				inside = true
			}
		}
	}
	if req.ShareID != nil && !inside {
		return result, p.NewFault("PERMISSION_DENIED", "LIST")
	}
	type pending struct {
		n              Node
		path, relative string
	}
	queue := []pending{{parent, path, relative}}
	seen := map[string]Node{parent.ID: parent}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		var cursor *string
		cursors := map[string]bool{}
		for {
			if err := ctx.Err(); err != nil {
				return result, p.SafeFault(err, "LIST")
			}
			page, err := c.Children(ctx, req.AccountID, current.n.ID, cursor)
			if err != nil {
				return result, p.SafeFault(err, "LIST")
			}
			if (page.Complete && page.Next != nil) || (!page.Complete && (page.Next == nil || *page.Next == "")) {
				return result, p.NewFault("PROVIDER_CHANGED", "LIST")
			}
			for _, n := range page.Nodes {
				if !validNode(n) || n.ParentID == nil || *n.ParentID != current.n.ID {
					return result, p.NewFault("PROVIDER_CHANGED", "LIST")
				}
				if old, ok := seen[n.ID]; ok {
					if !reflect.DeepEqual(old, n) {
						return result, p.NewFault("VERSION_CHANGED", "LIST")
					}
					continue
				}
				seen[n.ID] = n
				nPath, nRelative := join(current.path, n.Name), join(current.relative, n.Name)
				if n.Kind == "FOLDER" {
					result.Folders = append(result.Folders, folder(req.AccountID, n, nPath))
				}
				if req.ShareID != nil {
					result.Nodes = append(result.Nodes, CloudNode{n.ID, *req.ShareID, n.ParentID, n.ID, n.Revision, n.Name, n.Kind, nRelative, n.SizeBytes, n.ModifiedAt, n.Availability, n.AvailabilityReason, map[string]any{}})
					if n.Kind == "FOLDER" {
						if err := available(n, "LIST"); err != nil {
							return result, err
						}
						queue = append(queue, pending{n, nPath, nRelative})
					}
				}
			}
			if page.Complete {
				break
			}
			if cursors[*page.Next] {
				return result, p.NewFault("PROVIDER_CHANGED", "LIST")
			}
			cursors[*page.Next] = true
			cursor = page.Next
		}
		// Re-read the directory revision so paging across a moving tree is never
		// described as a complete immutable snapshot.
		latest, err := c.Node(ctx, req.AccountID, current.n.ID)
		if err != nil {
			return result, p.SafeFault(err, "LIST")
		}
		if !reflect.DeepEqual(latest, current.n) {
			return result, p.NewFault("VERSION_CHANGED", "LIST")
		}
	}
	sort.Slice(result.Nodes, func(i, j int) bool { return result.Nodes[i].ID < result.Nodes[j].ID })
	result.Complete = true
	return result, nil
}

type Target struct {
	FolderID string `json:"targetFolderId"`
	Revision string `json:"targetFolderRevision"`
}

// ValidateTarget is called again immediately before any transfer. It never
// creates folders, guesses an alternate path, or changes a local download root.
func ValidateTarget(ctx context.Context, c Client, account string, target Target) (Folder, error) {
	if c == nil {
		return Folder{}, p.NewFault("PROVIDER_UNAVAILABLE", "TRANSFER_TARGET")
	}
	if account == "" || target.FolderID == "" || target.Revision == "" {
		return Folder{}, p.NewFault("INVALID_INPUT", "TRANSFER_TARGET")
	}
	root, err := c.Root(ctx, account)
	if err != nil {
		return Folder{}, p.SafeFault(err, "TRANSFER_TARGET")
	}
	n, err := c.Node(ctx, account, target.FolderID)
	if err != nil {
		return Folder{}, p.SafeFault(err, "TRANSFER_TARGET")
	}
	if !validNode(n) || n.ID != target.FolderID || n.Kind != "FOLDER" {
		return Folder{}, p.NewFault("INVALID_INPUT", "TRANSFER_TARGET")
	}
	if err := available(n, "TRANSFER_TARGET"); err != nil {
		return Folder{}, err
	}
	if !n.Writable {
		return Folder{}, p.NewFault("PERMISSION_DENIED", "TRANSFER_TARGET")
	}
	if n.Revision != target.Revision {
		return Folder{}, p.NewFault("VERSION_CHANGED", "TRANSFER_TARGET")
	}
	chain, err := ancestry(ctx, c, account, n, root)
	if err != nil {
		return Folder{}, err
	}
	path := ""
	for i, parent := range chain {
		if i > 0 {
			path = join(path, parent.Name)
		}
	}
	return folder(account, n, path), nil
}
