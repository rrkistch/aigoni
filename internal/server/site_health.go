package server

import (
	"aigoni/internal/sitebackup"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *Server) siteHealthAPI(w http.ResponseWriter, r *http.Request) {
	s.settingsMu.Lock()
	site := s.cfg.Site
	s.settingsMu.Unlock()
	type check struct {
		Key     string `json:"key"`
		Label   string `json:"label"`
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	checks := []check{}
	u, err := url.Parse(site.BaseURL)
	host := ""
	if err == nil {
		host = u.Hostname()
	}
	if host == "" || host == "localhost" || host == "127.0.0.1" {
		checks = append(checks, check{"domain", "正式站点地址", "attention", "目前仍是本地地址。配置正式域名后，在设置中更新站点地址。"})
	} else {
		checks = append(checks, check{"domain", "正式站点地址", "ok", site.BaseURL})
	}
	if err == nil && u.Scheme == "https" && !s.env.CookieSecure {
		checks = append(checks, check{"cookie", "HTTPS 登录 Cookie", "attention", "正式 HTTPS 站点建议在 docker.env 启用 COOKIE_SECURE=true 并重建容器。"})
	}
	free, err := sitebackup.FreeBytes(s.root)
	if err != nil {
		checks = append(checks, check{"storage", "数据目录空间", "attention", "无法读取可用空间，请检查目录权限。"})
	} else {
		status := "ok"
		message := "空间可用于内容、构建和备份。"
		if free < 256<<20 {
			status = "attention"
			message = "剩余空间不足 256 MiB，请先清理不用的备份或扩容。"
		}
		checks = append(checks, check{"storage", "数据目录空间", status, message})
	}
	var build any
	if s.siteBuild != nil {
		s.siteBuild.mu.Lock()
		st := s.siteBuild.state
		s.siteBuild.mu.Unlock()
		build = st
		status := "ok"
		message := "公开网站已上线。"
		if st.Status != "published" {
			status = "attention"
			message = "请在概览查看当前发布进度。"
		}
		if st.Error != "" {
			message = st.Error
		}
		checks = append(checks, check{"build", "公开网站发布", status, message})
	}
	files, _ := os.ReadDir(s.backupsRoot())
	var latest time.Time
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		record, e := s.backupState(strings.TrimSuffix(file.Name(), ".json"))
		if e == nil && record.Kind == "backup" && record.Status == "ready" && record.Created.After(latest) {
			latest = record.Created
		}
	}
	status := "ok"
	message := "最近备份：" + latest.Format(time.RFC3339)
	if latest.IsZero() {
		status = "attention"
		message = "还没有后台数据备份，建议在写入个人内容后创建并下载一份。"
	}
	checks = append(checks, check{"backup", "数据备份", status, message})
	if s.restoreBlocked.Load() {
		checks = append(checks, check{"recovery", "恢复事务", "attention", "存在未完成恢复，请使用独立维护入口处理。"})
	}
	theme := map[string]string{"mode": s.env.FrontendMode, "version": "未启用"}
	if s.env.FrontendMode == "firefly" {
		source := s.activeThemeSource()
		b, e := os.ReadFile(filepath.Join(source, "package.json"))
		var pkg struct {
			Version string `json:"version"`
		}
		if e == nil && json.Unmarshal(b, &pkg) == nil {
			theme["version"] = pkg.Version
		} else {
			theme["version"] = "源码不可用"
		}
		theme["distribution"] = "source"
		if s.siteBuild != nil {
			s.siteBuild.mu.Lock()
			if s.siteBuild.state.Theme != "" && s.siteBuild.state.Theme != "source" {
				theme["distribution"] = "package"
				theme["version"] = s.siteBuild.state.Theme
			}
			s.siteBuild.mu.Unlock()
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeAdminJSON(w, 200, map[string]any{"checks": checks, "free_bytes": free, "build": build, "theme": theme, "ai_configured": s.wikiReady(), "maintenance_url": s.adminURL("maintenance")})
}
