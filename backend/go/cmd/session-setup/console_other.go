//go:build !windows

package main

import (
	"errors"
	"os"
)

func interactiveTerminal() bool { return false }
func readSecret() ([]byte, error) {
	return nil, errors.New("interactive setup targets a Windows desktop console")
}
func replaceFile(source, target string) error { return os.Rename(source, target) }
