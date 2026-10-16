package main

import (
	"errors"
	"os"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var consoleKernel = syscall.NewLazyDLL("kernel32.dll")
var getConsoleMode = consoleKernel.NewProc("GetConsoleMode")
var setConsoleMode = consoleKernel.NewProc("SetConsoleMode")
var readConsoleW = consoleKernel.NewProc("ReadConsoleW")
var moveFileExW = consoleKernel.NewProc("MoveFileExW")

func consoleMode(f *os.File) (uint32, bool) {
	var mode uint32
	ok, _, _ := getConsoleMode.Call(f.Fd(), uintptr(unsafe.Pointer(&mode)))
	return mode, ok != 0
}

func interactiveTerminal() bool {
	_, input := consoleMode(os.Stdin)
	_, output := consoleMode(os.Stdout)
	_, diagnostic := consoleMode(os.Stderr)
	return input && output && diagnostic
}

func readSecret() ([]byte, error) {
	mode, ok := consoleMode(os.Stdin)
	if !ok {
		return nil, errors.New("interactive input required")
	}
	// ENABLE_ECHO_INPUT is disabled only while this process reads the line.
	result, _, _ := setConsoleMode.Call(os.Stdin.Fd(), uintptr((mode|0x0001|0x0002)&^uint32(0x0004)))
	if result == 0 {
		return nil, errors.New("cannot hide console input")
	}
	defer setConsoleMode.Call(os.Stdin.Fd(), uintptr(mode))
	buffer := make([]uint16, 16387)
	defer clear(buffer)
	var count uint32
	result, _, _ = readConsoleW.Call(os.Stdin.Fd(), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), uintptr(unsafe.Pointer(&count)), 0)
	if result == 0 || count == 0 || count >= uint32(len(buffer)) {
		return nil, errors.New("cannot read secret line")
	}
	return []byte(string(utf16.Decode(buffer[:count]))), nil
}

func replaceFile(source, target string) error {
	from, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	result, _, callErr := moveFileExW.Call(uintptr(unsafe.Pointer(from)), uintptr(unsafe.Pointer(to)), 0x1|0x8)
	if result == 0 {
		return callErr
	}
	return nil
}
