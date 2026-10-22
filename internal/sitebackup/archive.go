// Package sitebackup handles data-only archives and recoverable directory replacement.
// Callers provide the exact allowed targets and stop application writes during Create/Restore.
package sitebackup

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"aigoni/internal/configdoc"
)

const MaxExpanded int64 = 16 << 30
const reserve int64 = 128 << 20

type Target struct {
	Name string
	Path string
	File bool
}
type Manifest struct {
	ThemeVersion string          `json:"theme_version,omitempty"`
	Version      int             `json:"version"`
	Created      time.Time       `json:"created"`
	Bytes        int64           `json:"bytes"`
	Roots        map[string]bool `json:"roots"`
	Excludes     []string        `json:"excludes"`
}

func Within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func ValidateTargets(root string, targets []Target) error {
	names := map[string]bool{}
	for _, t := range targets {
		if t.Name == "" || strings.ContainsAny(t.Name, "/\\:.") || names[t.Name] || !Within(root, t.Path) {
			return errors.New("unsafe backup target")
		}
		names[t.Name] = true
		rel, _ := filepath.Rel(root, t.Path)
		current := root
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			current = filepath.Join(current, part)
			info, e := os.Lstat(current)
			if os.IsNotExist(e) {
				break
			}
			if e != nil {
				return e
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return errors.New("backup targets cannot traverse symlinks")
			}
		}
	}
	for i, a := range targets {
		for j, b := range targets {
			if i != j && (a.Path == b.Path || Within(a.Path, b.Path)) {
				return errors.New("backup data paths overlap")
			}
		}
	}
	return nil
}
func Create(root string, targets []Target, out string, themeVersion ...string) (manifest Manifest, err error) {
	if err = ValidateTargets(root, targets); err != nil {
		return
	}
	manifest = Manifest{Version: 1, Created: time.Now().UTC(), Roots: map[string]bool{}, Excludes: []string{"API keys", "sessions", "assistant credentials", "theme dependencies", "generated website"}}
	if len(themeVersion) > 0 {
		manifest.ThemeVersion = themeVersion[0]
	}
	type entry struct {
		file, name string
		info       os.FileInfo
	}
	entries := []entry{}
	for _, target := range targets {
		if !target.File && Within(target.Path, out) {
			return manifest, errors.New("archive output is inside a data target")
		}
		info, e := os.Lstat(target.Path)
		if os.IsNotExist(e) {
			manifest.Roots[target.Name] = false
			continue
		}
		if e != nil {
			return manifest, e
		}
		if target.File == info.IsDir() {
			return manifest, errors.New("backup target kind mismatch")
		}
		manifest.Roots[target.Name] = true
		e = filepath.Walk(target.Path, func(file string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
				return errors.New("backup contains a symlink or special file")
			}
			rel, _ := filepath.Rel(target.Path, file)
			name := target.Name
			if rel != "." {
				name += "/" + filepath.ToSlash(rel)
			}
			if !validEntryName(name) {
				return errors.New("backup contains a non-portable file name")
			}
			if info.Mode().IsRegular() {
				if smallBackupEntry(name, target) && info.Size() > 4<<20 {
					return errors.New("configuration or Markdown entry exceeds the restorable 4 MiB limit")
				}
				manifest.Bytes += info.Size()
			}
			if manifest.Bytes > MaxExpanded || len(entries) >= 100000 {
				return errors.New("backup exceeds supported size")
			}
			entries = append(entries, entry{file, name, info})
			return nil
		})
		if e != nil {
			return manifest, e
		}
	}
	if err = os.MkdirAll(filepath.Dir(out), 0700); err != nil {
		return
	}
	free, e := FreeBytes(filepath.Dir(out))
	if e != nil {
		return manifest, e
	}
	if free < reserve {
		return manifest, errors.New("not enough free space")
	}
	file, e := os.CreateTemp(filepath.Dir(out), ".backup-*")
	if e != nil {
		return manifest, e
	}
	temp := file.Name()
	defer os.Remove(temp)
	defer file.Close()
	_ = file.Chmod(0600)
	limited := &limitedWriter{Writer: file, remaining: free - reserve}
	gz := gzip.NewWriter(limited)
	tw := tar.NewWriter(gz)
	body, _ := json.Marshal(manifest)
	if err = tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0600, Size: int64(len(body)), Typeflag: tar.TypeReg}); err == nil {
		_, err = tw.Write(body)
	}
	for _, entry := range entries {
		if err != nil {
			break
		}
		header, e := tar.FileInfoHeader(entry.info, "")
		if e != nil {
			err = e
			break
		}
		header.Name = entry.name
		header.Mode = 0600
		if entry.info.IsDir() {
			header.Mode = 0700
		}
		if err = tw.WriteHeader(header); err != nil {
			break
		}
		if entry.info.IsDir() {
			continue
		}
		from, e := os.Open(entry.file)
		if e != nil {
			err = e
			break
		}
		_, err = io.CopyN(tw, from, entry.info.Size())
		from.Close()
	}
	if e = tw.Close(); err == nil {
		err = e
	}
	if e = gz.Close(); err == nil {
		err = e
	}
	if err == nil {
		err = file.Sync()
	}
	if e = file.Close(); err == nil {
		err = e
	}
	if err == nil {
		err = os.Rename(temp, out)
	}
	return
}

