package configdoc

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicDocumentConflictAndRestart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "private", "settings.json")
	d, err := Read(file, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	d, err = Write(file, d, 0, map[string]string{"name": "first"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Write(file, d, 0, map[string]string{"name": "lost"}); !errors.Is(err, ErrConflict) {
		t.Fatal("missing conflict", err)
	}
	restored, err := Read(file, map[string]string{})
	if err != nil || restored.Value["name"] != "first" || restored.Revision != 1 {
		t.Fatal(restored, err)
	}
	before, _ := os.ReadFile(file)
	if err = Atomic(filepath.Join(file, "invalid"), []byte("bad")); err == nil {
		t.Fatal("expected write failure")
	}
	after, _ := os.ReadFile(file)
	if string(before) != string(after) {
		t.Fatal("failed write damaged current document")
	}
}
