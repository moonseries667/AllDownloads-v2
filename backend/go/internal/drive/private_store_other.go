//go:build !windows

package drive

import (
	"errors"
	"os"
	"syscall"
)

func sealPrivate(raw []byte) ([]byte, error)   { return raw, nil }
func unsealPrivate(raw []byte) ([]byte, error) { return raw, nil }
func checkPrivateMode(info os.FileInfo) error {
	if info.Mode().Perm()&0077 != 0 {
		return errors.New("private file permissions are too broad")
	}
	return nil
}
func replacePrivate(from, to string) error { return os.Rename(from, to) }
func acquirePrivateLock(name string) (func(), error) {
	f, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}
func privateLockBusy(err error) bool {
	return errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK)
}
