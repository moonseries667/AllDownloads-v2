package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/media"
)

// Account contains public account facts, never provider credentials or sessions.
type Account struct {
	ID            string     `json:"id"`
	Platform      string     `json:"platform"`
	Label         string     `json:"label"`
	State         string     `json:"state"`
	LastCheckedAt *time.Time `json:"lastCheckedAt"`
	ReasonCode    *string    `json:"reasonCode"`
	Revision      string     `json:"revision,omitempty"`
}

type AuthSession struct {
	AuthSessionID string     `json:"authSessionId"`
	AccountID     string     `json:"accountId"`
	Platform      string     `json:"platform"`
	State         string     `json:"state"`
	NextAction    string     `json:"nextAction"`
	URL           *string    `json:"url"`
	Message       string     `json:"message"`
	ExpiresAt     *time.Time `json:"expiresAt"`
	Revision      string     `json:"revision,omitempty"`
	TaskID        string     `json:"taskId,omitempty"`
	ReasonCode    *string    `json:"reasonCode"`
}

// AccountAdapter callbacks run only for an explicitly acquired account operation.
// LocalSetupMessage is an application-authored instruction, not provider output.
type AccountAdapter struct {
	Platform          string
	LocalSetupMessage string
	Check             func(context.Context) (Account, error)
	Begin             func(context.Context, string) (AuthSession, error)
	Poll              func(context.Context, string) (AuthSession, error)
	Cancel            func(context.Context, string) (AuthSession, error)
}

type AccountWorker struct {
	Next     Worker
	Accounts map[string]AccountAdapter
}

func (w *AccountWorker) Capabilities() []string {
	caps := []string{}
	if w.Next != nil {
		caps = append(caps, w.Next.Capabilities()...)
	}
	for _, capability := range []string{"CHECK_ACCOUNT", "AUTH_STEP"} {
		found := false
		for _, existing := range caps {
			found = found || existing == capability
		}
		if !found {
			caps = append(caps, capability)
		}
	}
	return caps
}

func (w *AccountWorker) SupportsPlatform(platform string) bool {
	if validPlatform(platform) {
		return true
	}
	if next, ok := w.Next.(interface{ SupportsPlatform(string) bool }); ok {
		return next.SupportsPlatform(platform)
	}
	return false
}

func (w *AccountWorker) SupportsOperation(op Operation) bool {
	if accountOperation(op) {
		return validPlatform(op.Platform)
	}
	if next, ok := w.Next.(interface{ SupportsOperation(Operation) bool }); ok {
		return next.SupportsOperation(op)
	}
	if next, ok := w.Next.(interface{ SupportsPlatform(string) bool }); ok {
		return next.SupportsPlatform(op.Platform)
	}
	return w.Next != nil && op.Platform == "BILIBILI"
}

func (w *AccountWorker) RegistrationFacts() map[string]any {
	if next, ok := w.Next.(interface{ RegistrationFacts() map[string]any }); ok {
		return next.RegistrationFacts()
	}
	return nil
}

func (w *AccountWorker) Recover(op Operation) (any, []File, bool, error) {
	// An interrupted auth operation does not authorize another login or poll.
	if accountOperation(op) {
		return nil, nil, false, nil
	}
	if next, ok := w.Next.(interface {
		Recover(Operation) (any, []File, bool, error)
	}); ok {
		return next.Recover(op)
	}
	return nil, nil, false, nil
}

type accountPayload struct {
	SchemaVersion int    `json:"schemaVersion"`
	AccountID     string `json:"accountId"`
	Platform      string `json:"platform"`
}

func accountOperation(op Operation) bool {
	return op.OperationType == "CHECK_ACCOUNT" || op.OperationType == "AUTH_STEP"
}

func decodeAccountPayload(raw json.RawMessage, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("invalid account payload")
	}
	return nil
}

func accountPlatform(id string) string {
	switch id {
	case "bilibili-main":
		return "BILIBILI"
	case "telegram-main":
		return "TELEGRAM"
	case "quark-main":
		return "QUARK"
	case "drive-main":
		return "GOOGLE_DRIVE"
	}
	return ""
}

func accountLabel(platform string) string {
	switch platform {
	case "BILIBILI":
		return "Bilibili 主账号"
	case "TELEGRAM":
		return "Telegram 主账号"
	case "QUARK":
		return "夸克主账号"
	default:
		return "Google Drive 主账号"
	}
}

