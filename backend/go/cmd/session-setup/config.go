package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/moonseries667/AllDownloads-v2/backend/go/internal/platforms/telegram"
)

type localConfig struct {
	path, stateDir string
	original       []byte
	fields         map[string]json.RawMessage
	isolated       bool
}

func loadConfig(path string) (*localConfig, error) {
	if !filepath.IsAbs(path) || noLinks(path) != nil {
		return nil, errors.New("configuration must be a regular file at an absolute path without links")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot read runner configuration")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, errors.New("invalid runner configuration size")
	}
	cfg := &localConfig{path: path, original: raw}
	if json.Unmarshal(raw, &cfg.fields) != nil || cfg.fields == nil || json.Unmarshal(cfg.fields["stateDir"], &cfg.stateDir) != nil || !filepath.IsAbs(cfg.stateDir) {
		return nil, errors.New("configuration requires an absolute new v2 stateDir")
	}
	if value := cfg.fields["isolatedTestMode"]; len(value) != 0 && json.Unmarshal(value, &cfg.isolated) != nil {
		return nil, errors.New("invalid isolatedTestMode")
	}
	return cfg, nil
}

func (cfg *localConfig) privatePath(path string) error {
	root := filepath.Join(filepath.Dir(cfg.stateDir), "auth")
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsAbs(path) || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || strings.ContainsAny(path, "\x00\r\n") {
		return errors.New("session paths must be inside this new runtime/auth directory")
	}
	if err := noLinks(path); err != nil {
		return errors.New("session paths must not use links or junctions")
	}
	return nil
}

func noLinks(path string) error {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("symbolic link")
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

func rawObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, errors.New("invalid account configuration")
	}
	return object, nil
}

func (cfg *localConfig) quarkAccount() (quarkSettings, error) {
	a := quarkSettings{}
	accounts, err := rawObject(cfg.fields["quarkAccounts"])
	if err != nil {
		return a, err
	}
	if raw := accounts["quark-main"]; len(raw) != 0 && json.Unmarshal(raw, &a) != nil {
		return a, errors.New("invalid quark-main configuration")
	}
	if a.CookieFile == "" {
		a.CookieFile = filepath.Join(filepath.Dir(cfg.stateDir), "auth", "quark-cookie.local")
	}
	if a.ExpectedUserID == "REPLACE_WITH_NEW_SESSION_QUARK_UID" {
		a.ExpectedUserID = ""
	}
	if !cfg.isolated && (a.BaseURL != "" || a.AccountURL != "") {
		return a, errors.New("platform endpoint overrides require isolatedTestMode")
	}
	if err := cfg.privatePath(a.CookieFile); err != nil {
		return a, err
	}
	return a, nil
}

func (cfg *localConfig) telegramAccount() (*telegram.TDL, telegram.TDLAccount, error) {
	settings := struct {
		Executable string                         `json:"executable"`
		Accounts   map[string]telegram.TDLAccount `json:"accounts"`
	}{}
	if raw := cfg.fields["telegram"]; len(raw) != 0 && json.Unmarshal(raw, &settings) != nil {
		return nil, telegram.TDLAccount{}, errors.New("invalid Telegram configuration")
	}
	if !filepath.IsAbs(settings.Executable) {
		return nil, telegram.TDLAccount{}, errors.New("configure the absolute path of the packaged tdl 0.20.4 executable first")
	}
	a := settings.Accounts["telegram-main"]
	if a.Namespace == "" {
		a.Namespace = "alldownloads-v2"
	}
	if a.Namespace != "alldownloads-v2" {
		return nil, a, errors.New("Telegram namespace must be alldownloads-v2")
	}
	if a.StorageDir == "" {
		a.StorageDir = filepath.Join(filepath.Dir(cfg.stateDir), "auth", "telegram")
	}
	if a.PeerID == "REPLACE_WITH_NEW_SESSION_USER_ID" {
		a.PeerID = ""
	}
	if err := cfg.privatePath(a.StorageDir); err != nil {
		return nil, a, err
	}
	if strings.Contains(a.StorageDir, ",") || noLinks(cfg.stateDir) != nil {
		return nil, a, errors.New("invalid new Telegram runtime path")
	}
	tdl := &telegram.TDL{Executable: settings.Executable, WorkDir: filepath.Join(cfg.stateDir, "telegram-work"), Accounts: map[string]telegram.TDLAccount{"telegram-main": a}}
	return tdl, a, nil
}

func (cfg *localConfig) unchanged() error {
	raw, err := os.ReadFile(cfg.path)
	if err != nil || !bytes.Equal(raw, cfg.original) {
		return errors.New("runner configuration changed during setup; no further changes were saved")
	}
	return nil
}

func put(object map[string]json.RawMessage, key string, value any) {
	object[key], _ = json.Marshal(value)
}

func (cfg *localConfig) saveQuark(a quarkSettings) error {
	accounts, err := rawObject(cfg.fields["quarkAccounts"])
	if err != nil {
		return err
	}
	account, err := rawObject(accounts["quark-main"])
	if err != nil {
		return err
	}
	put(account, "cookieFile", a.CookieFile)
	put(account, "expectedUserId", a.ExpectedUserID)
	put(accounts, "quark-main", account)
	put(cfg.fields, "quarkAccounts", accounts)
	return cfg.save()
}

func (cfg *localConfig) saveTelegram(executable string, a telegram.TDLAccount) error {
	settings, err := rawObject(cfg.fields["telegram"])
	if err != nil {
		return err
	}
	accounts, err := rawObject(settings["accounts"])
	if err != nil {
		return err
	}
	account, err := rawObject(accounts["telegram-main"])
	if err != nil {
		return err
	}
	put(account, "namespace", a.Namespace)
	put(account, "storageDir", a.StorageDir)
	put(account, "peerId", a.PeerID)
	put(accounts, "telegram-main", account)
	put(settings, "executable", executable)
	put(settings, "accounts", accounts)
	put(cfg.fields, "telegram", settings)
	return cfg.save()
}

func (cfg *localConfig) save() error {
	if err := cfg.unchanged(); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg.fields, "", "  ")
	if err != nil {
		return errors.New("cannot encode runner configuration")
	}
	raw = append(raw, '\n')
	if err := writeAtomic(cfg.path, raw, 0600); err != nil {
		return errors.New("cannot save private runner configuration")
	}
	cfg.original = raw
	return nil
}

func lockConfig(path string) (func(), error) {
	lockPath := path + ".session-setup.lock"
	f, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, errors.New("another setup is active; if a prior setup crashed, remove only runner.json.session-setup.lock after confirming it has stopped")
	}
	_ = f.Close()
	return func() { _ = os.Remove(lockPath) }, nil
}

func writeAtomic(path string, raw []byte, mode os.FileMode) error {
	if err := noLinks(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".session-setup-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return replaceFile(f.Name(), path)
}

func installHelper(tdl *telegram.TDL) error {
	source, err := os.Executable()
	if err != nil {
		return errors.New("cannot locate session-setup executable")
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		return errors.New("cannot read packaged identity helper")
	}
	if err := writeAtomic(tdl.IdentityHelperPath(), raw, 0700); err != nil {
		return errors.New("cannot install the private local Telegram identity helper")
	}
	return nil
}
