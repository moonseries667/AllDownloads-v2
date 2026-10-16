package drive

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

var crypt32 = syscall.NewLazyDLL("crypt32.dll")
var protectData = crypt32.NewProc("CryptProtectData")
var unprotectData = crypt32.NewProc("CryptUnprotectData")
var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var localFree = kernel32.NewProc("LocalFree")
var moveFileEx = kernel32.NewProc("MoveFileExW")

type dataBlob struct {
	size uint32
	data *byte
}

// DPAPI uses this Windows user's profile and machine, with no UI and without
// CRYPTPROTECT_LOCAL_MACHINE. Broad inherited directory ACLs cannot expose the
// plaintext token. Run authorization and the runner as the same Y7000P user.
func cryptPrivate(raw []byte, decrypt bool) ([]byte, error) {
	if len(raw) == 0 {
		return nil, errors.New("empty private data")
	}
	in := dataBlob{uint32(len(raw)), &raw[0]}
	var out dataBlob
	procedure := protectData
	if decrypt {
		procedure = unprotectData
	}
	ok, _, _ := procedure.Call(uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, 1, uintptr(unsafe.Pointer(&out)))
	runtime.KeepAlive(raw)
	if ok == 0 {
		return nil, errors.New("private data protection unavailable")
	}
	defer localFree.Call(uintptr(unsafe.Pointer(out.data)))
	if out.size == 0 || out.size > 1<<20 {
		return nil, errors.New("invalid protected data")
	}
	return append([]byte(nil), unsafe.Slice(out.data, int(out.size))...), nil
}

func sealPrivate(raw []byte) ([]byte, error) {
	sealed, err := cryptPrivate(raw, false)
	if err != nil {
		return nil, err
	}
	return append([]byte("ADGD1\x00"), sealed...), nil
}
func unsealPrivate(raw []byte) ([]byte, error) {
	if len(raw) < 7 || string(raw[:6]) != "ADGD1\x00" {
		return nil, errors.New("invalid protected file")
	}
	return cryptPrivate(raw[6:], true)
}
func checkPrivateMode(os.FileInfo) error { return nil }
func replacePrivate(from, to string) error {
	old, err := syscall.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	newPath, err := syscall.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	ok, _, err := moveFileEx.Call(uintptr(unsafe.Pointer(old)), uintptr(unsafe.Pointer(newPath)), 0x1|0x8)
	if ok == 0 {
		return err
	}
	return nil
}
func acquirePrivateLock(name string) (func(), error) {
	path, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	handle, err := syscall.CreateFile(path, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	return func() { _ = syscall.CloseHandle(handle) }, nil
}
func privateLockBusy(err error) bool {
	return errors.Is(err, syscall.Errno(32)) || errors.Is(err, syscall.Errno(33))
}
