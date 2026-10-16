package drive

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Only the configured v2 path is inspected. Reparse/symlink ancestors are not
// accepted for credential files or their sibling transaction lock.
func privatePath(name string) error {
	if !filepath.IsAbs(name) {
		return errors.New("private path must be absolute")
	}
	for current := filepath.Clean(name); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("private path is a link")
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

func readPrivate(name string) ([]byte, error) {
	if err := privatePath(name); err != nil {
		return nil, err
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, errors.New("invalid private file")
	}
	if err := checkPrivateMode(info); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, errors.New("cannot read private file")
	}
	return unsealPrivate(raw)
}

func writePrivate(name string, raw []byte) error {
	if err := privatePath(name); err != nil {
		return err
	}
	sealed, err := sealPrivate(raw)
	if err != nil {
		return err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	temporary := filepath.Join(filepath.Dir(name), ".drive-auth-"+hex.EncodeToString(nonce[:])+".tmp")
	f, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	_, writeErr := f.Write(sealed)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	return replacePrivate(temporary, name)
}

func lockPrivate(name string) (func(), error) {
	if err := privatePath(name); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		return nil, err
	}
	if err := privatePath(name + ".lock"); err != nil {
		return nil, err
	}
	return acquirePrivateLock(name + ".lock")
}
