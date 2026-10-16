package runner

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
)

func accountTestOperation(kind, account, platform, action string) Operation {
	payload := map[string]any{"schemaVersion": 1, "accountId": account, "platform": platform}
	if kind == "AUTH_STEP" {
		payload["authSessionId"], payload["action"] = "auth-test", action
	}
	encoded, _ := json.Marshal(payload)
	envelopePlatform := platform
	if platform == "GOOGLE_DRIVE" {
		envelopePlatform = "BILIBILI"
	}
	return Operation{OperationType: kind, Platform: envelopePlatform, AccountRef: &account, Payload: encoded}
}

func accountValue(t *testing.T, value any) Account {
	t.Helper()
	return value.(map[string]any)["account"].(Account)
}

func sessionValue(t *testing.T, value any) AuthSession {
	t.Helper()
	return value.(map[string]any)["session"].(AuthSession)
}

func TestAccountWorkerChecksOnlyExplicitFixedAccount(t *testing.T) {
	for _, accountID := range []string{"bilibili-main", "telegram-main", "quark-main", "drive-main"} {
		t.Run(accountID, func(t *testing.T) {
			platform := accountPlatform(accountID)
			calls := 0
			w := AccountWorker{Accounts: map[string]AccountAdapter{accountID: {Platform: platform, Check: func(context.Context) (Account, error) {
				calls++
				return Account{ID: accountID, Platform: platform, State: "ACTIVE", Label: "provider-secret", Revision: "provider-secret", ReasonCode: pointer("provider-secret")}, nil
			}}}}
			w.Capabilities()
			w.RegistrationFacts()
			w.SupportsOperation(accountTestOperation("CHECK_ACCOUNT", accountID, platform, ""))
			w.SupportsPlatform(platform)
			if _, _, complete, err := w.Recover(accountTestOperation("CHECK_ACCOUNT", accountID, platform, "")); err != nil || complete || calls != 0 {
				t.Fatalf("ordinary inspection or replay checked account: calls=%d complete=%v err=%v", calls, complete, err)
			}
			value, files, err := w.Handle(context.Background(), accountTestOperation("CHECK_ACCOUNT", accountID, platform, ""))
			if err != nil || len(files) != 0 {
				t.Fatal(err, files)
			}
			account := accountValue(t, value)
			if calls != 1 || account.ID != accountID || account.Platform != platform || account.State != "ACTIVE" || account.LastCheckedAt == nil || account.Label != accountLabel(platform) || account.ReasonCode != nil || account.Revision != "" {
				t.Fatalf("invalid checked facts: calls=%d account=%+v", calls, account)
			}
		})
	}
}

func TestAccountWorkerAuthDispatchRetainsSessionIdentity(t *testing.T) {
	var calls []string
	callback := func(action string) func(context.Context, string) (AuthSession, error) {
		return func(_ context.Context, sessionID string) (AuthSession, error) {
			calls = append(calls, action+":"+sessionID)
			session := AuthSession{AuthSessionID: sessionID, AccountID: "bilibili-main", Platform: "BILIBILI", State: "PENDING", NextAction: "WAIT", Message: "provider-secret", Revision: "provider-secret", TaskID: "provider-secret"}
			if action == "BEGIN" {
				session.NextAction, session.URL = "SCAN_QR", pointer("https://passport.bilibili.com/h5-app/passport/login/scan?navhide=1&qrcode_key=synthetic-qr&from=main_web")
			}
			if action == "CANCEL" {
				session.State, session.NextAction = "CANCELLED", "NONE"
			}
			return session, nil
		}
	}
	w := AccountWorker{Accounts: map[string]AccountAdapter{"bilibili-main": {Begin: callback("BEGIN"), Poll: callback("POLL"), Cancel: callback("CANCEL")}}}
	for _, action := range []string{"BEGIN", "POLL", "CANCEL"} {
		value, _, err := w.Handle(context.Background(), accountTestOperation("AUTH_STEP", "bilibili-main", "BILIBILI", action))
		if err != nil {
			t.Fatal(err)
		}
		session := sessionValue(t, value)
		if session.AuthSessionID != "auth-test" || session.AccountID != "bilibili-main" || session.Message == "provider-secret" || session.TaskID != "" || session.Revision != "" {
			t.Fatalf("unexpected public session: %+v", session)
		}
	}
	if strings.Join(calls, ",") != "BEGIN:auth-test,POLL:auth-test,CANCEL:auth-test" {
		t.Fatal(calls)
	}
}

