package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"aigoni/internal/config"
	"aigoni/internal/configdoc"
	"aigoni/internal/sitebackup"
	"aigoni/internal/themebundle"
)

type themeUpload struct {
	ID       string                `json:"id"`
	Tag      string                `json:"tag"`
	Name     string                `json:"name"`
	Size     int64                 `json:"size"`
	Offset   int64                 `json:"offset"`
	Status   string                `json:"status"`
	Error    string                `json:"error,omitempty"`
	Digest   string                `json:"digest,omitempty"`
	Manifest *themebundle.Manifest `json:"manifest,omitempty"`
	Expanded int64                 `json:"expanded,omitempty"`
	Created  time.Time             `json:"created"`
	Updated  time.Time             `json:"updated"`
}

func (s *Server) themesRoot() string { return filepath.Join(s.root, ".aigoni-admin", "themes") }
func (s *Server) readThemeUpload() (themeUpload, error) {
	var state themeUpload
	data, err := os.ReadFile(filepath.Join(s.themesRoot(), "upload.json"))
	if err == nil {
		err = json.Unmarshal(data, &state)
	}
	if err == nil && (!safeRelease(state.ID) || !themebundle.TagPattern.MatchString(state.Tag) || state.Name != themebundle.AssetName(state.Tag) || state.Size <= 0 || state.Size > themebundle.MaxArchive || state.Offset < 0 || state.Offset > state.Size) {
		err = errors.New("主题上传状态损坏")
	}
	return state, err
}
func (s *Server) saveThemeUpload(state themeUpload) error {
	state.Updated = time.Now().UTC()
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return configdoc.Atomic(filepath.Join(s.themesRoot(), "upload.json"), data)
}
func (s *Server) sourceThemeDir() string {
	source := s.env.FireflySourceDir
	if source == "" {
		source = "firefly"
	}
	return config.Abs(s.root, source)
}
func (s *Server) themeSource(version string) (string, error) {
	if version == "" || version == "source" {
		return s.sourceThemeDir(), nil
	}
	if !themebundle.TagPattern.MatchString(version) {
		return "", errors.New("无效的已安装主题版本")
	}
	dir := filepath.Join(s.themesRoot(), "versions", version, "source")
	if _, err := os.Stat(filepath.Join(dir, "scripts", "aigoni-build.mjs")); err != nil {
		return "", errors.New("主题版本未安装或已损坏")
	}
	return dir, nil
}
func (s *Server) activeThemeSource() string {
	version := ""
	if s.siteBuild != nil {
		s.siteBuild.mu.Lock()
		version = s.siteBuild.state.Theme
		s.siteBuild.mu.Unlock()
	}
	source, _ := s.themeSource(version)
	return source
}
func validateThemeOverrides(source string, groups map[string]any) error {
	data, err := os.ReadFile(filepath.Join(source, "aigoni-settings-schema.json"))
	if err != nil {
		return errors.New("主题缺少配置兼容清单")
	}
	var schema map[string]adminField
	if json.Unmarshal(data, &schema) != nil {
		return errors.New("主题配置兼容清单损坏")
	}
	for name, value := range groups {
		field, ok := schema[name]
		if !ok {
			return fmt.Errorf("候选主题不支持现有配置组 %s，设置未被修改", name)
		}
		if err = validateAdminValue(field, value, name); err != nil {
			return fmt.Errorf("候选主题不兼容现有配置：%w", err)
		}
	}
	return nil
}
func (s *Server) prepareThemeWork(ctx context.Context, version string) (string, error) {
	source, err := s.themeSource(version)
	if err != nil || version == "" || version == "source" {
		return source, err
	}
	work := filepath.Join(s.themesRoot(), "work", version)
	if _, err = os.Stat(filepath.Join(work, ".installed")); err == nil {
		return work, nil
	}
	parent := filepath.Dir(work)
	if err = os.MkdirAll(parent, 0700); err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(s.themesRoot(), "versions", version, "installation.json"))
	if err != nil {
		return "", err
	}
	var record themeUpload
	if json.Unmarshal(data, &record) != nil {
		return "", errors.New("主题安装记录损坏")
	}
	free, err := sitebackup.FreeBytes(parent)
	if err != nil || free < record.Expanded+(128<<20) {
		return "", errors.New("空间不足，无法创建主题构建副本")
	}
	tmp := work + "-copy"
	if !sitebackup.Within(parent, tmp) {
		return "", errors.New("无效构建目录")
	}
	if err = os.RemoveAll(tmp); err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if err = themebundle.Copy(ctx, source, tmp); err != nil {
		return "", err
	}
	if err = os.WriteFile(filepath.Join(tmp, ".installed"), []byte(version), 0600); err != nil {
		return "", err
	}
	if err = os.Rename(tmp, work); err != nil {
		return "", err
	}
	return work, nil
}
func (s *Server) recoverThemeInstall() {
	s.themeInstallMu.Lock()
	defer s.themeInstallMu.Unlock()
	state, err := s.readThemeUpload()
	if err != nil {
		return
	}
	if state.Status == "verifying" || state.Status == "cancelling" {
		state.Status = "failed"
		state.Error = "服务重启中断了主题验证，请重新验证。"
		_ = s.saveThemeUpload(state)
	}
	if state.Status != "ready" && state.Status != "verifying" && state.Status != "cancelling" && time.Since(state.Updated) > 24*time.Hour {
		s.clearThemeUpload(state)
		return
	}
}
func (s *Server) clearThemeUpload(state themeUpload) {
	if safeRelease(state.ID) {
		_ = os.Remove(filepath.Join(s.themesRoot(), state.ID+".part"))
	}
	_ = os.Remove(filepath.Join(s.themesRoot(), "upload.json"))
}
func (s *Server) themeAPI(w http.ResponseWriter, r *http.Request) {
	if s.siteBuild == nil {
		writeAdminError(w, 409, "theme_disabled", "主题管理需要启用 Firefly 模式。")
		return
	}
	s.themeInstallMu.Lock()
	state, err := s.readThemeUpload()
	s.themeInstallMu.Unlock()
	var upload any
	if err == nil {
		upload = state
	}
	s.siteBuild.mu.Lock()
	build := s.siteBuild.state
	s.siteBuild.mu.Unlock()
	writeAdminJSON(w, 200, map[string]any{"upload": upload, "current": build.Theme, "previous": build.PreviousTheme, "pending": build.PendingTheme, "build": build, "platform": runtime.GOOS + "/" + runtime.GOARCH, "supported": runtime.GOOS == "linux" && runtime.GOARCH == "amd64", "repository": "https://github.com/" + themebundle.Repository, "maintenance_url": s.adminURL("maintenance")})
}
func (s *Server) themeUploadAPI(w http.ResponseWriter, r *http.Request) {
	if s.siteBuild == nil {
		writeAdminError(w, 409, "theme_disabled", "需要启用 Firefly。")
		return
	}
	s.themeInstallMu.Lock()
	defer s.themeInstallMu.Unlock()
	state, err := s.readThemeUpload()
	id := r.PathValue("id")
	if id == "" {
		if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
			writeAdminError(w, 409, "unsupported_platform", "首版主题包仅支持 Linux amd64；当前源码目录用法仍可继续使用。")
			return
		}
		if err == nil && (state.Status == "verifying" || state.Status == "cancelling" || (state.Status == "uploading" && time.Since(state.Updated) < 24*time.Hour)) {
			writeAdminError(w, 409, "theme_busy", "请先完成或取消当前主题上传。")
			return
		}
		var req struct {
			Tag  string `json:"tag"`
			Name string `json:"name"`
			Size int64  `json:"size"`
		}
		if !decodeAdminWikiJSON(w, r, &req, false) {
			return
		}
		if !themebundle.TagPattern.MatchString(req.Tag) || req.Name != themebundle.AssetName(req.Tag) || req.Size <= 0 || req.Size > themebundle.MaxArchive {
			writeAdminError(w, 400, "invalid_theme", "请选择本项目 Release 的主题包，最大 1 GiB，标签格式 theme-v1.0.0。")
			return
		}
		id, err = newWikiAgentRunID()
		if err != nil {
			writeAdminError(w, 500, "theme_failed", "无法创建上传。")
			return
		}
		if state.ID != "" {
			s.clearThemeUpload(state)
		}
		state = themeUpload{ID: id, Tag: req.Tag, Name: req.Name, Size: req.Size, Status: "uploading", Created: time.Now().UTC()}
		if err = s.saveThemeUpload(state); err != nil {
			writeAdminError(w, 500, "theme_failed", "无法保存上传状态。")
			return
		}
		writeAdminJSON(w, 201, state)
		return
	}
	if err != nil || state.ID != id {
		writeAdminError(w, 404, "upload_not_found", "主题上传不存在。")
		return
	}
	if r.Method == "GET" {
		writeAdminJSON(w, 200, state)
		return
	}
	if r.Method == "DELETE" {
		if s.themeInstallCancel != nil {
			s.themeInstallCancel()
			state.Status = "cancelling"
			if err = s.saveThemeUpload(state); err != nil {
				writeAdminError(w, 500, "theme_failed", "无法保存取消状态。")
				return
			}
		} else {
			s.clearThemeUpload(state)
		}
		writeAdminJSON(w, 200, map[string]bool{"cancelled": true})
		return
	}
	if state.Status != "uploading" {
		writeAdminError(w, 409, "upload_closed", "上传已结束。")
		return
	}
	offset, e := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if e != nil || offset != state.Offset {
		writeAdminError(w, 409, "offset_conflict", "请查询上传进度后续传。")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	chunk, e := io.ReadAll(r.Body)
	if e != nil || len(chunk) == 0 || state.Offset+int64(len(chunk)) > state.Size {
		writeAdminError(w, 400, "invalid_chunk", "单块最大 8 MiB，不能超过声明大小。")
		return
	}
	free, e := sitebackup.FreeBytes(s.themesRoot())
	if e != nil || free < int64(len(chunk))+(128<<20) {
		writeAdminError(w, 507, "insufficient_storage", "空间不足，请清理不用的备份后续传。")
		return
	}
	file, e := os.OpenFile(filepath.Join(s.themesRoot(), id+".part"), os.O_CREATE|os.O_WRONLY, 0600)
	if e == nil {
		_, e = file.WriteAt(chunk, state.Offset)
		if e == nil {
			e = file.Sync()
		}
		ce := file.Close()
		if e == nil {
			e = ce
		}
	}
	if e != nil {
		writeAdminError(w, 500, "upload_failed", "写入失败，旧主题未改变。")
		return
	}
	state.Offset += int64(len(chunk))
	if e = s.saveThemeUpload(state); e != nil {
		writeAdminError(w, 500, "theme_failed", "无法保存上传进度。")
		return
	}
	writeAdminJSON(w, 200, state)
}
func (s *Server) themeVerifyAPI(w http.ResponseWriter, r *http.Request) {
	if s.siteBuild == nil {
		writeAdminError(w, 409, "theme_disabled", "需要启用 Firefly。")
		return
	}
	s.themeInstallMu.Lock()
	defer s.themeInstallMu.Unlock()
	state, err := s.readThemeUpload()
	if err != nil || state.ID != r.PathValue("id") || state.Offset != state.Size || s.themeInstallCancel != nil {
		writeAdminError(w, 409, "theme_not_ready", "请完成上传并等待当前任务结束。")
		return
	}
	if state.Status != "uploading" && state.Status != "failed" && state.Status != "cancelled" {
		writeAdminError(w, 409, "theme_not_ready", "当前状态不可重新验证。")
		return
	}
	s.previewMu.Lock()
	parent := s.previewContext
	s.previewMu.Unlock()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Minute)
	s.themeInstallCancel = cancel
	state.Status = "verifying"
	state.Error = ""
	if err = s.saveThemeUpload(state); err != nil {
		cancel()
		s.themeInstallCancel = nil
		writeAdminError(w, 500, "theme_failed", "无法保存验证状态。")
		return
	}
	go func(state themeUpload) {
		defer cancel()
		err := s.installTheme(ctx, &state)
		s.themeInstallMu.Lock()
		defer s.themeInstallMu.Unlock()
		s.themeInstallCancel = nil
		if ctx.Err() != nil {
			state.Status = "cancelled"
			state.Error = "主题验证已取消，当前主题不变。"
			_ = os.Remove(filepath.Join(s.themesRoot(), state.ID+".part"))
		} else if err != nil {
			state.Status = "failed"
			state.Error = err.Error()
		} else {
			state.Status = "ready"
			_ = os.Remove(filepath.Join(s.themesRoot(), state.ID+".part"))
		}
		if e := s.saveThemeUpload(state); e != nil {
			fmt.Printf("Theme install state persistence failed: %v\n", e)
		}
	}(state)
	writeAdminJSON(w, 202, state)
}

