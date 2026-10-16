package quark

import "context"

// CheckSession checks the real bound identity without listing drive contents,
// saving shares, downloading files, or modifying the cookie store.
func (c *NativeClient) CheckSession(ctx context.Context) (string, error) {
	return c.Identity(ctx)
}