func TestAccountWorkerUnconfiguredFactsAndLocalSetup(t *testing.T) {
	w := AccountWorker{}
	for _, accountID := range []string{"bilibili-main", "telegram-main", "quark-main", "drive-main"} {
		platform := accountPlatform(accountID)
		value, _, err := w.Handle(context.Background(), accountTestOperation("CHECK_ACCOUNT", accountID, platform, ""))
		if err != nil {
			t.Fatal(err)
		}
		account := accountValue(t, value)
		if account.State != "AUTH_REQUIRED" || account.ReasonCode == nil || *account.ReasonCode != "ACCOUNT_NOT_CONFIGURED" || account.LastCheckedAt != nil {
			t.Fatalf("unconfigured account claimed a check: %+v", account)
		}
		for _, action := range []string{"BEGIN", "POLL", "CANCEL"} {
			value, _, err = w.Handle(context.Background(), accountTestOperation("AUTH_STEP", accountID, platform, action))
			if err != nil {
				t.Fatal(err)
			}
			session := sessionValue(t, value)
			if action == "CANCEL" {
				if session.State != "CANCELLED" || session.NextAction != "NONE" {
					t.Fatal(session)
				}
			} else if session.State != "NEEDS_ACTION" || session.NextAction != "LOCAL_SETUP" || session.Message == "" || session.URL != nil || session.ReasonCode == nil || *session.ReasonCode != "ACCOUNT_NOT_CONFIGURED" {
				t.Fatalf("missing honest local setup: %+v", session)
			}
		}
	}
}

func TestAccountWorkerDriveLocalSetupNamesTheConfiguredAuthorizationSource(t *testing.T) {
	for _, code := range []string{"AUTH_MANAGED_EXTERNALLY", "AUTH_CLIENT_REQUIRED"} {
		t.Run(code, func(t *testing.T) {
			w := AccountWorker{Accounts: map[string]AccountAdapter{"drive-main": {Begin: func(_ context.Context, id string) (AuthSession, error) {
				return AuthSession{AuthSessionID: id, AccountID: "drive-main", Platform: "GOOGLE_DRIVE", State: "NEEDS_ACTION", NextAction: "LOCAL_SETUP", ReasonCode: pointer(code)}, nil
			}}}}
			value, _, err := w.Handle(context.Background(), accountTestOperation("AUTH_STEP", "drive-main", "GOOGLE_DRIVE", "BEGIN"))
			if err != nil {
				t.Fatal(err)
			}
			session := sessionValue(t, value)
			if session.ReasonCode == nil || *session.ReasonCode != code {
				t.Fatal("local setup reason changed")
			}
			if code == "AUTH_MANAGED_EXTERNALLY" {
				for _, expected := range []string{"Y7000P", "指定 rclone 配置", "gdrive", "主动检查账号"} {
					if !strings.Contains(session.Message, expected) {
						t.Fatal("managed authorization setup omitted its configured source or next action")
					}
				}
				if strings.Contains(session.Message, "Desktop OAuth client") {
					t.Fatal("managed authorization incorrectly requested a new OAuth client")
				}
			} else if !strings.Contains(session.Message, "Desktop OAuth client") {
				t.Fatal("explicit Desktop OAuth setup guidance changed")
			}
		})
	}
}

