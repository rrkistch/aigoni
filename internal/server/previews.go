package server

import (
	"aigoni/internal/configdoc"
	"aigoni/internal/content"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type previewState struct {
	ID      string    `json:"id"`
	Status  string    `json:"status"`
	URL     string    `json:"url"`
	Error   string    `json:"error,omitempty"`
	Created time.Time `json:"created"`
}

func (s *Server) previewStatePath(id string) string {
	return filepath.Join(s.siteBuild.root, "previews", id+".json")
}
func (s *Server) previewsAPI(w http.ResponseWriter, r *http.Request) {
	if s.siteBuild == nil {
		writeAdminError(w, 409, "preview_disabled", "预览需要启用 Firefly 模式。")
		return
	}
	if r.Method == "GET" {
		id := r.PathValue("id")
		if !strings.HasPrefix(id, "preview-") || !safeRelease(id) {
			writeAdminError(w, 404, "preview_not_found", "预览不存在。")
			return
		}
		b, e := os.ReadFile(s.previewStatePath(id))
		if e != nil {
			writeAdminError(w, 404, "preview_not_found", "预览不存在。")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
		return
	}
	var req struct {
		Type   string         `json:"type"`
		ID     string         `json:"id"`
		Groups map[string]any `json:"groups"`
	}
	if !decodeAdminWikiJSON(w, r, &req, false) {
		return
	}
	s.previewMu.Lock()
	if s.previewActive {
		s.previewMu.Unlock()
		writeAdminError(w, 409, "preview_busy", "已有预览正在生成，请等待完成。")
		return
	}
	s.previewMu.Unlock()
	raw, e := s.publicSnapshot()
	if e != nil {
		writeAdminError(w, 500, "preview_snapshot_failed", "无法读取公开内容。")
		return
	}
	var snap fireflySnapshot
	if json.Unmarshal(raw, &snap) != nil {
		writeAdminError(w, 500, "preview_snapshot_failed", "无法读取快照。")
		return
	}
	snap.PrivatePreview = true
	if snap.Theme == nil {
		snap.Theme = map[string]any{}
	}
	for group, value := range req.Groups {
		f, ok := adminSchema()[group]
		if !ok {
			writeAdminError(w, 400, "invalid_group", "预览配置不受支持。")
			return
		}
		if value == nil {
			delete(snap.Theme, group)
		} else {
			if err := validateAdminValue(f, value, f.Label); err != nil {
				writeAdminError(w, 400, "invalid_setting", err.Error())
				return
			}
			snap.Theme[group] = value
		}
	}
	page := "/"
	if req.ID != "" {
		typ, ok := adminAPIContentType(req.Type)
		if !ok || typ == content.TypeNote {
			writeAdminError(w, 400, "invalid_preview_type", "只支持公开内容类型的登录态预览。")
			return
		}
		s.contentWriteMu.Lock()
		item, err := s.repo.GetByID(req.ID, typ)
		s.contentWriteMu.Unlock()
		if err != nil {
			writeAdminError(w, 404, "content_not_found", "请先保存草稿再预览。")
			return
		}
		if item.Tags == nil {
			item.Tags = []string{}
		}
		post := fireflyPost{Slug: item.Slug, Title: item.Title, Description: item.Description, Published: item.Date, Updated: item.Lastmod, Category: item.Category, Tags: item.Tags, Image: item.CoverImage, TOC: item.TOC, Weight: item.Weight, Body: item.Body, Display: publicDisplay(item.Display)}
		replace := func(items []fireflyPost) []fireflyPost {
			out := []fireflyPost{}
			for _, v := range items {
				if v.Slug != post.Slug {
					out = append(out, v)
				}
			}
			return append(out, post)
		}
		switch typ {
		case content.TypePost:
			snap.Posts = replace(snap.Posts)
			page = "/posts/" + item.Slug + "/"
		case content.TypePage:
			snap.Pages = replace(snap.Pages)
			page = "/page/" + item.Slug + "/"
			if item.Slug == "about" {
				page = "/about/"
			}
		case content.TypeDynamic:
			snap.Dynamics = replace(snap.Dynamics)
			page = "/dynamic/"
		case content.TypeProject:
			snap.Projects = replace(snap.Projects)
			page = "/projects/" + item.Slug + "/"
		}
	}
	id, err := newWikiAgentRunID()
	if err != nil {
		writeAdminError(w, 500, "preview_failed", "无法创建预览。")
		return
	}
	id = "preview-" + id
	state := previewState{ID: id, Status: "queued", URL: "/_preview/" + id + page, Created: time.Now().UTC()}
	input := filepath.Join(s.siteBuild.root, "preview-inputs", id+".json")
	data, _ := json.Marshal(snap)
	if err = configdoc.Atomic(input, data); err != nil {
		writeAdminError(w, 500, "preview_failed", "无法保存预览快照。")
		return
	}
	s.previewMu.Lock()
	if s.previewActive {
		s.previewMu.Unlock()
		_ = os.Remove(input)
		writeAdminError(w, 409, "preview_busy", "已有预览正在生成。")
		return
	}
	s.previewActive = true
	s.previewMu.Unlock()
	b, _ := json.Marshal(state)
	if err = configdoc.Atomic(s.previewStatePath(id), b); err != nil {
		s.previewMu.Lock()
		s.previewActive = false
		s.previewMu.Unlock()
		_ = os.Remove(input)
		writeAdminError(w, 500, "preview_failed", "无法保存预览状态。")
		return
	}
	go s.buildPreview(state, input)
	writeAdminJSON(w, 202, state)
}
func (s *Server) buildPreview(state previewState, input string) {
	defer func() { s.previewMu.Lock(); s.previewActive = false; s.previewMu.Unlock() }()
	defer os.Remove(input)
	s.siteBuild.buildMu.Lock()
	defer s.siteBuild.buildMu.Unlock()
	state.Status = "building"
	b, _ := json.Marshal(state)
	_ = configdoc.Atomic(s.previewStatePath(state.ID), b)
	output := filepath.Join(s.siteBuild.root, "previews", state.ID)
	s.previewMu.Lock()
	parent := s.previewContext
	s.previewMu.Unlock()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Minute)
	defer cancel()
	err := os.MkdirAll(output, 0700)
	if err == nil {
		err = s.siteBuild.run(ctx, input, output)
	}
	if err != nil {
		state.Status = "failed"
		state.Error = "预览构建失败，公开网站未变更。请检查构建日志。"
	} else {
		state.Status = "ready"
	}
	b, _ = json.Marshal(state)
	_ = configdoc.Atomic(s.previewStatePath(state.ID), b)
}
func (s *Server) servePreview(w http.ResponseWriter, r *http.Request) {
	if s.siteBuild == nil || !s.auth.Authenticated(r) {
		http.NotFound(w, r)
		return
	}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/_preview/"), "/", 2)
	if len(parts) != 2 || !safeRelease(parts[0]) || !strings.HasPrefix(parts[0], "preview-") {
		http.NotFound(w, r)
		return
	}
	b, err := os.ReadFile(s.previewStatePath(parts[0]))
	var state previewState
	if err != nil || json.Unmarshal(b, &state) != nil || state.Status != "ready" || time.Since(state.Created) > 24*time.Hour {
		http.NotFound(w, r)
		return
	}
	relative := filepath.Clean(filepath.FromSlash(parts[1]))
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		http.NotFound(w, r)
		return
	}
	root := filepath.Join(s.siteBuild.root, "previews", parts[0])
	full := filepath.Join(root, relative)
	info, err := os.Lstat(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if info.IsDir() {
		full = filepath.Join(full, "index.html")
	}
	rel, _ := filepath.Rel(root, full)
	walk := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		walk = filepath.Join(walk, part)
		i, e := os.Lstat(walk)
		if e != nil || i.Mode()&os.ModeSymlink != 0 {
			http.NotFound(w, r)
			return
		}
	}
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Cache-Control", "no-store")
	if strings.HasSuffix(full, ".html") {
		data, err := os.ReadFile(full)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		// Keep internal navigation inside the authenticated preview; versioned assets retain their separate auth route.
		html := string(data)
		prefix := "/_preview/" + parts[0]
		html = strings.ReplaceAll(html, `href="/`, fmt.Sprintf(`href="%s/`, prefix))
		html = strings.ReplaceAll(html, `href="`+prefix+`/_firefly/`, `href="/_firefly/`)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, html)
		return
	}
	http.ServeFile(w, r, full)
}

// At startup interrupted previews are explicit failures, never reported as still running.
func (s *Server) recoverPreviews() {
	if s.siteBuild == nil {
		return
	}
	dir := filepath.Join(s.siteBuild.root, "previews")
	files, _ := os.ReadDir(dir)
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(file.Name(), ".json")
		if !safeRelease(id) || !strings.HasPrefix(id, "preview-") {
			continue
		}
		stateFile := filepath.Join(dir, file.Name())
		b, e := os.ReadFile(stateFile)
		var state previewState
		if e != nil || json.Unmarshal(b, &state) != nil {
			continue
		}
		if time.Since(state.Created) > 24*time.Hour {
			target := filepath.Join(dir, id)
			rel, e := filepath.Rel(dir, target)
			if e == nil && rel == id {
				_ = os.RemoveAll(target)
				_ = os.Remove(stateFile)
			}
			continue
		}
		if state.Status == "queued" || state.Status == "building" {
			state.Status = "failed"
			state.Error = "服务重启中断了预览，请重新生成。"
			data, _ := json.Marshal(state)
			_ = configdoc.Atomic(stateFile, data)
		}
	}
}
