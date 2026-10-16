package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/drive"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/bilibili"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/quark"
	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/runner"
)

func TestFreshRuntimeCanStartAndOfferHonestLoginSetup(t *testing.T) {
	root := t.TempDir()
	bil, err := bilibili.New(bilibili.Config{CookieFile: filepath.Join(root, "bilibili.session")})
	if err != nil {
		t.Fatal(err)
	}
	google, err := drive.NewGoogle(drive.GoogleConfig{AccountID: "drive-main", TokenFile: filepath.Join(root, "drive.session"), ClientFile: filepath.Join(root, "new-client.json")})
	if err != nil {
		t.Fatal(err)
	}
	transfers := &quark.Transfers{Clients: map[string]*quark.NativeClient{"quark-main": {AccountID: "quark-main", ExpectedUserID: "1", Cookie: func() (string, error) { return "", nil }}}}
	worker := &runner.AccountWorker{Accounts: accountAdapters(bil, map[string]*drive.Client{"drive-main": google}, transfers, nil)}
	for _, account := range []struct{ id, platform string }{{"bilibili-main", "BILIBILI"}, {"drive-main", "GOOGLE_DRIVE"}, {"telegram-main", "TELEGRAM"}, {"quark-main", "QUARK"}} {
		payload, _ := json.Marshal(map[string]any{"schemaVersion": 1, "accountId": account.id, "platform": account.platform})
		envelopePlatform := account.platform
		if envelopePlatform == "GOOGLE_DRIVE" {
			envelopePlatform = "BILIBILI"
		}
		value, _, err := worker.Handle(context.Background(), runner.Operation{OperationType: "CHECK_ACCOUNT", Platform: envelopePlatform, AccountRef: &account.id, Payload: payload})
		if err != nil {
			t.Fatalf("%s: %v", account.id, err)
		}
		fact := value.(map[string]any)["account"].(runner.Account)
		if fact.ID != account.id || fact.State != "AUTH_REQUIRED" {
			t.Fatalf("fresh runtime fabricated a login: %#v", fact)
		}
		if account.id == "quark-main" && (fact.ReasonCode == nil || *fact.ReasonCode != "AUTH_REQUIRED") {
			t.Fatalf("native platform authentication failure was erased: %#v", fact)
		}
	}
	id := "drive-main"
	payload, _ := json.Marshal(map[string]any{"schemaVersion": 1, "accountId": id, "platform": "GOOGLE_DRIVE", "authSessionId": "new-login", "action": "BEGIN"})
	value, _, err := worker.Handle(context.Background(), runner.Operation{OperationType: "AUTH_STEP", Platform: "BILIBILI", AccountRef: &id, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	flow := value.(map[string]any)["session"].(runner.AuthSession)
	if flow.State != "NEEDS_ACTION" || flow.NextAction != "LOCAL_SETUP" || flow.URL != nil || flow.ReasonCode == nil || *flow.ReasonCode != "AUTH_CLIENT_REQUIRED" {
		t.Fatalf("missing new Google client must provide setup, not a fabricated consent URL: %#v", flow)
	}
	// A process restart has no live QR/listener. Polling must not secretly BEGIN.
	bilAdapter := worker.Accounts["bilibili-main"]
	missing, err := bilAdapter.Poll(context.Background(), "flow-from-old-process")
	if err != nil || missing.State != "NEEDS_ACTION" || missing.ReasonCode == nil || *missing.ReasonCode != "AUTH_SESSION_NOT_FOUND" {
		t.Fatalf("lost QR flow: %#v, %v", missing, err)
	}
	missing, err = bilAdapter.Cancel(context.Background(), "flow-from-old-process")
	if err != nil || missing.State != "CANCELLED" {
		t.Fatalf("cancel absent flow: %#v, %v", missing, err)
	}
}
