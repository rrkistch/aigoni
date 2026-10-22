package sitebackup

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDataBackupRestoreAndInterruptedRecovery(t *testing.T) {
	root := t.TempDir()
	posts := filepath.Join(root, "posts")
	os.Mkdir(posts, 0700)
	os.WriteFile(filepath.Join(posts, "one.md"), []byte("original"), 0600)
	cfg := filepath.Join(root, "config.yaml")
	os.WriteFile(cfg, []byte("site: original"), 0600)
	targets := []Target{{Name: "posts", Path: posts}, {Name: "config", Path: cfg, File: true}}
	archive := filepath.Join(root, ".aigoni-admin", "backups", "one.tar.gz")
	manifest, err := Create(root, targets, archive)
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(root, ".aigoni-admin", "restores", "one")
	os.MkdirAll(work, 0700)
	stage := filepath.Join(work, "stage")
	if _, err = Extract(archive, stage, targets); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(posts, "one.md"), []byte("changed"), 0600)
	os.WriteFile(filepath.Join(posts, "extra.md"), []byte("not in backup"), 0600)
	if err = Restore(root, stage, targets, manifest); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(posts, "one.md"))
	if string(b) != "original" {
		t.Fatal("not restored")
	}
	if _, err = os.Stat(filepath.Join(posts, "extra.md")); !os.IsNotExist(err) {
		t.Fatal("restore merged instead of replaced")
	}
	// Simulate a crash after moving the old directory, before writing the commit decision.
	work = filepath.Join(root, ".aigoni-admin", "restores", "interrupted")
	rollback := filepath.Join(work, "rollback")
	os.MkdirAll(rollback, 0700)
	old := filepath.Join(rollback, "posts")
	os.Rename(posts, old)
	os.Mkdir(posts, 0700)
	os.WriteFile(filepath.Join(posts, "one.md"), []byte("partial"), 0600)
	j := journal{Directory: work, Moves: []move{{Target: posts, Rollback: old, HadOriginal: true}}}
	data, _ := json.Marshal(j)
	os.WriteFile(journalPath(root), data, 0600)
	if err = Recover(root); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(posts, "one.md"))
	if string(b) != "original" {
		t.Fatal("interrupted restore not recovered")
	}
}
func TestBackupRejectsArchiveTraversalAndLinks(t *testing.T) {
	for _, entry := range []tar.Header{{Name: "posts/../../escape", Typeflag: tar.TypeReg}, {Name: "posts/link", Typeflag: tar.TypeSymlink, Linkname: "../../escape"}} {
		t.Run(entry.Name, func(t *testing.T) {
			root := t.TempDir()
			archive := filepath.Join(root, "bad.tar.gz")
			file, _ := os.Create(archive)
			gz := gzip.NewWriter(file)
			tw := tar.NewWriter(gz)
			m, _ := json.Marshal(Manifest{Version: 1, Roots: map[string]bool{"posts": true}})
			tw.WriteHeader(&tar.Header{Name: "manifest.json", Size: int64(len(m)), Typeflag: tar.TypeReg, Mode: 0600})
			tw.Write(m)
			tw.WriteHeader(&tar.Header{Name: "posts", Typeflag: tar.TypeDir, Mode: 0700})
			tw.WriteHeader(&entry)
			tw.Close()
			gz.Close()
			file.Close()
			if _, err := Extract(archive, filepath.Join(root, "stage"), []Target{{Name: "posts", Path: filepath.Join(root, "posts")}}); err == nil {
				t.Fatal("unsafe archive accepted")
			}
			if _, err := os.Stat(filepath.Join(root, "escape")); !os.IsNotExist(err) {
				t.Fatal("archive escaped staging")
			}
		})
	}
}