func (w *AccountWorker) accountAdapter(op Operation, payload accountPayload) (AccountAdapter, error) {
	envelopePlatform := payload.Platform
	if envelopePlatform == "GOOGLE_DRIVE" {
		envelopePlatform = "BILIBILI"
	}
	if payload.SchemaVersion != 1 || accountPlatform(payload.AccountID) != payload.Platform || payload.Platform == "" || op.Platform != envelopePlatform || op.Destination != nil || op.AccountRef == nil || *op.AccountRef != payload.AccountID {
		return AccountAdapter{}, media.Fail("INVALID_PAYLOAD", "ACCOUNT", false)
	}
	adapter := w.Accounts[payload.AccountID]
	if adapter.Platform != "" && adapter.Platform != payload.Platform {
		return AccountAdapter{}, media.Fail("ACCOUNT_IDENTITY_CHANGED", "ACCOUNT", false)
	}
	return adapter, nil
}

func (w *AccountWorker) Handle(ctx context.Context, op Operation) (any, []File, error) {
	if !accountOperation(op) {
		if w.Next == nil {
			return nil, nil, media.Fail("UNSUPPORTED_OPERATION", "EXECUTE", false)
		}
		return w.Next.Handle(ctx, op)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if op.OperationType == "CHECK_ACCOUNT" {
		var payload accountPayload
		if decodeAccountPayload(op.Payload, &payload) != nil {
			return nil, nil, media.Fail("INVALID_PAYLOAD", "ACCOUNT", false)
		}
		adapter, err := w.accountAdapter(op, payload)
		if err != nil {
			return nil, nil, err
		}
		account := Account{ID: payload.AccountID, Platform: payload.Platform, State: "AUTH_REQUIRED", ReasonCode: pointer("ACCOUNT_NOT_CONFIGURED")}
		if adapter.Check != nil {
			account, err = adapter.Check(ctx)
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			if err != nil {
				failure := accountFailure(err)
				account = Account{ID: payload.AccountID, Platform: payload.Platform, State: "ERROR", ReasonCode: pointer(failure.Code)}
				if failure.Code == "AUTH_REQUIRED" || failure.Code == "ACCOUNT_NOT_CONFIGURED" {
					account.State = "AUTH_REQUIRED"
				}
			}
			checked := time.Now().UTC()
			account.LastCheckedAt = &checked
		}
		if account.ID != payload.AccountID || account.Platform != payload.Platform {
			return nil, nil, media.Fail("ACCOUNT_IDENTITY_CHANGED", "ACCOUNT", false)
		}
		switch account.State {
		case "ACTIVE", "AUTH_REQUIRED", "UNKNOWN", "ERROR":
		default:
			return nil, nil, media.Fail("INVALID_ACCOUNT_RESULT", "ACCOUNT", false)
		}
		account.Label, account.Revision = accountLabel(payload.Platform), ""
		account.ReasonCode = publicAccountReason(account.ReasonCode)
		if account.State == "ACTIVE" {
			account.ReasonCode = nil
		}
		return map[string]any{"account": account}, nil, nil
	}
	var payload struct {
		accountPayload
		AuthSessionID string `json:"authSessionId"`
		Action        string `json:"action"`
	}
	if decodeAccountPayload(op.Payload, &payload) != nil || payload.AuthSessionID == "" {
		return nil, nil, media.Fail("INVALID_PAYLOAD", "ACCOUNT", false)
	}
	adapter, err := w.accountAdapter(op, payload.accountPayload)
	if err != nil {
		return nil, nil, err
	}
	var callback func(context.Context, string) (AuthSession, error)
	switch payload.Action {
	case "BEGIN":
		callback = adapter.Begin
	case "POLL":
		callback = adapter.Poll
	case "CANCEL":
		callback = adapter.Cancel
	default:
		return nil, nil, media.Fail("INVALID_PAYLOAD", "ACCOUNT", false)
	}
	session := AuthSession{AuthSessionID: payload.AuthSessionID, AccountID: payload.AccountID, Platform: payload.Platform, State: "NEEDS_ACTION", NextAction: "LOCAL_SETUP", ReasonCode: pointer("ACCOUNT_NOT_CONFIGURED")}
	if callback != nil {
		session, err = callback(ctx, payload.AuthSessionID)
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		if err != nil {
			return nil, nil, accountFailure(err)
		}
	} else if payload.Action == "CANCEL" {
		session.State, session.NextAction, session.ReasonCode = "CANCELLED", "NONE", nil
	}
	if session.AuthSessionID != payload.AuthSessionID || session.AccountID != payload.AccountID || session.Platform != payload.Platform {
		return nil, nil, media.Fail("AUTH_SESSION_MISMATCH", "ACCOUNT", false)
	}
	if !validAuthSession(session) {
		return nil, nil, media.Fail("INVALID_AUTH_RESULT", "ACCOUNT", false)
	}
	session.Message = authMessage(session, adapter.LocalSetupMessage)
	session.ReasonCode = publicAccountReason(session.ReasonCode)
	session.Revision, session.TaskID = "", ""
	return map[string]any{"session": session}, nil, nil
}

func accountFailure(err error) *media.Failure {
	failure := media.Classify(err, "ACCOUNT")
	code := safeAccountCode(failure.Code)
	return &media.Failure{Code: code, Stage: "ACCOUNT", Retryable: code == "NETWORK_TEMPORARY" || code == "NETWORK_TIMEOUT" || code == "RATE_LIMITED"}
}

func safeAccountCode(code string) string {
	switch code {
	case "AUTH_REQUIRED", "ACCOUNT_NOT_CONFIGURED", "ACCOUNT_IDENTITY_CHANGED", "AUTH_OR_TOOL_REQUIRED", "PERMISSION_DENIED", "NETWORK_TEMPORARY", "NETWORK_TIMEOUT", "RATE_LIMITED", "PROVIDER_CHANGED", "AUTH_SESSION_NOT_FOUND", "AUTH_SESSION_MISMATCH", "AUTH_EXPIRED", "CANCELLED", "LOCAL_FILE_NOT_FOUND", "IO_ERROR":
		return code
	default:
		return "ACCOUNT_CHECK_FAILED"
	}
}

func publicAccountReason(reason *string) *string {
	if reason == nil {
		return nil
	}
	return pointer(safeAccountCode(*reason))
}

func validAuthSession(session AuthSession) bool {
	switch session.State {
	case "PENDING":
		if session.NextAction != "WAIT" && session.NextAction != "SCAN_QR" && session.NextAction != "OPEN_URL" {
			return false
		}
	case "SUCCEEDED", "CANCELLED", "EXPIRED":
		if session.NextAction != "NONE" {
			return false
		}
	case "NEEDS_ACTION":
		if session.NextAction != "LOCAL_SETUP" {
			return false
		}
	default:
		return false
	}
	if session.NextAction == "SCAN_QR" || session.NextAction == "OPEN_URL" {
		return session.URL != nil && validAuthURL(session.Platform, session.NextAction, *session.URL)
	}
	return session.URL == nil
}

func validAuthURL(platform, action, raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Opaque != "" || strings.Contains(raw, "#") {
		return false
	}
	var allowed map[string]bool
	if platform == "BILIBILI" && action == "SCAN_QR" && parsed.Host == "passport.bilibili.com" {
		allowed = map[string]bool{"navhide": true, "qrcode_key": true, "from": true}
	} else if platform == "GOOGLE_DRIVE" && action == "OPEN_URL" && parsed.Host == "accounts.google.com" {
		allowed = map[string]bool{"client_id": true, "redirect_uri": true, "response_type": true, "scope": true, "state": true, "access_type": true, "prompt": true, "include_granted_scopes": true, "code_challenge": true, "code_challenge_method": true, "hl": true}
	} else {
		return false
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return false
	}
	for key := range query {
		if !allowed[key] {
			return false
		}
	}
	return true
}

func authMessage(session AuthSession, setup string) string {
	switch session.NextAction {
	case "LOCAL_SETUP":
		if setup != "" {
			return setup
		}
		switch session.Platform {
		case "TELEGRAM":
			return "请在 Y7000P 为 v2 新建 Telegram 会话并完成本地登录，再主动检查账号。"
		case "QUARK":
			return "请在 Y7000P 的受控本地入口配置新夸克会话，再主动检查账号。"
		case "GOOGLE_DRIVE":
			return "请在 Y7000P 配置新的 Desktop OAuth client，再亲自完成 Google 官方授权。"
		default:
			return "请在 Y7000P 配置 Bilibili 主账号的受控登录入口，再重新发起登录。"
		}
	case "SCAN_QR":
		return "请使用哔哩哔哩客户端扫描二维码并确认登录。"
	case "OPEN_URL":
		return "请在 Y7000P 打开 Google 官方授权页面并亲自完成授权。"
	case "WAIT":
		return "等待登录确认，请在当前页面主动刷新状态。"
	}
	switch session.State {
	case "SUCCEEDED":
		return "登录已完成，可主动检查账号状态。"
	case "CANCELLED":
		return "登录流程已取消。"
	default:
		return "登录流程已过期，请重新发起登录。"
	}
}
