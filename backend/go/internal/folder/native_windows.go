//go:build windows

package folder

import (
	"runtime"
	"syscall"
	"unsafe"
)

var user32 = syscall.NewLazyDLL("user32.dll")
var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var ole32 = syscall.NewLazyDLL("ole32.dll")

// InteractiveDesktop refuses Session 0, invisible window stations, and a
// different/locked input desktop. It does not switch sessions or impersonate a
// desktop user when the runner was launched as a service.
func InteractiveDesktop() bool {
	var session uint32
	pid, _, _ := kernel32.NewProc("GetCurrentProcessId").Call()
	ok, _, _ := kernel32.NewProc("ProcessIdToSessionId").Call(pid, uintptr(unsafe.Pointer(&session)))
	if ok == 0 || session == 0 {
		return false
	}
	station, _, _ := user32.NewProc("GetProcessWindowStation").Call()
	var flags struct{ Inherit, Reserved, Flags uint32 }
	var needed uint32
	ok, _, _ = user32.NewProc("GetUserObjectInformationW").Call(station, 1, uintptr(unsafe.Pointer(&flags)), unsafe.Sizeof(flags), uintptr(unsafe.Pointer(&needed)))
	if ok == 0 || flags.Flags&1 == 0 {
		return false
	}
	input, _, _ := user32.NewProc("OpenInputDesktop").Call(0, 0, 1)
	if input == 0 {
		return false
	}
	defer user32.NewProc("CloseDesktop").Call(input)
	tid, _, _ := kernel32.NewProc("GetCurrentThreadId").Call()
	current, _, _ := user32.NewProc("GetThreadDesktop").Call(tid)
	name := func(handle uintptr) string {
		var buf [256]uint16
		ok, _, _ := user32.NewProc("GetUserObjectInformationW").Call(handle, 2, uintptr(unsafe.Pointer(&buf[0])), unsafe.Sizeof(buf), uintptr(unsafe.Pointer(&needed)))
		if ok == 0 {
			return ""
		}
		return syscall.UTF16ToString(buf[:])
	}
	a, b := name(input), name(current)
	return a != "" && a == b
}

type guid struct {
	A    uint32
	B, C uint16
	D    [8]byte
}
type comObject struct{ VTable *[29]uintptr }

func (o *comObject) call(index int, args ...uintptr) uint32 {
	params := append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)
	code, _, _ := syscall.SyscallN(o.VTable[index], params...)
	runtime.KeepAlive(o)
	return uint32(code)
}
func failed(code uint32) bool { return int32(code) < 0 }

// ShowNative is called only in the isolated helper mode of runner.exe. Its
// parent owns the deadline and process tree. IFileDialog stays on one COM STA.
func ShowNative() Result {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if !InteractiveDesktop() {
		return Result{Status: "UNAVAILABLE", Reason: "NO_INTERACTIVE_DESKTOP"}
	}
	code, _, _ := ole32.NewProc("CoInitializeEx").Call(0, 2)
	if failed(uint32(code)) {
		return Result{Status: "UNAVAILABLE", Reason: "COM_INITIALIZATION_FAILED"}
	}
	defer ole32.NewProc("CoUninitialize").Call()
	clsid := guid{0xdc1c5a9c, 0xe88a, 0x4dde, [8]byte{0xa5, 0xa1, 0x60, 0xf8, 0x2a, 0x20, 0xae, 0xf7}}
	iid := guid{0xd57c7288, 0xd4ad, 0x4768, [8]byte{0xbe, 0x02, 0x9d, 0x96, 0x95, 0x32, 0xd9, 0x60}}
	var dialog *comObject
	code, _, _ = ole32.NewProc("CoCreateInstance").Call(uintptr(unsafe.Pointer(&clsid)), 0, 1, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&dialog)))
	bad := func() Result { return Result{Status: "UNAVAILABLE", Reason: "NATIVE_PICKER_FAILED"} }
	if failed(uint32(code)) || dialog == nil {
		return bad()
	}
	defer dialog.call(2)
	var options uint32
	if failed(dialog.call(10, uintptr(unsafe.Pointer(&options)))) {
		return bad()
	}
	// PICKFOLDERS | FORCEFILESYSTEM | PATHMUSTEXIST | DONTADDTORECENT.
	if failed(dialog.call(9, uintptr(options|0x20|0x40|0x800|0x02000000))) {
		return bad()
	}
	title, _ := syscall.UTF16PtrFromString("选择本机下载目录（运行主机）")
	if failed(dialog.call(17, uintptr(unsafe.Pointer(title)))) {
		return bad()
	}
	runtime.KeepAlive(title)
	code = uintptr(dialog.call(3, 0))
	if uint32(code) == 0x800704c7 {
		return Result{Status: "CANCELLED", Reason: "PICKER_CANCELLED"}
	}
	if failed(uint32(code)) {
		return bad()
	}
	var item *comObject
	if failed(dialog.call(20, uintptr(unsafe.Pointer(&item)))) || item == nil {
		return bad()
	}
	defer item.call(2)
	var path *uint16
	if failed(item.call(5, 0x80058000, uintptr(unsafe.Pointer(&path)))) || path == nil {
		return bad()
	}
	defer ole32.NewProc("CoTaskMemFree").Call(uintptr(unsafe.Pointer(path)))
	text := make([]uint16, 0, 256)
	for i := 0; i < 32768; i++ {
		v := *(*uint16)(unsafe.Add(unsafe.Pointer(path), i*2))
		if v == 0 {
			return Result{Status: "SELECTED", Path: syscall.UTF16ToString(text)}
		}
		text = append(text, v)
	}
	return bad()
}
