package main

import (
	"context"
	"sync"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/drive"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/bilibili"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/quark"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/telegram"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/runner"
)

// Only the user's explicit account operations invoke these callbacks. Creating
// the worker or registering capabilities never checks a platform or logs in.
func accountAdapters(bil *bilibili.Session, drives map[string]*drive.Client, transfers *quark.Transfers, tel *telegram.TDL) map[string]runner.AccountAdapter {
	adapters := map[string]runner.AccountAdapter{}
	if bil != nil {
		flows := newFlowLinks()
		adapters["bilibili-main"] = runner.AccountAdapter{Platform: "BILIBILI",
			Check: func(ctx context.Context) (runner.Account, error) {
				fact, err := bil.Check(ctx)
				state := "AUTH_REQUIRED"
				if fact.State == "READY" {
					state = "ACTIVE"
				}
				return runner.Account{ID: "bilibili-main", Platform: "BILIBILI", State: state}, err
			},
			Begin: func(ctx context.Context, id string) (runner.AuthSession, error) {
				if flow, ok := flows.get(id); ok {
					value, err := bil.PollQR(ctx, flow)
					return bilAuth(id, value, err)
				}
				value, err := bil.BeginQR(ctx)
				if err == nil {
					flows.put(id, value.ID)
				}
				return bilAuth(id, value, err)
			},
			Poll: func(ctx context.Context, id string) (runner.AuthSession, error) {
				flow, ok := flows.get(id)
				if !ok {
					return authSetup(id, "bilibili-main", "BILIBILI", "AUTH_SESSION_NOT_FOUND"), nil
				}
				value, err := bil.PollQR(ctx, flow)
				return bilAuth(id, value, err)
			},
			Cancel: func(ctx context.Context, id string) (runner.AuthSession, error) {
				flow, ok := flows.get(id)
				if !ok {
					return authCancelled(id, "bilibili-main", "BILIBILI"), nil
				}
				_, err := bil.CancelQR(flow)
				return cancelledAuthResult(id, "bilibili-main", "BILIBILI", err)
			},
		}
	}
	if client := drives["drive-main"]; client != nil {
		flows := newFlowLinks()
		adapters["drive-main"] = runner.AccountAdapter{Platform: "GOOGLE_DRIVE",
			Check: func(ctx context.Context) (runner.Account, error) {
				return runner.Account{ID: "drive-main", Platform: "GOOGLE_DRIVE", State: "ACTIVE"}, client.Check(ctx)
			},
			Begin: func(ctx context.Context, id string) (runner.AuthSession, error) {
				if flow, ok := flows.get(id); ok {
					value, err := client.PollOAuth(flow)
					return driveAuth(id, value, err)
				}
				value, err := client.StartOAuth(ctx)
				if err == nil {
					flows.put(id, value.ID)
				}
				return driveAuth(id, value, err)
			},
			Poll: func(ctx context.Context, id string) (runner.AuthSession, error) {
				flow, ok := flows.get(id)
				if !ok {
					return authSetup(id, "drive-main", "GOOGLE_DRIVE", "AUTH_SESSION_NOT_FOUND"), nil
				}
				value, err := client.PollOAuth(flow)
				return driveAuth(id, value, err)
			},
			Cancel: func(ctx context.Context, id string) (runner.AuthSession, error) {
				flow, ok := flows.get(id)
				if !ok {
					return authCancelled(id, "drive-main", "GOOGLE_DRIVE"), nil
				}
				_, err := client.CancelOAuth(flow)
				return cancelledAuthResult(id, "drive-main", "GOOGLE_DRIVE", err)
			},
		}
	}
	if transfers != nil {
		if client := transfers.Clients["quark-main"]; client != nil {
			adapters["quark-main"] = runner.AccountAdapter{Platform: "QUARK", Check: func(ctx context.Context) (runner.Account, error) {
				_, err := client.Identity(ctx)
				return runner.Account{ID: "quark-main", Platform: "QUARK", State: "ACTIVE"}, err
			}}
		}
	}
	if tel != nil {
		if _, configured := tel.Accounts["telegram-main"]; configured {
			adapters["telegram-main"] = runner.AccountAdapter{Platform: "TELEGRAM", Check: func(ctx context.Context) (runner.Account, error) {
				_, err := tel.CheckSession(ctx, "telegram-main")
				return runner.Account{ID: "telegram-main", Platform: "TELEGRAM", State: "ACTIVE"}, err
			}}
		}
	}
	return adapters
}

