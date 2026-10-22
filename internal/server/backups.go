package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"aigoni/internal/config"
	"aigoni/internal/configdoc"
	"aigoni/internal/sitebackup"
	"gopkg.in/yaml.v3"
)

type backupRecord struct {
	ID           string               `json:"id"`
	Kind         string               `json:"kind"`
	Status       string               `json:"status"`
	Name         string               `json:"name,omitempty"`
	Size         int64                `json:"size"`
	Offset       int64                `json:"offset"`
	Created      time.Time            `json:"created"`
	Updated      time.Time            `json:"updated"`
	Error        string               `json:"error,omitempty"`
	Manifest     *sitebackup.Manifest `json:"manifest,omitempty"`
	SafetyBackup string               `json:"safety_backup,omitempty"`
}

func (s *Server) backupsRoot() string         { return filepath.Join(s.root, ".aigoni-admin", "backups") }
func (s *Server) backupFile(id string) string { return filepath.Join(s.backupsRoot(), id+".tar.gz") }
func (s *Server) backupState(id string) (backupRecord, error) {
	s.backupStateMu.Lock()
	defer s.backupStateMu.Unlock()
	var state backupRecord
	if !safeRelease(id) {
		return state, errors.New("invalid backup id")
	}
	b, err := os.ReadFile(filepath.Join(s.backupsRoot(), id+".json"))
	if err == nil {
		err = json.Unmarshal(b, &state)
	}
	if err == nil && state.ID != id {
		err = errors.New("backup metadata ID mismatch")
	}
	return state, err
}
func (s *Server) saveBackup(state backupRecord) error {
	// Serialize readers too: Windows cannot replace a file held open by ReadFile.
	s.backupStateMu.Lock()
	defer s.backupStateMu.Unlock()
	state.Updated = time.Now().UTC()
	b, _ := json.Marshal(state)
	return configdoc.Atomic(filepath.Join(s.backupsRoot(), state.ID+".json"), b)
}
func (s *Server) backupTargets() ([]sitebackup.Target, error) {
	s.settingsMu.Lock()
	paths := s.cfg.Paths
	s.settingsMu.Unlock()
	targets := []sitebackup.Target{}
	for _, item := range []struct{ name, path string }{{"posts", paths.PostsDir}, {"pages", paths.PagesDir}, {"notes", paths.NotesDir}, {"dynamics", filepath.Join(filepath.Dir(paths.PostsDir), "dynamics")}, {"projects", filepath.Join(filepath.Dir(paths.PostsDir), "projects")}, {"public", paths.PublicDir}, {"wiki", "wiki"}} {
		absolute := config.Abs(s.root, item.path)
		relative, _ := filepath.Rel(s.root, absolute)
		for _, part := range strings.Split(relative, string(filepath.Separator)) {
			if strings.HasPrefix(part, ".") {
				return nil, errors.New("数据路径不能覆盖隐藏配置目录")
			}
		}
		targets = append(targets, sitebackup.Target{Name: item.name, Path: absolute})
	}
	uploads := config.Abs(s.root, paths.UploadsDir)
	public := config.Abs(s.root, paths.PublicDir)
	if uploads != public && !sitebackup.Within(public, uploads) {
		targets = append(targets, sitebackup.Target{Name: "uploads", Path: uploads})
	}
	targets = append(targets, sitebackup.Target{Name: "config", Path: filepath.Join(s.root, "config.yaml"), File: true}, sitebackup.Target{Name: "theme", Path: s.themeSettingsPath(), File: true}, sitebackup.Target{Name: "onboarding", Path: s.onboardingPath(), File: true}, sitebackup.Target{Name: "history", Path: filepath.Join(s.root, ".aigoni-admin", "theme-history")}, sitebackup.Target{Name: "mediafiles", Path: filepath.Join(s.mediaRoot(), "files")}, sitebackup.Target{Name: "mediaindex", Path: filepath.Join(s.mediaRoot(), "index")})
	return targets, sitebackup.ValidateTargets(s.root, targets)
}
func (s *Server) hasWikiTask(writeOnly bool) bool {
	s.wikiMu.Lock()
	defer s.wikiMu.Unlock()
	for _, run := range s.wikiAgentRuns {
		if (run.Status == "pending" || run.Status == "running") && (!writeOnly || run.Kind != "ask") {
			return true
		}
	}
	return false
}

