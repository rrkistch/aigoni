package server

import (
	"aigoni/internal/config"
	"aigoni/internal/configdoc"
	"aigoni/internal/sitebackup"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (s *Server) mediaContentRootsLocked() []string {
	posts := config.Abs(s.root, s.cfg.Paths.PostsDir)
	return []string{posts, config.Abs(s.root, s.cfg.Paths.PagesDir), config.Abs(s.root, s.cfg.Paths.NotesDir), filepath.Join(filepath.Dir(posts), "dynamics"), filepath.Join(filepath.Dir(posts), "projects"), filepath.Join(s.root, "wiki")}
}
func mediaFileContains(file, asset string) (bool, error) {
	f, err := os.Open(file)
	if err != nil {
		return false, err
	}
	defer f.Close()
	buffer := make([]byte, 64<<10)
	tail := []byte{}
	for {
		n, e := f.Read(buffer)
		data := append(tail, buffer[:n]...)
		if bytes.Contains(data, []byte(asset)) {
			return true, nil
		}
		keep := len(asset) - 1
		if keep > len(data) {
			keep = len(data)
		}
		tail = append([]byte(nil), data[len(data)-keep:]...)
		if e == io.EOF {
			return false, nil
		}
		if e != nil {
			return false, e
		}
	}
}
func walkMediaReferences(roots []string, asset string, fn func(string) error) error {
	for _, root := range roots {
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(root, func(file string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() && file != root && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if entry.IsDir() || strings.ToLower(filepath.Ext(file)) != ".md" {
				return nil
			}
			found, err := mediaFileContains(file, asset)
			if err != nil {
				return err
			}
			if found {
				return fn(file)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
func (s *Server) mediaRetainedReferences(asset string) []string {
	refs := []string{}
	if err := walkMediaReferences([]string{filepath.Join(s.root, "wiki", ".backups")}, asset, func(string) error {
		if len(refs) == 0 {
			refs = append(refs, "保留的Wiki备份")
		}
		return nil
	}); err != nil {
		refs = append(refs, "Wiki备份引用检查失败")
	}
	s.themeMu.Lock()
	entries, err := os.ReadDir(filepath.Join(s.root, ".aigoni-admin", "theme-history"))
	if err != nil && !os.IsNotExist(err) {
		refs = append(refs, "设置历史不可读，暂不能确认可删除")
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		found, e := mediaFileContains(filepath.Join(s.root, ".aigoni-admin", "theme-history", entry.Name()), asset)
		if e != nil {
			refs = append(refs, "设置历史引用检查失败")
			break
		}
		if found {
			refs = append(refs, "可恢复的设置历史")
			break
		}
	}
	s.themeMu.Unlock()
	s.integrationMu.Lock()
	entries, err = os.ReadDir(filepath.Join(s.assistantRoot(), "operations"))
	if err != nil && !os.IsNotExist(err) {
		refs = append(refs, "助手候选不可读，暂不能确认可删除")
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		op, e := s.readAssistantOperation(strings.TrimSuffix(entry.Name(), ".json"))
		if e != nil {
			refs = append(refs, "助手候选引用检查失败")
			break
		}
		if op.Status != "pending" && op.Status != "applying" && op.Status != "needs_review" {
			continue
		}
		data, _ := json.Marshal(op.After)
		if bytes.Contains(data, []byte(asset)) {
			refs = append(refs, "尚未处理的助手候选")
			break
		}
	}
	s.integrationMu.Unlock()
	return refs
}
func (s *Server) readMediaItem(id string) (mediaItem, error) {
	var item mediaItem
	if !safeRelease(id) {
		return item, errors.New("invalid media ID")
	}
	data, err := os.ReadFile(filepath.Join(s.mediaRoot(), "index", id+".json"))
	if err == nil {
		err = json.Unmarshal(data, &item)
	}
	if err == nil && (!validMediaItem(item) || item.ID != id) {
		err = errors.New("invalid media record")
	}
	return item, err
}
func (s *Server) mediaReplaceAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ReplacementID       string `json:"replacement_id"`
		Revision            uint64 `json:"revision"`
		ReplacementRevision uint64 `json:"replacement_revision"`
		Confirm             bool   `json:"confirm"`
	}
	if !decodeAdminWikiJSON(w, r, &req, false) {
		return
	}
	if !req.Confirm {
		writeAdminError(w, 400, "confirmation_required", "替换会更新执行时的所有当前可编辑引用，请确认。")
		return
	}
	if !s.maintenanceMu.TryLock() {
		writeAdminError(w, 409, "media_busy", "有内容保存或备份正在进行，请稍后重试。")
		return
	}
	defer s.maintenanceMu.Unlock()
	if s.hasWikiTask(true) {
		writeAdminError(w, 409, "wiki_busy", "请先等待或停止正在写Wiki的任务。")
		return
	}
	if !s.restoreMu.TryLock() {
		writeAdminError(w, 409, "media_busy", "有页面或文件正在读取，请稍后重试替换；上传的新素材会保留。")
		return
	}
	defer s.restoreMu.Unlock()
	s.settingsMu.Lock()
	s.contentWriteMu.Lock()
	s.themeMu.Lock()
	s.mediaMu.Lock()
	var changed int
	var err error
	func() {
		defer s.mediaMu.Unlock()
		defer s.themeMu.Unlock()
		defer s.contentWriteMu.Unlock()
		defer s.settingsMu.Unlock()
		old, e := s.readMediaItem(r.PathValue("id"))
		if e != nil {
			err = errors.New("旧素材不存在")
			return
		}
		replacement, e := s.readMediaItem(req.ReplacementID)
		if e != nil || replacement.ID == old.ID || replacement.Kind != old.Kind {
			err = errors.New("替代素材必须是另一份已上传的同类型素材")
			return
		}
		if old.Revision != req.Revision || replacement.Revision != req.ReplacementRevision {
			err = configdoc.ErrConflict
			return
		}
		files := map[string][]byte{}
		modes := map[string]os.FileMode{}
		var total int64
		add := func(file string, data []byte) error {
			if len(files) >= 1000 || total+int64(len(data)) > 128<<20 {
				return errors.New("本次替换涉及文件过多或超过128MiB，请分批在内容编辑器修改")
			}
			info, e := os.Lstat(file)
			mode := os.FileMode(0600)
			if e == nil {
				if !info.Mode().IsRegular() {
					return errors.New("引用目标不是普通文件")
				}
				mode = info.Mode().Perm()
			} else if !os.IsNotExist(e) {
				return e
			}
			files[file] = data
			modes[file] = mode
			total += int64(len(data))
			return nil
		}
		replaceFile := func(file string) error {
			info, e := os.Lstat(file)
			if e != nil {
				return e
			}
			if !info.Mode().IsRegular() || info.Size() > 8<<20 {
				return errors.New("只自动替换8MiB以内的普通文本文件，请手动处理较大文档")
			}
			data, e := os.ReadFile(file)
			if e != nil {
				return e
			}
			next := bytes.ReplaceAll(data, []byte(old.Path), []byte(replacement.Path))
			if bytes.Equal(data, next) {
				return nil
			}
			return add(file, next)
		}
		err = walkMediaReferences(s.mediaContentRootsLocked(), old.Path, replaceFile)
		if err != nil {
			return
		}
		configChanged := false
		configPath := filepath.Join(s.root, "config.yaml")
		if found, e := mediaFileContains(configPath, old.Path); e == nil && found {
			if err = replaceFile(configPath); err != nil {
				return
			}
			configChanged = true
		} else if e != nil && !os.IsNotExist(e) {
			err = e
			return
		}
		doc, e := s.readThemeSettings()
		if e != nil {
			err = e
			return
		}
		value, _ := json.Marshal(doc.Value)
		if bytes.Contains(value, []byte(old.Path)) {
			previous, _ := json.Marshal(doc)
			history := filepath.Join(s.root, ".aigoni-admin", "theme-history", fmt.Sprintf("%020d.json", doc.Revision))
			if err = add(history, previous); err != nil {
				return
			}
			value = bytes.ReplaceAll(value, []byte(old.Path), []byte(replacement.Path))
			if err = json.Unmarshal(value, &doc.Value); err != nil {
				return
			}
			if doc.Revision == ^uint64(0) {
				err = errors.New("配置版本超出范围")
				return
			}
			doc.Revision++
			next, _ := json.MarshalIndent(doc, "", "  ")
			if err = add(s.themeSettingsPath(), next); err != nil {
				return
			}
		}
		changed = len(files)
		old.ReplacedBy = replacement.ID
		old.Revision++
		old.References = nil
		record, _ := json.Marshal(old)
		if err = add(filepath.Join(s.mediaRoot(), "index", old.ID+".json"), record); err != nil {
			return
		}
		id, e := newWikiAgentRunID()
		if e != nil {
			err = e
			return
		}
		directory := filepath.Join(s.root, ".aigoni-admin", "restores", "media-"+id)
		stage := filepath.Join(directory, "stage")
		if !sitebackup.Within(filepath.Join(s.root, ".aigoni-admin", "restores"), directory) {
			err = errors.New("invalid media transaction path")
			return
		}
		if err = os.MkdirAll(stage, 0700); err != nil {
			return
		}
		defer func() {
			if _, e := os.Stat(filepath.Join(s.root, ".aigoni-admin", "restore-journal.json")); os.IsNotExist(e) {
				_ = os.RemoveAll(directory)
			}
		}()

		targets := []sitebackup.Target{}
		manifest := sitebackup.Manifest{Version: 1, Roots: map[string]bool{}}
		i := 0
		for file, data := range files {
			name := fmt.Sprintf("file%d", i)
			i++
			if err = configdoc.Atomic(filepath.Join(stage, name), data); err != nil {
				return
			}
			if err = os.Chmod(filepath.Join(stage, name), modes[file]); err != nil {
				return
			}
			targets = append(targets, sitebackup.Target{Name: name, Path: file, File: true})
			manifest.Roots[name] = true
		}
		err = sitebackup.Restore(s.root, stage, targets, manifest)
		var cleanup *sitebackup.CleanupError
		if errors.As(err, &cleanup) {
			err = nil
		}
		if err != nil {
			if _, e := os.Stat(filepath.Join(s.root, ".aigoni-admin", "restore-journal.json")); e == nil {
				s.restoreBlocked.Store(true)
			}
			return
		}
		if configChanged {
			err = s.reloadConfig()
			if err != nil {
				s.restoreBlocked.Store(true)
				return
			}
		}
		s.trimThemeHistory()
	}()
	if err != nil {
		writeAdminError(w, 409, "media_replace_failed", err.Error())
		return
	}
	buildErr := s.queueSiteBuild(false)
	status := "queued"
	if buildErr != nil {
		status = "failed"
	}
	if s.siteBuild == nil {
		status = "disabled"
	}
	writeAdminJSON(w, 200, map[string]any{"saved": true, "changed_files": changed, "build_status": status, "message": "当前引用已替换；历史、候选和旧公开版本保留原文件，等待新版本上线。"})
}