// Provider flow handles live only as long as the process that owns the QR or
// loopback listener. A restart requires a new explicit flow, never another BEGIN
// hidden behind polling an old session. Entries expire without holding secrets.
type flowLink struct {
	id      string
	expires time.Time
}
type flowLinks struct {
	mu  sync.Mutex
	ids map[string]flowLink
}

func newFlowLinks() *flowLinks { return &flowLinks{ids: map[string]flowLink{}} }
func (f *flowLinks) get(id string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	link, ok := f.ids[id]
	if !ok || time.Now().After(link.expires) {
		delete(f.ids, id)
		return "", false
	}
	return link.id, true
}
func (f *flowLinks) put(id, providerID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for key, link := range f.ids {
		if time.Now().After(link.expires) {
			delete(f.ids, key)
		}
	}
	f.ids[id] = flowLink{providerID, time.Now().Add(15 * time.Minute)}
}
func authSetup(id, account, platform, code string) runner.AuthSession {
	return runner.AuthSession{AuthSessionID: id, AccountID: account, Platform: platform, State: "NEEDS_ACTION", NextAction: "LOCAL_SETUP", ReasonCode: &code}
}
func authCancelled(id, account, platform string) runner.AuthSession {
	return runner.AuthSession{AuthSessionID: id, AccountID: account, Platform: platform, State: "CANCELLED", NextAction: "NONE"}
}
func cancelledAuthResult(id, account, platform string, err error) (runner.AuthSession, error) {
	// Cancelling the UI flow never deletes an already committed account login.
	// A provider may report that login won the race; Java's cancellation intent
	// must still remain terminal and the cleanup operation must be acknowledged.
	if err != nil {
		code := media.Classify(err, "AUTH").Code
		if code != "AUTH_FLOW_NOT_FOUND" && code != "AUTH_SESSION_NOT_FOUND" {
			return runner.AuthSession{}, err
		}
	}
	return authCancelled(id, account, platform), nil
}
func authError(id, account, platform string, err error) (runner.AuthSession, error) {
	code := media.Classify(err, "AUTH").Code
	if code == "AUTH_FLOW_NOT_FOUND" {
		code = "AUTH_SESSION_NOT_FOUND"
	}
	switch code {
	case "AUTH_CLIENT_REQUIRED", "AUTH_CONFIG_INVALID", "AUTH_SCOPE_REQUIRED", "AUTH_SESSION_NOT_FOUND", "AUTH_REQUIRED", "ACCOUNT_IDENTITY_CHANGED", "AUTH_STORE_INVALID", "AUTH_STORE_UNAVAILABLE":
		return authSetup(id, account, platform, code), nil
	default:
		return runner.AuthSession{}, err
	}
}
func bilAuth(id string, value bilibili.QRLogin, err error) (runner.AuthSession, error) {
	if err != nil {
		return authError(id, "bilibili-main", "BILIBILI", err)
	}
	result := runner.AuthSession{AuthSessionID: id, AccountID: "bilibili-main", Platform: "BILIBILI", ExpiresAt: &value.ExpiresAt}
	switch value.State {
	case "WAITING_SCAN":
		result.State, result.NextAction, result.URL = "PENDING", "SCAN_QR", &value.URL
	case "STARTING", "WAITING_CONFIRM":
		result.State, result.NextAction = "PENDING", "WAIT"
	case "READY":
		result.State, result.NextAction = "SUCCEEDED", "NONE"
	case "CANCELLED", "EXPIRED":
		result.State, result.NextAction = value.State, "NONE"
	default:
		result = authSetup(id, "bilibili-main", "BILIBILI", "AUTH_REQUIRED")
	}
	return result, nil
}
func driveAuth(id string, value drive.OAuthSession, err error) (runner.AuthSession, error) {
	if err != nil {
		return authError(id, "drive-main", "GOOGLE_DRIVE", err)
	}
	result := runner.AuthSession{AuthSessionID: id, AccountID: "drive-main", Platform: "GOOGLE_DRIVE", ExpiresAt: &value.ExpiresAt}
	switch value.State {
	case "PENDING":
		result.State, result.NextAction, result.URL = "PENDING", "OPEN_URL", &value.AuthorizationURL
	case "SUCCEEDED", "CANCELLED", "EXPIRED":
		result.State, result.NextAction = value.State, "NONE"
	default:
		code := value.ReasonCode
		if code == "" {
			code = "AUTH_REQUIRED"
		}
		result = authSetup(id, "drive-main", "GOOGLE_DRIVE", code)
	}
	return result, nil
}