func TestAccountWorkerRejectsInvalidOperationBeforeCallingAdapter(t *testing.T) {
	calls := 0
	w := AccountWorker{Accounts: map[string]AccountAdapter{"bilibili-main": {Check: func(context.Context) (Account, error) {
		calls++
		return Account{}, nil
	}}}}
	base := func() Operation { return accountTestOperation("CHECK_ACCOUNT", "bilibili-main", "BILIBILI", "") }
	tests := map[string]func() Operation{
		"missing account ref":     func() Operation { op := base(); op.AccountRef = nil; return op },
		"wrong account ref":       func() Operation { op := base(); op.AccountRef = pointer("telegram-main"); return op },
		"second Bilibili account": func() Operation { return accountTestOperation("CHECK_ACCOUNT", "bilibili-secondary", "BILIBILI", "") },
		"wrong fixed platform":    func() Operation { return accountTestOperation("CHECK_ACCOUNT", "bilibili-main", "TELEGRAM", "") },
		"wrong envelope":          func() Operation { op := base(); op.Platform = "TELEGRAM"; return op },
		"Google envelope": func() Operation {
			op := accountTestOperation("CHECK_ACCOUNT", "drive-main", "GOOGLE_DRIVE", "")
			op.Platform = "GOOGLE_DRIVE"
			return op
		},
		"destination": func() Operation { op := base(); op.Destination = &Destination{}; return op },
		"schema version": func() Operation {
			op := base()
			op.Payload = []byte(`{"schemaVersion":2,"accountId":"bilibili-main","platform":"BILIBILI"}`)
			return op
		},
		"unknown payload field": func() Operation {
			op := base()
			op.Payload = []byte(`{"schemaVersion":1,"accountId":"bilibili-main","platform":"BILIBILI","cookie":"synthetic-secret"}`)
			return op
		},
		"trailing JSON": func() Operation { op := base(); op.Payload = append(op.Payload, []byte(` {}`)...); return op },
		"missing auth session": func() Operation {
			op := accountTestOperation("AUTH_STEP", "bilibili-main", "BILIBILI", "BEGIN")
			op.Payload = []byte(`{"schemaVersion":1,"accountId":"bilibili-main","platform":"BILIBILI","action":"BEGIN"}`)
			return op
		},
		"unknown auth action": func() Operation { return accountTestOperation("AUTH_STEP", "bilibili-main", "BILIBILI", "LOGIN_AGAIN") },
	}
	for name, makeOperation := range tests {
		t.Run(name, func(t *testing.T) {
			if _, _, err := w.Handle(context.Background(), makeOperation()); err == nil || media.Classify(err, "").Code != "INVALID_PAYLOAD" {
				t.Fatalf("accepted invalid operation: %v", err)
			}
		})
	}
	if calls != 0 {
		t.Fatal("invalid operation reached account adapter")
	}
}

func TestAccountWorkerRejectsReturnedIdentityChanges(t *testing.T) {
	for _, changed := range []string{"account", "platform", "session"} {
		t.Run(changed, func(t *testing.T) {
			w := AccountWorker{Accounts: map[string]AccountAdapter{"bilibili-main": {Check: func(context.Context) (Account, error) {
				account := Account{ID: "bilibili-main", Platform: "BILIBILI", State: "ACTIVE"}
				if changed == "account" {
					account.ID = "bilibili-secondary"
				} else {
					account.Platform = "TELEGRAM"
				}
				return account, nil
			}, Begin: func(context.Context, string) (AuthSession, error) {
				session := AuthSession{AuthSessionID: "auth-test", AccountID: "bilibili-main", Platform: "BILIBILI", State: "PENDING", NextAction: "WAIT"}
				switch changed {
				case "account":
					session.AccountID = "bilibili-secondary"
				case "platform":
					session.Platform = "TELEGRAM"
				default:
					session.AuthSessionID = "different-session"
				}
				return session, nil
			}}}}
			if _, _, err := w.Handle(context.Background(), accountTestOperation("CHECK_ACCOUNT", "bilibili-main", "BILIBILI", "")); err == nil || media.Classify(err, "").Code != "ACCOUNT_IDENTITY_CHANGED" {
				t.Fatalf("accepted changed account: %v", err)
			}
			if _, _, err := w.Handle(context.Background(), accountTestOperation("AUTH_STEP", "bilibili-main", "BILIBILI", "BEGIN")); err == nil || media.Classify(err, "").Code != "AUTH_SESSION_MISMATCH" {
				t.Fatalf("accepted changed session: %v", err)
			}
		})
	}
}