type limitedWriter struct {
	io.Writer
	remaining int64
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, errors.New("backup exhausted free-space allowance")
	}
	n, e := w.Writer.Write(p)
	w.remaining -= int64(n)
	return n, e
}
func validEntryName(name string) bool {
	return name != "" && !strings.ContainsAny(name, "\\:\x00") && !strings.HasPrefix(name, "/") && path.Clean(name) == strings.TrimSuffix(name, "/") && name != ".." && !strings.HasPrefix(name, "../")
}

// Extract only accepts the current data-only format. It never follows archive links.
func Extract(archive, stage string, targets []Target) (Manifest, error) {
	var manifest Manifest
	file, err := os.Open(archive)
	if err != nil {
		return manifest, err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return manifest, err
	}
	defer gz.Close()
	gz.Multistream(false)
	tr := tar.NewReader(gz)
	first, err := tr.Next()
	if err != nil || first.Name != "manifest.json" || first.Size > 64<<10 || first.Typeflag != tar.TypeReg {
		return manifest, errors.New("not an aigoni data backup")
	}
	b, err := io.ReadAll(io.LimitReader(tr, 64<<10))
	if err != nil || json.Unmarshal(b, &manifest) != nil || manifest.Version != 1 || manifest.Bytes < 0 || manifest.Bytes > MaxExpanded {
		return manifest, errors.New("unsupported or corrupt backup manifest")
	}
	allowed := map[string]Target{}
	for _, target := range targets {
		allowed[target.Name] = target
		if _, ok := manifest.Roots[target.Name]; !ok {
			return manifest, errors.New("backup target inventory does not match this version")
		}
	}
	if len(manifest.Roots) != len(allowed) {
		return manifest, errors.New("unknown backup data targets")
	}
	if err = os.Mkdir(stage, 0700); err != nil {
		return manifest, err
	}
	free, err := FreeBytes(stage)
	if err != nil || free < manifest.Bytes+reserve {
		return manifest, errors.New("not enough space to validate and restore backup")
	}
	total := int64(0)
	count := 0
	seen := map[string]bool{}
	for {
		header, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return manifest, e
		}
		count++
		if count > 100000 || !validEntryName(header.Name) {
			return manifest, errors.New("unsafe archive entry")
		}
		name := strings.TrimSuffix(header.Name, "/")
		if seen[name] {
			return manifest, errors.New("duplicate archive entry")
		}
		seen[name] = true
		group := strings.SplitN(name, "/", 2)[0]
		target, ok := allowed[group]
		if !ok || !manifest.Roots[group] || (target.File && name != group) {
			return manifest, errors.New("archive entry is outside allowed targets")
		}
		output := filepath.Join(stage, filepath.FromSlash(name))
		if !Within(stage, output) {
			return manifest, errors.New("archive path escapes staging area")
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if header.Size != 0 {
				return manifest, errors.New("directory entry contains data")
			}
			if target.File {
				return manifest, errors.New("file target contains directory")
			}
			if e = os.MkdirAll(output, 0700); e != nil {
				return manifest, e
			}
		case tar.TypeReg, tar.TypeRegA:
			if name == group && !target.File {
				return manifest, errors.New("directory root cannot be replaced by a file")
			}
			small := smallBackupEntry(name, target)
			if small && header.Size > 4<<20 {
				return manifest, errors.New("configuration or Markdown entry exceeds 4 MiB")
			}
			if header.Size < 0 || header.Size > manifest.Bytes-total || header.Size > MaxExpanded-total {
				return manifest, errors.New("expanded backup exceeds declared size")
			}
			total += header.Size
			if e = os.MkdirAll(filepath.Dir(output), 0700); e != nil {
				return manifest, e
			}
			out, e := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				return manifest, e
			}
			_, e = io.CopyN(out, tr, header.Size)
			if e == nil {
				e = out.Sync()
			}
			out.Close()
			if e != nil {
				return manifest, e
			}
		default:
			return manifest, errors.New("archive links and special files are forbidden")
		}
	}
	if trailing, e := io.Copy(io.Discard, io.LimitReader(gz, (1<<20)+1)); e != nil {
		return manifest, e
	} else if trailing > 1<<20 {
		return manifest, errors.New("excessive trailing archive data")
	}
	if total != manifest.Bytes {
		return manifest, errors.New("backup is incomplete")
	}
	for name, present := range manifest.Roots {
		if present && !seen[name] {
			return manifest, errors.New("backup root is missing")
		}
	}
	return manifest, nil
}

