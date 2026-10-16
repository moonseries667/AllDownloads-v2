package executor

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// State reuses the executor's atomic JSON and Windows exclusive lock primitives
// for the runner delivery journal. It contains no business scheduling policy.
type State struct {
	dir  string
	lock io.Closer
}

func OpenState(dir string) (*State, error) {
	root, err := prepareRoot(dir)
	if err != nil {
		return nil, err
	}
	lock, err := acquireRunLocks(filepath.Join(root, ".runner.lock"))
	if err != nil {
		return nil, err
	}
	return &State{root, lock}, nil
}
func (s *State) Close() error { return s.lock.Close() }
func (s *State) Read(name string, value any) error {
	if err := validateOperationID(name); err != nil {
		return err
	}
	path := filepath.Join(s.dir, name+".json")
	if err := rejectReparseAncestors(path); err != nil {
		return err
	}
	return readJSON(path, value)
}
func (s *State) Write(name string, value any) error {
	if err := validateOperationID(name); err != nil {
		return err
	}
	path := filepath.Join(s.dir, name+".json")
	if err := rejectReparseAncestors(path); err != nil {
		return err
	}
	return atomicWriteJSON(path, value)
}

func WriteStateFile(path string, value any) error {
	if err := rejectReparseAncestors(path); err != nil {
		return err
	}
	return atomicWriteJSON(path, value)
}
func (s *State) Names(prefix string) ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) && strings.HasSuffix(entry.Name(), ".json") {
			out = append(out, strings.TrimSuffix(entry.Name(), ".json"))
		}
	}
	return out, nil
}
func VerifyFile(root, relative, hash, size string) error {
	if _, err := normalizeRelativePath(relative); err != nil {
		return err
	}
	e := Engine{outputRoot: root}
	path, exists, err := e.inspectOutput(relative)
	if err != nil {
		return err
	}
	if !exists {
		return os.ErrNotExist
	}
	n, err := strconv.ParseInt(size, 10, 64)
	if err != nil || n < 0 {
		return errors.New("invalid receipt size")
	}
	ok, err := matchesFile(path, hash, n)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("file no longer matches receipt")
	}
	return nil
}