func TestAccountWorkerOnlyReturnsOfficialCredentialFreeAuthorizationURLs(t *testing.T) {
	for _, test := range []struct {
		platform, action, url string
		valid                 bool
	}{
		{"BILIBILI", "SCAN_QR", "https://passport.bilibili.com/h5-app/passport/login/scan?qrcode_key=synthetic", true},
		{"BILIBILI", "SCAN_QR", "https://account.bilibili.com/h5/account-h5/auth/scan-web?navhide=1&callback=close&from=&qrcode_key=synthetic", true},
		{"BILIBILI", "SCAN_QR", "https://account.bilibili.com/h5/account-h5/auth/scan-web?navhide=1&callback=https%3A%2F%2Fevil.test&from=&qrcode_key=synthetic", false},
		{"BILIBILI", "SCAN_QR", "https://account.bilibili.com/h5/account-h5/auth/scan-web?navhide=1&callback=close&from=&qrcode_key=one&qrcode_key=two", false},
		{"BILIBILI", "SCAN_QR", "https://account.bilibili.com/h5/account-h5/auth/scan-web?navhide=1&callback=close&from=&qrcode_key=", false},
		{"BILIBILI", "SCAN_QR", "https://account.bilibili.com/other?navhide=1&callback=close&from=&qrcode_key=synthetic", false},
		{"GOOGLE_DRIVE", "OPEN_URL", "https://accounts.google.com/o/oauth2/v2/auth?client_id=synthetic&redirect_uri=http%3A%2F%2F127.0.0.1%3A4000&response_type=code&scope=drive.file&state=synthetic", true},
		{"BILIBILI", "SCAN_QR", "http://passport.bilibili.com/login", false},
		{"BILIBILI", "SCAN_QR", "https://passport.bilibili.com.evil.test/login", false},
		{"BILIBILI", "SCAN_QR", "https://secret@passport.bilibili.com/login", false},
		{"BILIBILI", "SCAN_QR", "https://passport.bilibili.com/login#", false},
		{"BILIBILI", "SCAN_QR", "https://passport.bilibili.com:8443/login", false},
		{"BILIBILI", "SCAN_QR", "https://passport.bilibili.com/login?access_token=synthetic-secret", false},
		{"GOOGLE_DRIVE", "OPEN_URL", "https://accounts.google.com/o/oauth2/v2/auth?code=synthetic-secret", false},
		{"GOOGLE_DRIVE", "OPEN_URL", "https://accounts.google.com/o/oauth2/v2/auth?client_secret=synthetic-secret", false},
		{"TELEGRAM", "SCAN_QR", "https://passport.bilibili.com/login", false},
		{"QUARK", "OPEN_URL", "https://accounts.google.com/o/oauth2/v2/auth", false},
	} {
		t.Run(test.url+test.platform, func(t *testing.T) {
			accountID := "bilibili-main"
			switch test.platform {
			case "GOOGLE_DRIVE":
				accountID = "drive-main"
			case "TELEGRAM":
				accountID = "telegram-main"
			case "QUARK":
				accountID = "quark-main"
			}
			w := AccountWorker{Accounts: map[string]AccountAdapter{accountID: {Begin: func(_ context.Context, id string) (AuthSession, error) {
				return AuthSession{AuthSessionID: id, AccountID: accountID, Platform: test.platform, State: "PENDING", NextAction: test.action, URL: &test.url}, nil
			}}}}
			_, _, err := w.Handle(context.Background(), accountTestOperation("AUTH_STEP", accountID, test.platform, "BEGIN"))
			if test.valid != (err == nil) {
				t.Fatalf("valid=%v err=%v", test.valid, err)
			}
		})
	}
}

func TestAccountWorkerSanitizesProviderFailuresAndPublicText(t *testing.T) {
	for _, providerErr := range []error{errors.New("cookie=synthetic-secret"), media.Fail("cookie=synthetic-secret", "synthetic-secret", true), media.Fail("AUTH_REQUIRED", "synthetic-secret", false)} {
		w := AccountWorker{Accounts: map[string]AccountAdapter{"bilibili-main": {Check: func(context.Context) (Account, error) { return Account{}, providerErr }, Begin: func(context.Context, string) (AuthSession, error) { return AuthSession{}, providerErr }}}}
		value, _, err := w.Handle(context.Background(), accountTestOperation("CHECK_ACCOUNT", "bilibili-main", "BILIBILI", ""))
		if err != nil {
			t.Fatal(err)
		}
		account := accountValue(t, value)
		encoded, _ := json.Marshal(value)
		if account.State == "ACTIVE" || account.ReasonCode == nil || strings.Contains(string(encoded), "synthetic-secret") {
			t.Fatalf("unsafe account failure: %s", encoded)
		}
		_, _, err = w.Handle(context.Background(), accountTestOperation("AUTH_STEP", "bilibili-main", "BILIBILI", "BEGIN"))
		if err == nil || strings.Contains(err.Error(), "synthetic-secret") {
			t.Fatalf("unsafe auth failure: %v", err)
		}
	}
	w := AccountWorker{Accounts: map[string]AccountAdapter{"telegram-main": {LocalSetupMessage: "请在 Y7000P 的受控入口创建新 Telegram 会话。", Begin: func(_ context.Context, id string) (AuthSession, error) {
		return AuthSession{AuthSessionID: id, AccountID: "telegram-main", Platform: "TELEGRAM", State: "NEEDS_ACTION", NextAction: "LOCAL_SETUP", Message: "cookie=synthetic-secret", ReasonCode: pointer("synthetic-secret")}, nil
	}}}}
	value, _, err := w.Handle(context.Background(), accountTestOperation("AUTH_STEP", "telegram-main", "TELEGRAM", "BEGIN"))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(value)
	if strings.Contains(string(encoded), "synthetic-secret") || sessionValue(t, value).Message != "请在 Y7000P 的受控入口创建新 Telegram 会话。" {
		t.Fatalf("unsafe public callback text: %s", encoded)
	}
}

