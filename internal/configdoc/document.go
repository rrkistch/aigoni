// Package configdoc stores small versioned administrative documents atomically.
// Callers own serialization with their domain lock. No documents are public by default.
package configdoc

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrConflict = errors.New("configuration revision conflict")

type Document[T any] struct {
	Version  int    `json:"version"`
	Revision uint64 `json:"revision"`
	Value    T      `json:"value"`
}

func Read[T any](name string, fallback T) (Document[T], error) {
	d := Document[T]{Version: 1, Value: fallback}
	b, err := os.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	if err = json.Unmarshal(b, &d); err != nil {
		return d, err
	}
	if d.Version != 1 {
		return d, fmt.Errorf("unsupported configuration version %d", d.Version)
	}
	return d, nil
}

func Write[T any](name string, current Document[T], expected uint64, value T) (Document[T], error) {
	if current.Revision != expected {
		return current, ErrConflict
	}
	next := Document[T]{Version: 1, Revision: current.Revision + 1, Value: value}
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return current, err
	}
	if err = Atomic(name, b); err != nil {
		return current, err
	}
	return next, nil
}

func Atomic(name string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(name), ".pending-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temp, name)
}