// Note: 恢复替换当前数据且保留安全备份，见 docs/decisions/data-backup.md。
func (s *Server) startBackupTask(state backupRecord, fn func(*backupRecord) error) bool {
	s.backupMu.Lock()
	if s.backupActive != "" {
		s.backupMu.Unlock()
		return false
	}
	s.backupActive = state.ID
	s.backupMu.Unlock()
	if err := s.saveBackup(state); err != nil {
		s.backupMu.Lock()
		s.backupActive = ""
		s.backupMu.Unlock()
		return false
	}
	go func() {
		defer func() { s.backupMu.Lock(); s.backupActive = ""; s.backupMu.Unlock() }()
		if err := fn(&state); err != nil {
			state.Status = "failed"
			state.Error = err.Error()
		}
		if err := s.saveBackup(state); err != nil {
			log.Printf("Backup task %s completed with status %s but metadata persistence failed: %v", state.ID, state.Status, err)
		}
	}()
	return true
}
func (s *Server) backupsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		files, _ := os.ReadDir(s.backupsRoot())
		items := []backupRecord{}
		for _, file := range files {
			if strings.HasSuffix(file.Name(), ".json") {
				state, e := s.backupState(strings.TrimSuffix(file.Name(), ".json"))
				if e == nil {
					items = append(items, state)
				}
			}
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Created.After(items[j].Created) })
		writeAdminJSON(w, 200, map[string]any{"items": items, "contains_private_data": true, "credentials_included": false, "recovery_required": s.restoreBlocked.Load(), "maintenance_url": s.adminURL("maintenance")})
		return
	}
	id, err := newWikiAgentRunID()
	if err != nil {
		writeAdminError(w, 500, "backup_failed", "无法创建备份任务。")
		return
	}
	state := backupRecord{ID: id, Kind: "backup", Status: "queued", Created: time.Now().UTC()}
	if !s.startBackupTask(state, func(state *backupRecord) error {
		if !s.maintenanceMu.TryLock() {
			return errors.New("有内容正在保存，请稍后重试")
		}
		defer s.maintenanceMu.Unlock()
		if s.hasWikiTask(true) {
			return errors.New("请先等待或取消正在写入知识库的 AI 任务")
		}
		s.wikiWriteMu.Lock()
		defer s.wikiWriteMu.Unlock()
		targets, err := s.backupTargets()
		if err != nil {
			return err
		}
		state.Status = "creating"
		if err := s.saveBackup(*state); err != nil {
			return err
		}
		manifest, err := sitebackup.Create(s.root, targets, s.backupFile(state.ID), s.activeThemeVersion())
		if err != nil {
			return err
		}
		info, err := os.Stat(s.backupFile(state.ID))
		if err != nil {
			return err
		}
		state.Manifest = &manifest
		state.Size = info.Size()
		state.Status = "ready"
		return nil
	}) {
		writeAdminError(w, 409, "backup_busy", "已有备份/恢复任务或无法保存任务状态。")
		return
	}
	writeAdminJSON(w, 202, state)
}
func (s *Server) backupItemAPI(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	state, err := s.backupState(id)
	if err != nil {
		writeAdminError(w, 404, "backup_not_found", "备份不存在。")
		return
	}
	if r.Method == "GET" {
		writeAdminJSON(w, 200, state)
		return
	}
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	if s.backupActive != "" {
		writeAdminError(w, 409, "backup_busy", "此任务仍在执行。")
		return
	}
	for _, file := range []string{s.backupFile(id), filepath.Join(s.backupsRoot(), id+".part")} {
		if e := os.Remove(file); e != nil && !os.IsNotExist(e) {
			writeAdminError(w, 500, "backup_delete_failed", "备份文件删除失败。")
			return
		}
	}
	directory := filepath.Join(s.root, ".aigoni-admin", "restores", id)
	if sitebackup.Within(filepath.Join(s.root, ".aigoni-admin", "restores"), directory) {
		if e := os.RemoveAll(directory); e != nil {
			writeAdminError(w, 500, "backup_delete_failed", "恢复暂存数据清理失败。")
			return
		}
	}
	if e := os.Remove(filepath.Join(s.backupsRoot(), id+".json")); e != nil && !os.IsNotExist(e) {
		writeAdminError(w, 500, "backup_delete_failed", "备份记录删除失败。")
		return
	}
	w.WriteHeader(204)
}
func (s *Server) backupDownload(w http.ResponseWriter, r *http.Request) {
	state, err := s.backupState(r.PathValue("id"))
	if err != nil || state.Status != "ready" || state.Kind != "backup" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="aigoni-%s.tar.gz"`, state.ID))
	http.ServeFile(w, r, s.backupFile(state.ID))
}
func (s *Server) backupUploadAPI(w http.ResponseWriter, r *http.Request) {
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	id := r.PathValue("id")
	if id == "" {
		var req struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		}
		if !decodeAdminWikiJSON(w, r, &req, false) {
			return
		}
		if req.Size <= 0 || req.Size > 4<<30 || !strings.HasSuffix(strings.ToLower(req.Name), ".tar.gz") || len(req.Name) > 200 || strings.ContainsAny(req.Name, "/\\\x00") {
			writeAdminError(w, 400, "invalid_backup", "请选择本站生成的 tar.gz 数据备份，最大 4 GiB。")
			return
		}
		id, err := newWikiAgentRunID()
		if err != nil {
			writeAdminError(w, 500, "upload_failed", "无法分配上传。")
			return
		}
		state := backupRecord{ID: id, Kind: "restore", Status: "uploading", Name: req.Name, Size: req.Size, Created: time.Now().UTC()}
		if err = s.saveBackup(state); err != nil {
			writeAdminError(w, 500, "upload_failed", "无法保存上传状态。")
			return
		}
		writeAdminJSON(w, 201, state)
		return
	}
	state, err := s.backupState(id)
	if err != nil || state.Kind != "restore" {
		writeAdminError(w, 404, "upload_not_found", "恢复上传不存在。")
		return
	}
	if r.Method == "GET" {
		writeAdminJSON(w, 200, state)
		return
	}
	if state.Status != "uploading" {
		writeAdminError(w, 409, "upload_closed", "上传已经结束。")
		return
	}
	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || offset != state.Offset {
		writeAdminError(w, 409, "offset_conflict", "上传进度已变化，请查询后续传。")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	chunk, err := io.ReadAll(r.Body)
	if err != nil || len(chunk) == 0 || state.Offset+int64(len(chunk)) > state.Size {
		writeAdminError(w, 400, "invalid_chunk", "无效分块，单块最大 8 MiB。")
		return
	}
	free, spaceErr := sitebackup.FreeBytes(s.backupsRoot())
	if spaceErr != nil || free < int64(len(chunk))+(128<<20) {
		writeAdminError(w, 507, "insufficient_storage", "可用空间不足，请清理不需要的备份后续传。")
		return
	}
	file, err := os.OpenFile(filepath.Join(s.backupsRoot(), id+".part"), os.O_CREATE|os.O_WRONLY, 0600)
	if err == nil {
		_, err = file.WriteAt(chunk, state.Offset)
		if err == nil {
			err = file.Sync()
		}
		file.Close()
	}
	if err != nil {
		writeAdminError(w, 500, "upload_failed", "磁盘写入失败。")
		return
	}
	state.Offset += int64(len(chunk))
	if err = s.saveBackup(state); err != nil {
		writeAdminError(w, 500, "upload_failed", "无法保存上传进度。")
		return
	}
	writeAdminJSON(w, 200, state)
}
func (s *Server) backupValidateAPI(w http.ResponseWriter, r *http.Request) {
	state, err := s.backupState(r.PathValue("id"))
	if err != nil || state.Kind != "restore" || state.Status == "restoring" || state.Offset != state.Size {
		writeAdminError(w, 409, "upload_incomplete", "请先完成备份上传。")
		return
	}
	part := filepath.Join(s.backupsRoot(), state.ID+".part")
	input := part
	if state.Status != "uploading" {
		input = s.backupFile(state.ID)
	}
	info, err := os.Stat(input)
	if err != nil || info.Size() != state.Size {
		writeAdminError(w, 400, "invalid_backup", "上传数据大小不一致。")
		return
	}
	state.Status = "validating"
	state.Error = ""
	state.SafetyBackup = ""
	if !s.startBackupTask(state, func(state *backupRecord) error {
		if input == part {
			if err := os.Rename(part, s.backupFile(state.ID)); err != nil {
				return err
			}
		}
		targets, err := s.backupTargets()
		if err != nil {
			return err
		}
		work := filepath.Join(s.root, ".aigoni-admin", "restores", state.ID)
		if !sitebackup.Within(filepath.Join(s.root, ".aigoni-admin", "restores"), work) {
			return errors.New("invalid restore directory")
		}
		if err = os.RemoveAll(work); err != nil {
			return err
		}
		if err = os.MkdirAll(work, 0700); err != nil {
			return err
		}
		manifest, err := sitebackup.Extract(s.backupFile(state.ID), filepath.Join(work, "stage"), targets)
		if err != nil {
			return err
		}
		cfg, err := config.Load(filepath.Join(work, "stage", "config"))
		if err != nil {
			return errors.New("备份中的站点配置无效")
		}
		s.settingsMu.Lock()
		cfg.Paths = s.cfg.Paths
		s.settingsMu.Unlock()
		if fields := validateSettingsConfig(cfg, s.env.UploadAllowedExts); len(fields) > 0 {
			return errors.New("备份配置与当前程序不兼容")
		}
		b, err := yaml.Marshal(cfg)
		if err != nil {
			return err
		}
		if err = configdoc.Atomic(filepath.Join(work, "stage", "config"), b); err != nil {
			return err
		}
		if err = s.validateBackupData(filepath.Join(work, "stage"), manifest); err != nil {
			return err
		}
		inventory, err := sitebackup.Inventory(s.root, targets)
		if err != nil {
			return err
		}
		if err = configdoc.Atomic(filepath.Join(work, "inventory.json"), inventory); err != nil {
			return err
		}
		state.Manifest = &manifest
		state.Status = "validated"
		return nil
	}) {
		writeAdminError(w, 409, "backup_busy", "已有维护任务，请等待完成。")
		return
	}
	writeAdminJSON(w, 202, state)
}
func (s *Server) backupRestoreAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Confirm bool `json:"confirm"`
	}
	if !decodeAdminWikiJSON(w, r, &req, false) {
		return
	}
	if !req.Confirm {
		writeAdminError(w, 400, "confirmation_required", "恢复将替换当前数据，请确认影响后执行。")
		return
	}
	state, err := s.backupState(r.PathValue("id"))
	if err != nil || state.Status != "validated" || state.Manifest == nil {
		writeAdminError(w, 409, "backup_not_validated", "请先验证恢复包。")
		return
	}
	state.Status = "queued-restore"
	state.Error = ""
	if !s.startBackupTask(state, func(state *backupRecord) error {
		if !s.maintenanceMu.TryLock() {
			return errors.New("有内容正在保存，请稍后重试")
		}
		defer s.maintenanceMu.Unlock()
		if s.hasWikiTask(false) {
			return errors.New("请先等待或取消知识库任务")
		}
		s.wikiWriteMu.Lock()
		defer s.wikiWriteMu.Unlock()
		if s.siteBuild != nil {
			if !s.siteBuild.buildMu.TryLock() {
				return errors.New("请先等待网站或预览构建完成")
			}
			defer s.siteBuild.buildMu.Unlock()
		}
		targets, err := s.backupTargets()
		if err != nil {
			return err
		}
		work := filepath.Join(s.root, ".aigoni-admin", "restores", state.ID)
		before, err := os.ReadFile(filepath.Join(work, "inventory.json"))
		if err != nil {
			return err
		}
		current, err := sitebackup.Inventory(s.root, targets)
		if err != nil {
			return err
		}
		if !bytes.Equal(before, current) {
			return errors.New("验证后网站数据发生变化，请重新验证恢复包，防止覆盖未确认的修改")
		}
		safetyID, err := newWikiAgentRunID()
		if err != nil {
			return err
		}
		state.Status = "safety-backup"
		if err := s.saveBackup(*state); err != nil {
			return err
		}
		manifest, err := sitebackup.Create(s.root, targets, s.backupFile(safetyID), s.activeThemeVersion())
		if err != nil {
			return err
		}
		info, err := os.Stat(s.backupFile(safetyID))
		if err != nil {
			return err
		}
		safety := backupRecord{ID: safetyID, Kind: "backup", Status: "ready", Created: time.Now().UTC(), Size: info.Size(), Manifest: &manifest}
		if err = s.saveBackup(safety); err != nil {
			return err
		}
		state.SafetyBackup = safetyID
		if !s.restoreMu.TryLock() {
			return errors.New("有页面或文件正在读取，请稍后重试")
		}
		defer s.restoreMu.Unlock()
		state.Status = "restoring"
		if err := s.saveBackup(*state); err != nil {
			return err
		}
		if s.siteBuild != nil {
			s.siteBuild.mu.Lock()
			old := s.siteBuild.state
			s.siteBuild.state.Blocked = true
			err = s.siteBuild.persist()
			if err != nil {
				s.siteBuild.state = old
			}
			s.siteBuild.mu.Unlock()
			if err != nil {
				return errors.New("无法保存维护状态，恢复尚未执行")
			}
		}
		s.settingsMu.Lock()
		s.contentWriteMu.Lock()
		s.themeMu.Lock()
		s.mediaMu.Lock()
		s.onboardingMu.Lock()
		err = sitebackup.Restore(s.root, filepath.Join(work, "stage"), targets, *state.Manifest)
		var cleanup *sitebackup.CleanupError
		restored := err == nil || errors.As(err, &cleanup)
		if restored {
			if cleanup != nil {
				state.Error = "数据已恢复；临时副本清理未完成，请检查目录权限。"
			}
			err = s.reloadConfig()
			if err != nil {
				s.restoreBlocked.Store(true)
			}
		} else if _, journalErr := os.Stat(filepath.Join(s.root, ".aigoni-admin", "restore-journal.json")); journalErr == nil {
			s.restoreBlocked.Store(true)
		}
		s.onboardingMu.Unlock()
		s.mediaMu.Unlock()
		s.themeMu.Unlock()
		s.contentWriteMu.Unlock()
		s.settingsMu.Unlock()
		if err != nil {
			return err
		}
		if s.siteBuild != nil {
			s.siteBuild.mu.Lock()
			s.siteBuild.state.Blocked = true
			_ = s.siteBuild.persist()
			s.siteBuild.mu.Unlock()
			if err = s.queueSiteBuild(true); err != nil {
				state.Status = "restored"
				state.Error = "数据已恢复，但网站重建排队失败；请在概览重试"
				return nil
			}
		}
		state.Status = "restored"
		return nil
	}) {
		writeAdminError(w, 409, "backup_busy", "已有备份或恢复任务。")
		return
	}
	writeAdminJSON(w, 202, state)
}

// Only HTTP mutations are paused during backup. Restore also excludes in-flight readers.
func (s *Server) maintenanceGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		exempt := strings.HasPrefix(r.URL.Path, adminAPIBasePath+"/backups") || r.URL.Path == adminAPIBasePath+"/session"
		if r.URL.Path == s.adminURL("maintenance") {
			next.ServeHTTP(w, r)
			return
		}
		if s.restoreBlocked.Load() {
			if r.URL.Path == adminAPIBasePath+"/session" || r.URL.Path == adminAPIBasePath+"/backups/recover" || (exempt && r.Method == "GET") {
				next.ServeHTTP(w, r)
				return
			}
			writeAdminError(w, 503, "recovery_required", "上次恢复尚未完成，请进入维护入口恢复一致状态。")
			return
		}
		if exempt {
			next.ServeHTTP(w, r)
			return
		}
		if !s.restoreMu.TryRLock() {
			writeAdminError(w, 503, "restoring", "网站正在恢复数据，请稍后重试。")
			return
		}
		defer s.restoreMu.RUnlock()
		if isAdminWriteMethod(r.Method) {
			if !s.maintenanceMu.TryRLock() {
				writeAdminError(w, 409, "maintenance_busy", "备份或恢复进行中，暂时不能保存内容。")
				return
			}
			defer s.maintenanceMu.RUnlock()
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverBackupTasks() {
	if _, err := os.Stat(filepath.Join(s.root, ".aigoni-admin", "restore-journal.json")); err == nil {
		s.restoreBlocked.Store(true)
	}
	files, _ := os.ReadDir(s.backupsRoot())
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		if strings.HasPrefix(file.Name(), ".backup-") {
			_ = os.Remove(filepath.Join(s.backupsRoot(), file.Name()))
			continue
		}
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		state, err := s.backupState(strings.TrimSuffix(file.Name(), ".json"))
		if err != nil {
			continue
		}
		switch state.Status {
		case "queued", "creating", "validating", "queued-restore", "safety-backup", "restoring":
			state.Status = "failed"
			state.Error = "服务重启中断了任务。恢复事务会先回滚到一致状态；可重新验证恢复包。"
			_ = s.saveBackup(state)
		}
	}
}
func (s *Server) backupRecoverAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Confirm bool `json:"confirm"`
	}
	if !decodeAdminWikiJSON(w, r, &req, false) {
		return
	}
	if !req.Confirm {
		writeAdminError(w, 400, "confirmation_required", "请确认恢复未完成的事务。")
		return
	}
	if !s.maintenanceMu.TryLock() {
		writeAdminError(w, 409, "maintenance_busy", "请等待当前维护操作。")
		return
	}
	defer s.maintenanceMu.Unlock()
	if !s.restoreMu.TryLock() {
		writeAdminError(w, 409, "maintenance_busy", "请等待当前读取结束。")
		return
	}
	defer s.restoreMu.Unlock()
	err := sitebackup.Recover(s.root)
	var cleanup *sitebackup.CleanupError
	if err != nil && !errors.As(err, &cleanup) {
		writeAdminError(w, 500, "recovery_failed", "恢复仍未完成，请检查 Docker 日志和数据目录权限后重试。")
		return
	}
	s.settingsMu.Lock()
	err = s.reloadConfig()
	s.settingsMu.Unlock()
	if err != nil {
		writeAdminError(w, 500, "recovery_failed", "配置仍无法读取，请检查数据目录。")
		return
	}
	if s.siteBuild != nil {
		s.siteBuild.mu.Lock()
		s.siteBuild.state.Blocked = true
		err = s.siteBuild.persist()
		s.siteBuild.mu.Unlock()
		if err == nil {
			err = s.queueSiteBuild(true)
		}
		if err != nil {
			writeAdminError(w, 500, "recovery_failed", "数据一致，但维护状态或重建任务无法保存，请检查磁盘后重试。")
			return
		}
	}
	s.restoreBlocked.Store(false)
	writeAdminJSON(w, 200, map[string]any{"recovered": true, "message": "数据已恢复一致状态，公开网站等待重新构建。"})
}