func TestAccountWorkerPreservesControlledLoginFailureCategories(t *testing.T) {
	for _, code := range []string{"PLATFORM_RESPONSE_UNKNOWN", "NETWORK_ERROR", "RESOURCE_UNAVAILABLE", "SESSION_WRITE_FAILED", "LOGIN_IN_PROGRESS"} {
		t.Run(code, func(t *testing.T) {
			w := AccountWorker{Accounts: map[string]AccountAdapter{"bilibili-main": {Begin: func(context.Context, string) (AuthSession, error) {
				return AuthSession{}, media.Fail(code, "BILIBILI_SESSION", code == "NETWORK_ERROR")
			}}}}
			_, _, err := w.Handle(context.Background(), accountTestOperation("AUTH_STEP", "bilibili-main", "BILIBILI", "BEGIN"))
			failure := media.Classify(err, "ACCOUNT")
			if failure.Code != code || failure.Retryable != (code == "NETWORK_ERROR") {
				t.Fatalf("lost controlled login failure: %#v", failure)
			}
		})
	}
}

type accountNextWorker struct{ handles, recoveries int }

func (*accountNextWorker) Capabilities() []string                { return []string{"DOWNLOAD_MEDIA", "AUTH_STEP"} }
func (*accountNextWorker) SupportsPlatform(platform string) bool { return platform == "CUSTOM" }
func (*accountNextWorker) SupportsOperation(op Operation) bool   { return op.OperationType == "NEXT" }
func (*accountNextWorker) RegistrationFacts() map[string]any {
	return map[string]any{"directoryPicker": "existing-fact"}
}
func (w *accountNextWorker) Handle(context.Context, Operation) (any, []File, error) {
	w.handles++
	return "handled", []File{{OutputID: "existing-file"}}, nil
}
func (w *accountNextWorker) Recover(Operation) (any, []File, bool, error) {
	w.recoveries++
	return "recovered", []File{{OutputID: "existing-file"}}, true, nil
}

func TestAccountWorkerPreservesNextDispatchFactsAndRecovery(t *testing.T) {
	next := &accountNextWorker{}
	w := AccountWorker{Next: next}
	if strings.Join(w.Capabilities(), ",") != "DOWNLOAD_MEDIA,AUTH_STEP,CHECK_ACCOUNT" || !w.SupportsOperation(Operation{OperationType: "NEXT"}) || w.SupportsOperation(Operation{OperationType: "UNSUPPORTED", Platform: "CUSTOM"}) || !w.SupportsPlatform("CUSTOM") || w.RegistrationFacts()["directoryPicker"] != "existing-fact" {
		t.Fatal("next capabilities, dispatch or facts were lost")
	}
	value, files, err := w.Handle(context.Background(), Operation{OperationType: "NEXT"})
	if err != nil || value != "handled" || len(files) != 1 || next.handles != 1 {
		t.Fatal(value, files, err)
	}
	value, files, complete, err := w.Recover(Operation{OperationType: "NEXT"})
	if err != nil || value != "recovered" || len(files) != 1 || !complete || next.recoveries != 1 {
		t.Fatal(value, files, complete, err)
	}
	if _, _, complete, err := w.Recover(accountTestOperation("AUTH_STEP", "bilibili-main", "BILIBILI", "BEGIN")); err != nil || complete || next.recoveries != 1 {
		t.Fatal("auth recovery delegated or started another flow")
	}
}

func TestAccountWorkerCancellationDoesNotStartLogin(t *testing.T) {
	calls := 0
	w := AccountWorker{Accounts: map[string]AccountAdapter{"bilibili-main": {Begin: func(ctx context.Context, id string) (AuthSession, error) {
		calls++
		return AuthSession{}, ctx.Err()
	}}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := w.Handle(ctx, accountTestOperation("AUTH_STEP", "bilibili-main", "BILIBILI", "BEGIN")); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("cancelled auth dispatched: calls=%d err=%v", calls, err)
	}
}
