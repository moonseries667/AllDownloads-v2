package drive

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var mountMpr = syscall.NewLazyDLL("mpr.dll")
var getLogicalDrives = kernel32.NewProc("GetLogicalDrives")
var getConnection = mountMpr.NewProc("WNetGetConnectionW")

func observeMount() (mountObservation, error) {
	mask, _, _ := getLogicalDrives.Call()
	if mask == 0 {
		return mountObservation{}, fault("MOUNT_DRIVE_PROBE_FAILED")
	}
	if mask&(1<<('G'-'A')) == 0 {
		return mountObservation{}, nil
	}
	drive, _ := syscall.UTF16PtrFromString(MountPoint)
	buffer := make([]uint16, 1024)
	size := uint32(len(buffer))
	result, _, _ := getConnection.Call(uintptr(unsafe.Pointer(drive)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)))
	// Every logical G: is occupied, including fixed disks, SUBST and mappings
	// that are not ours. Only the expected new WinFsp network share matches.
	return mountObservation{occupied: true, matches: result == 0 && strings.EqualFold(syscall.UTF16ToString(buffer), `\\server\`+MountVolume)}, nil
}

func checkWinFsp() error {
	// WinFsp's official loader uses the 32-bit HKLM Software\WinFsp InstallDir.
	// This is a read-only installation probe, never an installer or service edit.
	path, _ := syscall.UTF16PtrFromString(`SOFTWARE\WinFsp`)
	var key syscall.Handle
	if syscall.RegOpenKeyEx(syscall.HKEY_LOCAL_MACHINE, path, 0, syscall.KEY_READ|syscall.KEY_WOW64_32KEY, &key) != nil {
		return fault("WINFSP_REQUIRED")
	}
	defer syscall.RegCloseKey(key)
	name, _ := syscall.UTF16PtrFromString("InstallDir")
	var kind, size uint32
	if syscall.RegQueryValueEx(key, name, nil, &kind, nil, &size) != nil || kind != syscall.REG_SZ || size < 2 || size > 65536 {
		return fault("WINFSP_REQUIRED")
	}
	buffer := make([]byte, size)
	if syscall.RegQueryValueEx(key, name, nil, &kind, &buffer[0], &size) != nil {
		return fault("WINFSP_REQUIRED")
	}
	wide := make([]uint16, len(buffer)/2)
	for i := range wide {
		wide[i] = binary.LittleEndian.Uint16(buffer[i*2:])
	}
	file := filepath.Join(syscall.UTF16ToString(wide), "bin", "winfsp-x64.dll")
	info, err := os.Stat(file)
	if err != nil || !info.Mode().IsRegular() {
		return fault("WINFSP_REQUIRED")
	}
	return nil
}
