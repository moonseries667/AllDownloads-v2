package platforms_test

import (
	"context"
	"errors"
	"testing"

	p "github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/quark"
)

type cloud struct {
	nodes       map[string]quark.Node
	pages       map[string][]quark.Page
	calls       map[string]int
	root, share string
	mutate      func(string)
}

func (c *cloud) Root(ctx context.Context, a string) (quark.Node, error) { return c.nodes[c.root], nil }
func (c *cloud) Node(ctx context.Context, a, id string) (quark.Node, error) {
	n, ok := c.nodes[id]
	if !ok {
		return n, p.NewFault("RESOURCE_DELETED", "LOOKUP")
	}
	return n, nil
}
func (c *cloud) ShareRoot(ctx context.Context, a, id string) (quark.Node, error) {
	if id != "managed" {
		return quark.Node{}, p.NewFault("PERMISSION_DENIED", "LOOKUP")
	}
	return c.nodes[c.share], nil
}
func (c *cloud) Children(ctx context.Context, a, id string, cursor *string) (quark.Page, error) {
	if err := ctx.Err(); err != nil {
		return quark.Page{}, err
	}
	i := c.calls[id]
	c.calls[id]++
	if c.mutate != nil {
		c.mutate(id)
	}
	if len(c.pages[id]) == 0 {
		return quark.Page{Complete: true}, nil
	}
	return c.pages[id][i%len(c.pages[id])], nil
}
func tree() *cloud {
	root := quark.Node{ID: "actual-root", Name: "", Kind: "FOLDER", Revision: "r1", Availability: "AVAILABLE", Writable: true}
	a := quark.Node{ID: "a", ParentID: ptr(root.ID), Name: "课程", Kind: "FOLDER", Revision: "a1", Availability: "AVAILABLE", Writable: true}
	b := a
	b.ID = "b"
	b.Revision = "b1"
	child := a
	child.ID = "child"
	child.ParentID = ptr(a.ID)
	child.Name = "第二课"
	child.Revision = "c1"
	file := quark.Node{ID: "file", ParentID: ptr(child.ID), Name: "介绍.mp4", Kind: "FILE", Revision: "v1", Availability: "AVAILABLE", SizeBytes: ptr("0")}
	return &cloud{nodes: map[string]quark.Node{root.ID: root, a.ID: a, b.ID: b, child.ID: child, file.ID: file}, pages: map[string][]quark.Page{root.ID: {{Nodes: []quark.Node{a}, Next: ptr("two")}, {Nodes: []quark.Node{a, b}, Complete: true}}, a.ID: {{Nodes: []quark.Node{child}, Complete: true}}, child.ID: {{Nodes: []quark.Node{file}, Complete: true}}}, calls: map[string]int{}, root: root.ID, share: a.ID}
}

func TestQuarkRootPaginationSameNamesAndEmptyFolder(t *testing.T) {
	c := tree()
	res, err := quark.List(context.Background(), c, quark.ListRequest{SchemaVersion: 1, AccountID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Complete || len(res.Folders) != 3 || len(res.Nodes) != 0 || c.calls[c.root] != 2 {
		t.Fatalf("%+v", res)
	}
	root := res.Folders[0]
	if root.ID != c.root || root.Name != "我的云盘" || root.Path != "" || !root.Selectable || root.ParentID != nil {
		t.Fatal(root)
	}
	if res.Folders[1].Name != res.Folders[2].Name || res.Folders[1].ID == res.Folders[2].ID || res.Folders[1].Path != "课程" {
		t.Fatal("same names merged")
	}
	res, err = quark.List(context.Background(), c, quark.ListRequest{SchemaVersion: 1, AccountID: "test", ParentID: ptr("b")})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Complete || len(res.Folders) != 2 || len(res.Nodes) != 0 {
		t.Fatal("empty folder not represented")
	}
}

func TestQuarkShareTreeUsesActualParentChainAndStableVersions(t *testing.T) {
	c := tree()
	res, err := quark.List(context.Background(), c, quark.ListRequest{SchemaVersion: 1, AccountID: "test", ShareID: ptr("managed")})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 2 || !res.Complete {
		t.Fatal(res)
	}
	file := res.Nodes[1]
	if file.ID != "file" || file.Version != "v1" || file.RelativePath != "第二课/介绍.mp4" || file.SizeBytes == nil || *file.SizeBytes != "0" {
		t.Fatal(file)
	}
	if res.Folders[2].Path != "课程/第二课" || res.Folders[2].Name != "第二课" {
		t.Fatal(res.Folders)
	}
	_, err = quark.List(context.Background(), tree(), quark.ListRequest{SchemaVersion: 1, AccountID: "test", ShareID: ptr("managed"), ParentID: ptr("b")})
	if err == nil {
		t.Fatal("unmanaged subtree accepted")
	}
}

func TestQuarkTargetChangesNeverFallBackToRoot(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*cloud)
		code   string
	}{
		{"missing", func(c *cloud) { delete(c.nodes, "a") }, "RESOURCE_DELETED"},
		{"permission", func(c *cloud) { n := c.nodes["a"]; n.Writable = false; c.nodes["a"] = n }, "PERMISSION_DENIED"},
		{"moved", func(c *cloud) { n := c.nodes["a"]; n.ParentID = ptr("b"); n.Revision = "a2"; c.nodes["a"] = n }, "VERSION_CHANGED"},
		{"file", func(c *cloud) { n := c.nodes["a"]; n.Kind = "FILE"; c.nodes["a"] = n }, "INVALID_INPUT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := tree()
			tc.change(c)
			_, err := quark.ValidateTarget(context.Background(), c, "test", quark.Target{FolderID: "a", Revision: "a1"})
			var f *p.Fault
			if !errors.As(err, &f) || f.Code != tc.code {
				t.Fatalf("%v", err)
			}
		})
	}
	c := tree()
	target, err := quark.ValidateTarget(context.Background(), c, "test", quark.Target{FolderID: "a", Revision: "a1"})
	if err != nil || target.ID != "a" || target.Path != "课程" {
		t.Fatal(target, err)
	}
}

func TestQuarkRejectsTraversalChangesAndFakePathNames(t *testing.T) {
	c := tree()
	c.mutate = func(id string) { n := c.nodes[id]; n.Revision = "changed"; c.nodes[id] = n }
	res, err := quark.List(context.Background(), c, quark.ListRequest{SchemaVersion: 1, AccountID: "test"})
	if err == nil || res.Complete {
		t.Fatal("moving snapshot called complete")
	}
	c = tree()
	n := c.pages[c.root][0].Nodes[0]
	n.Name = "/root/child"
	c.pages[c.root][0].Nodes[0] = n
	_, err = quark.List(context.Background(), c, quark.ListRequest{SchemaVersion: 1, AccountID: "test"})
	if err == nil {
		t.Fatal("path disguised as name accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c = tree()
	_, err = quark.List(ctx, c, quark.ListRequest{SchemaVersion: 1, AccountID: "test"})
	if err == nil || len(c.calls) != 0 {
		t.Fatal("cancel ignored")
	}
}