// Note: 安装来源、工作副本与原子切换边界，见 docs/decisions/independent-theme.md。
func (s *Server) installTheme(ctx context.Context, state *themeUpload) error {
	b := s.siteBuild
	for !b.buildMu.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	defer b.buildMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	archive := filepath.Join(s.themesRoot(), state.ID+".part")
	info, err := os.Stat(archive)
	if err != nil || info.Size() != state.Size {
		return errors.New("上传文件不完整")
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	digest, err := themebundle.TrustedDigest(ctx, client, state.Tag, state.Size)
	if err != nil {
		return err
	}
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	file.Close()
	if err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != digest {
		return errors.New("主题包与 GitHub 发布校验值不一致，未执行任何脚本")
	}
	if err = s.checkThemeRuntime(ctx); err != nil {
		return err
	}
	state.Digest = digest
	versions := filepath.Join(s.themesRoot(), "versions")
	if err = os.MkdirAll(versions, 0700); err != nil {
		return err
	}
	free, err := sitebackup.FreeBytes(versions)
	if err != nil || free < 128<<20 {
		return errors.New("安装前需要保留至少 128 MiB 可用空间")
	}
	final := filepath.Join(versions, state.Tag)
	if data, e := os.ReadFile(filepath.Join(final, "installation.json")); e == nil {
		var old themeUpload
		if json.Unmarshal(data, &old) != nil || old.Digest != digest {
			return errors.New("同名主题版本与已安装版本不同，请使用新的发布标签")
		}
		state.Manifest = old.Manifest
		state.Expanded = old.Expanded
		return nil
	}
	stage := filepath.Join(versions, "install-"+state.ID)
	if !sitebackup.Within(versions, stage) {
		return errors.New("无效安装路径")
	}
	if err = os.RemoveAll(stage); err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	manifest, expanded, err := themebundle.Extract(ctx, archive, stage, state.Tag)
	if err != nil {
		return err
	}
	s.themeMu.Lock()
	settings, err := s.readThemeSettings()
	s.themeMu.Unlock()
	if err != nil {
		return err
	}
	if err = validateThemeOverrides(filepath.Join(stage, "source"), settings.Value); err != nil {
		return err
	}
	state.Manifest = &manifest
	state.Expanded = expanded
	data, _ := json.Marshal(state)
	if err = configdoc.Atomic(filepath.Join(stage, "installation.json"), data); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return os.Rename(stage, final)
}
func (s *Server) themeActivateAPI(w http.ResponseWriter, r *http.Request) {
	if s.siteBuild == nil {
		writeAdminError(w, 409, "theme_disabled", "需要启用 Firefly。")
		return
	}
	s.themeInstallMu.Lock()
	defer s.themeInstallMu.Unlock()
	if s.themeInstallCancel != nil {
		writeAdminError(w, 409, "theme_busy", "请先等待或取消安装任务。")
		return
	}
	var req struct {
		Version string `json:"version"`
	}
	if !decodeAdminWikiJSON(w, r, &req, true) {
		return
	}
	b := s.siteBuild
	b.mu.Lock()
	if b.state.PendingTheme != "" {
		b.mu.Unlock()
		writeAdminError(w, 409, "theme_busy", "主题切换正在构建。")
		return
	}
	current, previous := b.state.Theme, b.state.PreviousTheme
	b.mu.Unlock()
	target := req.Version
	if strings.HasSuffix(r.URL.Path, "/rollback") {
		target = previous
	}
	if target == "" || target == current {
		writeAdminError(w, 409, "theme_not_available", "没有可切换的主题版本。")
		return
	}
	if target != "source" {
		if !themebundle.TagPattern.MatchString(target) {
			writeAdminError(w, 400, "invalid_theme", "无效主题版本。")
			return
		}
		data, e := os.ReadFile(filepath.Join(s.themesRoot(), "versions", target, "installation.json"))
		var installed themeUpload
		if e != nil || json.Unmarshal(data, &installed) != nil || installed.Manifest == nil || installed.Manifest.Validate(target) != nil {
			writeAdminError(w, 409, "theme_not_available", "主题尚未通过可信安装校验。")
			return
		}
	}
	source, err := s.themeSource(target)
	if err != nil {
		writeAdminError(w, 409, "theme_not_available", err.Error())
		return
	}
	s.themeMu.Lock()
	settings, err := s.readThemeSettings()
	s.themeMu.Unlock()
	if err == nil {
		if target != "source" {
			err = validateThemeOverrides(source, settings.Value)
		} else {
			_, err = os.Stat(filepath.Join(source, "scripts", "aigoni-build.mjs"))
		}
	}
	if err != nil {
		writeAdminError(w, 409, "theme_incompatible", err.Error())
		return
	}
	b.mu.Lock()
	old := b.state
	b.state.PendingTheme = target
	err = b.persist()
	if err != nil {
		b.state = old
	}
	b.mu.Unlock()
	if err != nil {
		writeAdminError(w, 500, "theme_failed", "无法保存主题切换状态。")
		return
	}
	if err = s.queueSiteBuild(true); err != nil {
		writeAdminError(w, 503, "build_unavailable", "主题切换已记录，但排队失败，请重试构建。")
		return
	}
	writeAdminJSON(w, 202, map[string]any{"saved": true, "pending": target, "published": false})
}
func (s *Server) themeCancelAPI(w http.ResponseWriter, r *http.Request) {
	s.themeInstallMu.Lock()
	defer s.themeInstallMu.Unlock()
	if s.siteBuild == nil {
		writeAdminError(w, 409, "theme_disabled", "需要启用 Firefly。")
		return
	}
	b := s.siteBuild
	b.mu.Lock()
	old := b.state
	b.state.PendingTheme = ""
	err := b.persist()
	if err != nil {
		b.state = old
	}
	cancel := b.cancel
	b.mu.Unlock()
	if err != nil {
		writeAdminError(w, 500, "theme_failed", "无法取消切换。")
		return
	}
	if cancel != nil {
		cancel()
	}
	if err = s.queueSiteBuild(true); err != nil {
		writeAdminError(w, 503, "build_unavailable", "取消后无法排队当前版本，请重试。")
		return
	}
	writeAdminJSON(w, 202, map[string]bool{"cancelled": true})
}

// Cleanup holds both resources so no preview/compiler or installer loses its source.
func (s *Server) cleanupThemeVersions() {
	if s.siteBuild == nil || !s.themeInstallMu.TryLock() {
		return
	}
	defer s.themeInstallMu.Unlock()
	if s.themeInstallCancel != nil || !s.siteBuild.buildMu.TryLock() {
		return
	}
	defer s.siteBuild.buildMu.Unlock()
	s.siteBuild.mu.Lock()
	st := s.siteBuild.state
	s.siteBuild.mu.Unlock()
	keep := map[string]bool{st.Theme: true, st.PreviousTheme: true, st.PendingTheme: true}
	upload, err := s.readThemeUpload()
	if err == nil {
		keep[upload.Tag] = true
	}
	for _, folder := range []string{"versions", "work"} {
		root := filepath.Join(s.themesRoot(), folder)
		entries, _ := os.ReadDir(root)
		for _, entry := range entries {
			if !entry.IsDir() || !themebundle.TagPattern.MatchString(entry.Name()) || keep[entry.Name()] {
				continue
			}
			target := filepath.Join(root, entry.Name())
			if sitebackup.Within(root, target) {
				_ = os.RemoveAll(target)
			}
		}
	}
}
func (s *Server) themeMaintenance(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.themeInstallMu.Lock()
			state, err := s.readThemeUpload()
			if err == nil && state.Status != "ready" && state.Status != "verifying" && state.Status != "cancelling" && time.Since(state.Updated) > 24*time.Hour {
				s.clearThemeUpload(state)
			}
			s.themeInstallMu.Unlock()
			s.cleanupThemeVersions()
		}
	}
}

func (s *Server) activeThemeVersion() string {
	if s.siteBuild == nil {
		return "source"
	}
	s.siteBuild.mu.Lock()
	defer s.siteBuild.mu.Unlock()
	if s.siteBuild.state.Theme == "" {
		return "source"
	}
	return s.siteBuild.state.Theme
}

func (s *Server) checkThemeRuntime(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	node := s.env.FireflyNode
	if node == "" {
		node = "node"
	}
	out, err := exec.CommandContext(ctx, node, "--version").Output()
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(out)), "v24.") {
		return errors.New("主题包需要 Node 24，请升级后端运行环境；原源码模式不受影响")
	}
	out, err = exec.CommandContext(ctx, "pnpm", "--version").Output()
	if err != nil || strings.TrimSpace(string(out)) != themebundle.PNPM {
		return errors.New("主题包需要 pnpm " + themebundle.PNPM + "，请升级后端运行环境")
	}
	return nil
}
