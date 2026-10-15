//go:build windows

package executor

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

const (
	errorLockViolation        = 33
	fileAttributeReparsePoint = 0x400
	lockfileFailImmediately   = 0x1
	lockfileExclusiveLock     = 0x2
	moveFileReplaceExisting   = 0x1
	moveFileWriteThrough      = 0x8
)

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx        = kernel32.NewProc("LockFileEx")
	procUnlockFileEx      = kernel32.NewProc("UnlockFileEx")
	procCloseHandle       = kernel32.NewProc("CloseHandle")
	procGetFileAttributes = kernel32.NewProc("GetFileAttributesW")
	procMoveFileExW       = kernel32.NewProc("MoveFileExW")
)

type windowsFileLock struct {
	file       *os.File
	overlapped syscall.Overlapped
}

type windowsRunLocks struct {
	locks []*windowsFileLock
}

func acquireRunLocks(paths ...string) (io.Closer, error) {
	result := &windowsRunLocks{}
	for _, path := range paths {
		if err := rejectReparseAncestors(path); err != nil {
			_ = result.Close()
			return nil, err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			_ = result.Close()
			return nil, fmt.Errorf("open lock file %s: %w", path, err)
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			file.Close()
			_ = result.Close()
			if err != nil {
				return nil, fmt.Errorf("inspect lock file %s: %w", path, err)
			}
			return nil, fmt.Errorf("lock path is not a regular file: %s", path)
		}
		lock := &windowsFileLock{file: file}
		flags := lockfileExclusiveLock | lockfileFailImmediately
		ok, _, callErr := procLockFileEx.Call(
			file.Fd(), uintptr(flags), 0, 1, 0,
			uintptr(unsafe.Pointer(&lock.overlapped)),
		)
		runtime.KeepAlive(lock)
		if ok == 0 {
			file.Close()
			_ = result.Close()
			if callErr == syscall.Errno(errorLockViolation) {
				return nil, fmt.Errorf("lock is already held: %s", path)
			}
			return nil, fmt.Errorf("LockFileEx %s: %w", path, callErr)
		}
		result.locks = append(result.locks, lock)
	}
	return result, nil
}

func (l *windowsRunLocks) Close() error {
	var closeErr error
	for i := len(l.locks) - 1; i >= 0; i-- {
		closeErr = errors.Join(closeErr, l.locks[i].Close())
	}
	l.locks = nil
	return closeErr
}

func (l *windowsFileLock) Close() error {
	if l.file == nil {
		return nil
	}
	ok, _, unlockErr := procUnlockFileEx.Call(
		l.file.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&l.overlapped)),
	)
	runtime.KeepAlive(l)
	var err error
	if ok == 0 {
		err = fmt.Errorf("UnlockFileEx: %w", unlockErr)
	}
	err = errors.Join(err, l.file.Close())
	l.file = nil
	return err
}

func isReparsePoint(path string) (bool, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	attributes, _, callErr := procGetFileAttributes.Call(uintptr(unsafe.Pointer(name)))
	if uint32(attributes) == 0xffffffff {
		return false, fmt.Errorf("GetFileAttributesW: %w", callErr)
	}
	return uint32(attributes)&fileAttributeReparsePoint != 0, nil
}

func replaceFile(source, destination string) error {
	from, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := syscall.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	ok, _, callErr := procMoveFileExW.Call(
		uintptr(unsafe.Pointer(from)), uintptr(unsafe.Pointer(to)),
		moveFileReplaceExisting|moveFileWriteThrough,
	)
	if ok == 0 {
		return fmt.Errorf("MoveFileExW replace: %w", callErr)
	}
	return nil
}

func publishNewFile(source, destination string) error {
	from, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := syscall.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	ok, _, callErr := procMoveFileExW.Call(
		uintptr(unsafe.Pointer(from)), uintptr(unsafe.Pointer(to)),
		moveFileWriteThrough,
	)
	if ok == 0 {
		return fmt.Errorf("MoveFileExW publish without replacement: %w", callErr)
	}
	return nil
}