type move struct {
	Target      string `json:"target"`
	Stage       string `json:"stage"`
	Rollback    string `json:"rollback"`
	HadOriginal bool   `json:"had_original"`
}
type journal struct {
	Committed bool   `json:"committed"`
	Directory string `json:"directory"`
	Moves     []move `json:"moves"`
}

func journalPath(root string) string {
	return filepath.Join(root, ".aigoni-admin", "restore-journal.json")
}
func Restore(root, stage string, targets []Target, manifest Manifest) error {
	if err := ValidateTargets(root, targets); err != nil {
		return err
	}
	if !Within(filepath.Join(root, ".aigoni-admin", "restores"), stage) {
		return errors.New("staging is outside managed restore storage")
	}
	rollback := filepath.Join(filepath.Dir(stage), "rollback")
	if err := os.Mkdir(rollback, 0700); err != nil {
		return err
	}
	j := journal{Directory: filepath.Dir(stage)}
	for _, target := range targets {
		_, err := os.Lstat(target.Path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		m := move{Target: target.Path, Rollback: filepath.Join(rollback, target.Name), HadOriginal: err == nil}
		if manifest.Roots[target.Name] {
			m.Stage = filepath.Join(stage, target.Name)
		}
		j.Moves = append(j.Moves, m)
	}
	b, _ := json.Marshal(j)
	if err := configdoc.Atomic(journalPath(root), b); err != nil {
		return err
	}
	fail := func(err error) error {
		if rollbackErr := Recover(root); rollbackErr != nil {
			var cleanup *CleanupError
			if errors.As(rollbackErr, &cleanup) {
				return err
			}
			return fmt.Errorf("restore failed (%v); recovery required: %w", err, rollbackErr)
		}
		return err
	}
	for _, m := range j.Moves {
		if m.HadOriginal {
			if err := os.Rename(m.Target, m.Rollback); err != nil {
				return fail(err)
			}
		}
		if m.Stage != "" {
			if err := os.MkdirAll(filepath.Dir(m.Target), 0700); err != nil {
				return fail(err)
			}
			if err := os.Rename(m.Stage, m.Target); err != nil {
				return fail(err)
			}
		}
	}
	j.Committed = true
	b, _ = json.Marshal(j)
	if err := configdoc.Atomic(journalPath(root), b); err != nil {
		return fail(err)
	}
	return Recover(root)
}

// Recover runs before configuration is read at startup. Interrupted transactions roll back.
func Recover(root string) error {
	b, err := os.ReadFile(journalPath(root))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var j journal
	if err = json.Unmarshal(b, &j); err != nil {
		return err
	}
	managed := filepath.Join(root, ".aigoni-admin", "restores")
	if !Within(managed, j.Directory) {
		return errors.New("invalid restore journal directory")
	}
	for _, m := range j.Moves {
		if !Within(root, m.Target) || !Within(j.Directory, m.Rollback) || (m.Stage != "" && !Within(j.Directory, m.Stage)) {
			return errors.New("invalid restore journal paths")
		}
	}
	if !j.Committed {
		for i := len(j.Moves) - 1; i >= 0; i-- {
			m := j.Moves[i]
			_, err := os.Lstat(m.Rollback)
			if err == nil {
				if err = os.RemoveAll(m.Target); err != nil {
					return err
				}
				if err = os.Rename(m.Rollback, m.Target); err != nil {
					return err
				}
			} else if !os.IsNotExist(err) {
				return err
			} else if !m.HadOriginal {
				if err = os.RemoveAll(m.Target); err != nil {
					return err
				}
			}
		}
	}
	if err = os.Remove(journalPath(root)); err != nil {
		return err
	}
	if err = os.RemoveAll(j.Directory); err != nil {
		return &CleanupError{Err: err}
	}
	return nil
}

// CleanupError means data is already consistent; only private temporary copies remain.
type CleanupError struct{ Err error }

func (e *CleanupError) Error() string {
	return "restore completed, temporary cleanup pending: " + e.Err.Error()
}
func (e *CleanupError) Unwrap() error { return e.Err }

func smallBackupEntry(name string, target Target) bool {
	group := strings.SplitN(name, "/", 2)[0]
	return target.File || group == "history" || group == "mediaindex" || (strings.HasSuffix(strings.ToLower(name), ".md") && group != "public" && group != "uploads")
}
