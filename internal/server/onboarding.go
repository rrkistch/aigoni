package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aigoni/internal/configdoc"
	"aigoni/internal/content"
	"gopkg.in/yaml.v3"
)

type sampleOwnership struct {
	Type     content.Type `json:"type"`
	ID       string       `json:"id"`
	Revision string       `json:"revision"`
}
type onboardingSettings struct {
	Step       int               `json:"step"`
	Completed  bool              `json:"completed"`
	SampleMode string            `json:"sample_mode"`
	Imported   bool              `json:"imported"`
	Owned      []sampleOwnership `json:"owned,omitempty"`
}

func (s *Server) onboardingPath() string {
	return filepath.Join(s.root, ".aigoni-admin", "onboarding.json")
}
func (s *Server) readOnboarding() (configdoc.Document[onboardingSettings], error) {
	return configdoc.Read(s.onboardingPath(), onboardingSettings{})
}
func (s *Server) onboardingAPI(w http.ResponseWriter, r *http.Request) {
	s.onboardingMu.Lock()
	defer s.onboardingMu.Unlock()
	d, err := s.readOnboarding()
	if err != nil {
		writeAdminError(w, 500, "onboarding_unavailable", "无法读取建站进度。")
		return
	}
	if r.Method == "GET" {
		s.contentWriteMu.Lock()
		posts, e := s.repo.List(content.TypePost)
		s.contentWriteMu.Unlock()
		writeAdminJSON(w, 200, map[string]any{"revision": d.Revision, "step": d.Value.Step, "completed": d.Value.Completed, "sample_mode": d.Value.SampleMode, "imported": d.Value.Imported, "suggested": e == nil && len(posts) == 0 && !d.Value.Completed, "samples": []string{"原版动态与项目 Markdown", "关于我与友链说明", "默认音乐、友链、公开书签、相册及打赏示例"}})
		return
	}
	var req struct {
		Revision  uint64 `json:"revision"`
		Step      int    `json:"step"`
		Completed bool   `json:"completed"`
	}
	if !decodeAdminWikiJSON(w, r, &req, false) {
		return
	}
	if req.Step < 0 || req.Step > 3 {
		writeAdminError(w, 400, "invalid_step", "建站步骤无效。")
		return
	}
	next := d.Value
	next.Step = req.Step
	next.Completed = req.Completed
	updated, err := configdoc.Write(s.onboardingPath(), d, req.Revision, next)
	if errors.Is(err, configdoc.ErrConflict) {
		writeAdminError(w, 409, "revision_conflict", "建站进度已变化，请重新加载。")
		return
	}
	if err != nil {
		writeAdminError(w, 500, "onboarding_save_failed", "无法保存进度。")
		return
	}
	writeAdminJSON(w, 200, updated)
}
func (s *Server) onboardingSamplesAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Revision uint64 `json:"revision"`
		Mode     string `json:"mode"`
		Confirm  bool   `json:"confirm"`
	}
	if !decodeAdminWikiJSON(w, r, &req, false) {
		return
	}
	if !req.Confirm || (req.Mode != "keep" && req.Mode != "clear") {
		writeAdminError(w, 400, "confirmation_required", "请确认保留/导入示例，或清空未经修改的示例。")
		return
	}
	s.onboardingMu.Lock()
	defer s.onboardingMu.Unlock()
	d, err := s.readOnboarding()
	if err != nil {
		writeAdminError(w, 500, "onboarding_unavailable", "无法读取建站进度。")
		return
	}
	if req.Revision != d.Revision {
		writeAdminError(w, 409, "revision_conflict", "建站进度已变化，请重新加载。")
		return
	}
	changed, skipped := 0, 0
	if req.Mode == "keep" && !d.Value.Imported {
		source := s.activeThemeSource()
		for _, group := range []struct {
			folder string
			typ    content.Type
		}{{"dynamic", content.TypeDynamic}, {"projects", content.TypeProject}, {"spec", content.TypePage}} {
			files, e := os.ReadDir(filepath.Join(source, "src", "content", group.folder))
			if e != nil {
				writeAdminError(w, 409, "theme_source_unavailable", "主题源码尚未安装，暂时不能导入示例。")
				return
			}
			for _, entry := range files {
				if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" || entry.Type()&os.ModeSymlink != 0 {
					continue
				}
				if group.folder == "spec" && entry.Name() != "about.md" {
					continue
				}
				input, e := s.readSample(filepath.Join(source, "src", "content", group.folder, entry.Name()), group.typ)
				if e != nil {
					writeAdminError(w, 400, "sample_import_failed", e.Error())
					return
				}
				s.contentWriteMu.Lock()
				existing, getErr := s.repo.GetByID(input.ID, input.Type)
				if getErr == nil && existing != nil {
					s.contentWriteMu.Unlock()
					skipped++
					continue
				}
				if getErr != nil && !os.IsNotExist(getErr) {
					s.contentWriteMu.Unlock()
					writeAdminError(w, 500, "sample_import_failed", "读取示例目标失败。")
					return
				}
				item, rev, e := content.NewService(s.repo).Create(input)
				s.contentWriteMu.Unlock()
				if errors.Is(e, content.ErrSlugConflict) {
					skipped++
					continue
				}
				if e != nil {
					writeAdminError(w, 500, "sample_import_failed", "无法保存示例内容。")
					return
				}
				d.Value.Owned = append(d.Value.Owned, sampleOwnership{Type: item.Type, ID: item.ID, Revision: rev})
				d, e = configdoc.Write(s.onboardingPath(), d, d.Revision, d.Value)
				if e != nil {
					writeAdminError(w, 500, "sample_progress_failed", "示例已保存，但进度写入失败；不会覆盖已有文件，请检查磁盘。")
					return
				}
				changed++
			}
		}
		d.Value.Imported = true
	}
	if req.Mode == "clear" {
		remaining := []sampleOwnership{}
		for _, owned := range d.Value.Owned {
			s.contentWriteMu.Lock()
			e := content.NewService(s.repo).Delete(owned.ID, owned.Type, owned.Revision)
			s.contentWriteMu.Unlock()
			if errors.Is(e, content.ErrRevisionConflict) {
				remaining = append(remaining, owned)
				skipped++
			} else if e != nil && !os.IsNotExist(e) {
				writeAdminError(w, 500, "sample_delete_failed", "示例删除失败，请检查磁盘权限后重试；个人修改不会被覆盖。")
				return
			} else {
				changed++
			}
		}
		d.Value.Owned = remaining
		// Only unconfigured defaults are cleared. Personal edits always win.
		clear := map[string]any{"friends": []any{}, "booknav": []any{}, "music": map[string]any{"mode": "local", "local": map[string]any{"playlist": []any{}}}, "gallery": map[string]any{"albums": []any{}}, "sponsor": map[string]any{"methods": []any{}, "sponsors": []any{}, "showButtonInPost": false}, "announcement": map[string]any{"content": "", "link": map[string]any{"enable": false}}, "profile": map[string]any{"links": []any{}}, "footer": map[string]any{"useThemeHTML": false}}
		s.themeMu.Lock()
		theme, e := s.readThemeSettings()
		s.themeMu.Unlock()
		if e != nil {
			writeAdminError(w, 500, "sample_settings_failed", "无法读取当前外观设置。")
			return
		}
		for name := range clear {
			if _, edited := theme.Value[name]; edited {
				delete(clear, name)
				skipped++
			}
		}
		if len(clear) > 0 {
			if _, e = s.changeThemeSettings(theme.Revision, clear); e != nil {
				writeAdminError(w, 409, "sample_settings_conflict", "外观设置已变化；已有内容未被覆盖，请重试。")
				return
			}
		}
	}
	d.Value.SampleMode = req.Mode
	d, err = configdoc.Write(s.onboardingPath(), d, d.Revision, d.Value)
	if err != nil {
		writeAdminError(w, 500, "onboarding_save_failed", "无法保存示例处理状态。")
		return
	}
	build := "queued"
	if err = s.queueSiteBuild(false); err != nil {
		build = "failed"
	}
	if s.siteBuild == nil {
		build = "disabled"
	}
	writeAdminJSON(w, 200, map[string]any{"changed": changed, "preserved": skipped, "revision": d.Revision, "build_status": build, "message": "示例处理完成；已修改的个人内容和配置会保留。"})
}
func (s *Server) readSample(file string, typ content.Type) (content.SaveInput, error) {
	input := content.SaveInput{Type: typ, Publish: true, Date: time.Now().UTC()}
	data, err := os.ReadFile(file)
	if err != nil {
		return input, err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	meta := map[string]any{}
	body := text
	if strings.HasPrefix(text, "---\n") {
		parts := strings.SplitN(text[4:], "\n---", 2)
		if len(parts) != 2 {
			return input, errors.New("示例元数据格式无效")
		}
		if err = yaml.Unmarshal([]byte(parts[0]), &meta); err != nil {
			return input, err
		}
		body = strings.TrimLeft(parts[1], "\n")
	}
	slug := strings.TrimSuffix(filepath.Base(file), ".md")
	if v, ok := meta["slug"].(string); ok && v != "" {
		slug = v
	}
	if !safeRelease(slug) {
		return input, errors.New("示例路径标识无效")
	}
	meta["slug"] = slug
	if meta["title"] == nil {
		meta["title"] = "动态 " + slug
		if typ == content.TypePage {
			meta["title"] = "关于我"
		}
	}
	meta["date"] = meta["published"]
	if meta["date"] == nil {
		meta["date"] = time.Now().UTC().Format(time.RFC3339)
	}
	meta["publish"] = meta["draft"] != true
	meta["cover_image"] = meta["image"]
	header, _ := yaml.Marshal(meta)
	parsed, err := content.Parse([]byte("---\n"+string(header)+"---\n"+body), typ)
	if err != nil {
		return input, fmt.Errorf("示例 %s 无法读取：%w", filepath.Base(file), err)
	}
	input.Publish = parsed.Publish
	input.ID = "samples/" + slug
	input.Title = parsed.Title
	input.Slug = slug
	input.Date = parsed.Date
	input.Description = parsed.Description
	input.Tags = parsed.Tags
	input.CoverImage = parsed.CoverImage
	input.Body = body
	input.TOC = true
	display := map[string]any{}
	for _, key := range []string{"pinned", "location", "status", "link"} {
		if value, ok := meta[key]; ok {
			display[key] = value
		}
	}
	input.Display = publicDisplay(display)
	if order, ok := meta["order"].(int); ok {
		input.Weight = order
	}
	if input.CoverImage != "" && !strings.HasPrefix(input.CoverImage, "/") && !strings.HasPrefix(input.CoverImage, "http") {
		source := filepath.Join(filepath.Dir(file), filepath.FromSlash(input.CoverImage))
		rel, e := filepath.Rel(filepath.Dir(file), source)
		if e != nil || strings.HasPrefix(rel, "..") {
			return input, errors.New("示例图片越界")
		}
		info, e := os.Lstat(source)
		if e != nil || !info.Mode().IsRegular() {
			return input, errors.New("示例图片不可用")
		}
		id, e := newWikiAgentRunID()
		if e != nil {
			return input, e
		}
		s.mediaMu.Lock()
		part := filepath.Join(s.mediaRoot(), "pending", id+".part")
		e = os.MkdirAll(filepath.Dir(part), 0700)
		if e == nil {
			var from, to *os.File
			from, e = os.Open(source)
			if e == nil {
				to, e = os.OpenFile(part, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if e == nil {
					_, e = io.Copy(to, from)
					to.Close()
				}
				from.Close()
			}
		}
		if e == nil {
			var asset mediaItem
			asset, e = s.finishMedia(mediaUpload{ID: id, Name: filepath.Base(source), Size: info.Size()}, part)
			if e == nil {
				input.Body = strings.ReplaceAll(input.Body, input.CoverImage, asset.Path)
				input.CoverImage = asset.Path
			}
		}
		s.mediaMu.Unlock()
		if e != nil {
			return input, e
		}
	}
	return input, nil
}
