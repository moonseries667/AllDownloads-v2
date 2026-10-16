package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/gotd/td/session"
	gotd "github.com/gotd/td/telegram"
	"github.com/gotd/td/tgerr"
	p "github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/telegram"
)

// tdl 0.20.4's official local extension protocol passes a private temporary
// file in TDL_EXTENSION, never credentials in argv. This program reads it only
// internally. See cmd/extension.go and extension/extension.go in that tag.
type extensionEnvironment struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	AppID     int    `json:"app_id"`
	AppHash   string `json:"app_hash"`
	Session   []byte `json:"session"`
	Proxy     string `json:"proxy"`
	Debug     bool   `json:"debug"`
}

func runIdentityExtension(ctx context.Context, output io.Writer) error {
	f, err := os.Open(os.Getenv("TDL_EXTENSION"))
	if err != nil {
		return p.NewFault("AUTH_REQUIRED", "IDENTITY")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	defer clear(raw)
	if err != nil || len(raw) > 1<<20 {
		return p.NewFault("AUTH_REQUIRED", "IDENTITY")
	}
	var env extensionEnvironment
	if json.Unmarshal(raw, &env) != nil || env.Name != telegram.IdentityExtension || env.Namespace != "alldownloads-v2" || env.AppID <= 0 || env.AppHash == "" || len(env.Session) == 0 || env.Debug || env.Proxy != "" {
		return p.NewFault("AUTH_REQUIRED", "IDENTITY")
	}
	defer clear(env.Session)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	storage := &session.StorageMemory{}
	if storage.StoreSession(ctx, env.Session) != nil {
		return p.NewFault("AUTH_REQUIRED", "IDENTITY")
	}
	client := gotd.NewClient(env.AppID, env.AppHash, gotd.Options{SessionStorage: storage, NoUpdates: true})
	var selfID string
	err = client.Run(ctx, func(ctx context.Context) error {
		self, err := client.Self(ctx)
		if err != nil {
			return err
		}
		if self.ID <= 0 || self.Deleted || self.Bot {
			return p.NewFault("AUTH_REQUIRED", "IDENTITY")
		}
		selfID = strconv.FormatInt(self.ID, 10)
		return nil
	})
	if err != nil {
		if ctx.Err() != nil {
			return p.SafeFault(ctx.Err(), "IDENTITY")
		}
		var fault *p.Fault
		if errors.As(err, &fault) {
			return fault
		}
		if tgerr.Is(err, "AUTH_KEY_UNREGISTERED", "SESSION_REVOKED", "SESSION_EXPIRED", "USER_DEACTIVATED", "AUTH_KEY_DUPLICATED") {
			return p.NewFault("AUTH_REQUIRED", "IDENTITY")
		}
		return p.NewFault("NETWORK_TEMPORARY", "IDENTITY")
	}
	return json.NewEncoder(output).Encode(struct {
		SelfID string `json:"selfId"`
	}{selfID})
}
