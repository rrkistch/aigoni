package server

import (
	"encoding/json"
	"errors"

	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"aigoni/internal/config"
	"aigoni/internal/configdoc"
)

type mediaItem struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Path       string   `json:"path"`
	Kind       string   `json:"kind"`
	Size       int64    `json:"size"`
	Width      int      `json:"width,omitempty"`
	Height     int      `json:"height,omitempty"`
	Created    string   `json:"created"`
	References []string `json:"references,omitempty"`
}
type mediaUpload struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	Offset  int64     `json:"offset"`
	Updated time.Time `json:"updated"`
}

func (s *Server) mediaRoot() string { return filepath.Join(s.root, ".aigoni-admin", "media") }
func (s *Server) mediaUploadPath(id string) string {
	return filepath.Join(s.mediaRoot(), "pending", id+".json")
}
func mediaKind(name string) (string, int64) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif":
		return "image", 20 << 20
	case ".mp3", ".wav", ".ogg", ".m4a", ".flac":
		return "audio", 100 << 20
	case ".lrc":
		return "lyrics", 1 << 20
	}
	return "", 0
}
func (s *Server) cleanMediaUploads() {
	dir := filepath.Join(s.mediaRoot(), "pending")
	files, _ := os.ReadDir(dir)
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		p := filepath.Join(dir, f.Name())
		b, err := os.ReadFile(p)
		var u mediaUpload
		if err == nil && json.Unmarshal(b, &u) == nil && time.Since(u.Updated) > 24*time.Hour && safeRelease(u.ID) {
			_ = os.Remove(filepath.Join(dir, u.ID+".part"))
			_ = os.Remove(p)
		}
	}
}
func (s *Server) mediaUploadAPI(w http.ResponseWriter, r *http.Request) {
	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()
	s.cleanMediaUploads()
	id := r.PathValue("id")
	if id == "" {
		var req struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		}
		if !decodeAdminWikiJSON(w, r, &req, false) {
			return
		}
		kind, max := mediaKind(req.Name)
		if kind == "" || req.Size < 1 || req.Size > max || len(req.Name) > 200 || strings.ContainsAny(req.Name, "/\\\x00") {
			writeAdminError(w, 400, "invalid_media", "支持图片（20 MiB）、音频（100 MiB）或 LRC 歌词（1 MiB），文件名不能包含路径。")
			return
		}
		id, err := newWikiAgentRunID()
		if err != nil {
			writeAdminError(w, 500, "upload_failed", "无法创建上传。")
			return
		}
		u := mediaUpload{ID: id, Name: req.Name, Size: req.Size, Updated: time.Now()}
		b, _ := json.Marshal(u)
		if err = configdoc.Atomic(s.mediaUploadPath(id), b); err != nil {
			writeAdminError(w, 500, "upload_failed", "无法保存上传状态。")
			return
		}
		writeAdminJSON(w, 201, u)
		return
	}
	if !safeRelease(id) {
		writeAdminError(w, 404, "upload_not_found", "上传不存在。")
		return
	}
	b, err := os.ReadFile(s.mediaUploadPath(id))
	var u mediaUpload
	if err != nil || json.Unmarshal(b, &u) != nil {
		writeAdminError(w, 404, "upload_not_found", "上传已失效，请重新上传。")
		return
	}
	part := filepath.Join(s.mediaRoot(), "pending", id+".part")
	if r.Method == "GET" {
		writeAdminJSON(w, 200, u)
		return
	}
	if r.Method == "DELETE" {
		_ = os.Remove(part)
		_ = os.Remove(s.mediaUploadPath(id))
		w.WriteHeader(204)
		return
	}
	if r.Method == "PUT" {
		offset, e := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
		if e != nil || offset != u.Offset {
			writeAdminError(w, 409, "offset_conflict", "上传进度已变化，请查询后继续。")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
		chunk, e := io.ReadAll(r.Body)
		if e != nil || len(chunk) == 0 || u.Offset+int64(len(chunk)) > u.Size {
			writeAdminError(w, 400, "invalid_chunk", "上传分块为空、超过 8 MiB 或超过声明大小。")
			return
		}
		f, e := os.OpenFile(part, os.O_CREATE|os.O_WRONLY, 0600)
		if e != nil {
			writeAdminError(w, 500, "upload_failed", "磁盘写入失败。")
			return
		}
		_, e = f.WriteAt(chunk, u.Offset)
		if e == nil {
			e = f.Sync()
		}
		f.Close()
		if e != nil {
			writeAdminError(w, 500, "upload_failed", "磁盘写入失败。")
			return
		}
		u.Offset += int64(len(chunk))
		u.Updated = time.Now()
		b, _ = json.Marshal(u)
		if e = configdoc.Atomic(s.mediaUploadPath(id), b); e != nil {
			writeAdminError(w, 500, "upload_failed", "无法保存上传进度，请重试此分块。")
			return
		}
		writeAdminJSON(w, 200, u)
		return
	}
	if u.Offset != u.Size {
		writeAdminError(w, 409, "upload_incomplete", "文件尚未上传完整。")
		return
	}
	item, err := s.finishMedia(u, part)
	if err != nil {
		writeAdminError(w, 400, "invalid_media", err.Error())
		return
	}
	_ = os.Remove(s.mediaUploadPath(id))
	writeAdminJSON(w, 201, item)
}
func (s *Server) finishMedia(u mediaUpload, part string) (mediaItem, error) {
	item := mediaItem{ID: u.ID, Name: u.Name, Size: u.Size, Created: time.Now().UTC().Format(time.RFC3339)}
	item.Kind, _ = mediaKind(u.Name)
	f, err := os.Open(part)
	if err != nil {
		return item, errors.New("找不到上传数据，请重传。")
	}
	defer f.Close()
	if info, e := f.Stat(); e != nil || info.Size() != u.Size {
		return item, errors.New("上传数据大小不一致，请重新上传。")
	}
	head := make([]byte, 512)
	count, _ := f.Read(head)
	head = head[:count]
	mime := http.DetectContentType(head)
	ext := strings.ToLower(filepath.Ext(u.Name))
	valid := false
	switch item.Kind {
	case "image":
		expected := map[string]string{".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".gif": "image/gif", ".webp": "image/webp"}
		valid = expected[ext] != "" && mime == expected[ext]
		if ext == ".avif" {
			valid = len(head) >= 12 && string(head[4:8]) == "ftyp" && (string(head[8:12]) == "avif" || string(head[8:12]) == "avis")
		}
		if valid {
			f.Seek(0, 0)
			if cfg, _, e := image.DecodeConfig(f); e == nil {
				item.Width = cfg.Width
				item.Height = cfg.Height
			}
		}
	case "audio":
		switch ext {
		case ".mp3":
			valid = strings.HasPrefix(string(head), "ID3") || (len(head) > 1 && head[0] == 255 && head[1]&224 == 224)
		case ".wav":
			valid = len(head) >= 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "WAVE"
		case ".ogg":
			valid = strings.HasPrefix(string(head), "OggS")
		case ".flac":
			valid = strings.HasPrefix(string(head), "fLaC")
		case ".m4a":
			valid = len(head) >= 12 && string(head[4:8]) == "ftyp"
		}
	case "lyrics":
		f.Seek(0, 0)
		data, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
		valid = e == nil && len(data) <= 1<<20 && utf8.Valid(data) && !strings.ContainsRune(string(data), 0)
	}
	if !valid {
		return item, errors.New("文件内容与格式不匹配；不接受 SVG、脚本或伪装的文件。")
	}
	f.Close()
	dir := filepath.Join(s.mediaRoot(), "files")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return item, errors.New("无法创建素材目录。")
	}
	filename := u.ID + ext
	item.Path = "/_media/" + filename
	// Metadata is committed only after the immutable content has been moved.
	if err = os.Rename(part, filepath.Join(dir, filename)); err != nil {
		return item, errors.New("素材保存失败，请重新上传。")
	}
	b, _ := json.Marshal(item)
	if err = configdoc.Atomic(filepath.Join(s.mediaRoot(), "index", u.ID+".json"), b); err != nil {
		_ = os.Remove(filepath.Join(dir, filename))
		return item, errors.New("素材索引保存失败，请重新上传。")
	}
	return item, nil
}
func (s *Server) mediaReferences(asset string) []string {
	refs := []string{}
	cfg, _ := s.publicThemeSettings()
	b, _ := json.Marshal(cfg)
	if strings.Contains(string(b), asset) {
		refs = append(refs, "当前外观或模块设置")
	}
	s.settingsMu.Lock()
	roots := []string{config.Abs(s.root, s.cfg.Paths.PostsDir), config.Abs(s.root, s.cfg.Paths.PagesDir), config.Abs(s.root, s.cfg.Paths.NotesDir)}
	site, _ := json.Marshal(s.cfg.Site)
	s.settingsMu.Unlock()
	if strings.Contains(string(site), asset) {
		refs = append(refs, "站点设置")
	}
	s.contentWriteMu.Lock()
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if filepath.Ext(path) != ".md" {
				return nil
			}
			info, e := d.Info()
			if e != nil || info.Size() > 2<<20 {
				return nil
			}
			data, e := os.ReadFile(path)
			if e == nil && strings.Contains(string(data), asset) {
				rel, _ := filepath.Rel(s.root, path)
				refs = append(refs, filepath.ToSlash(rel))
			}
			return nil
		})
	}
	s.contentWriteMu.Unlock()
	// Published snapshots keep versioned assets alive until their release is retired.
	if s.siteBuild != nil {
		s.siteBuild.mu.Lock()
		published := append([]byte(nil), s.siteBuild.published...)
		s.siteBuild.mu.Unlock()
		if strings.Contains(string(published), asset) {
			refs = append(refs, "当前公开网站")
		}
	}
	return refs
}
func (s *Server) mediaAPI(w http.ResponseWriter, r *http.Request) {
	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()
	files, _ := os.ReadDir(filepath.Join(s.mediaRoot(), "index"))
	items := []mediaItem{}
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		b, e := os.ReadFile(filepath.Join(s.mediaRoot(), "index", file.Name()))
		var item mediaItem
		if e != nil || json.Unmarshal(b, &item) != nil || !validMediaItem(item) || file.Name() != item.ID+".json" {
			continue
		}
		item.References = s.mediaReferences(item.Path)
		items = append(items, item)
	}
	if r.Method == "GET" {
		writeAdminJSON(w, 200, map[string]any{"items": items})
		return
	}
	for _, item := range items {
		if item.ID != r.PathValue("id") {
			continue
		}
		if len(item.References) > 0 {
			writeAdminError(w, 409, "media_in_use", "素材仍被内容或公开版本引用，请先移除引用并等待发布完成。")
			return
		}
		if err := os.Remove(filepath.Join(s.mediaRoot(), "files", filepath.Base(item.Path))); err != nil && !os.IsNotExist(err) {
			writeAdminError(w, 500, "delete_failed", "无法删除素材。")
			return
		}
		_ = os.Remove(filepath.Join(s.mediaRoot(), "index", item.ID+".json"))
		w.WriteHeader(204)
		return
	}
	writeAdminError(w, 404, "media_not_found", "素材不存在。")
}
func (s *Server) serveMedia(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/_media/")
	ext := filepath.Ext(name)
	id := strings.TrimSuffix(name, ext)
	if !safeRelease(id) || name != filepath.Base(name) {
		http.NotFound(w, r)
		return
	}
	b, err := os.ReadFile(filepath.Join(s.mediaRoot(), "index", id+".json"))
	var item mediaItem
	if err != nil || json.Unmarshal(b, &item) != nil || item.Path != r.URL.Path {
		http.NotFound(w, r)
		return
	}
	name = filepath.Join(s.mediaRoot(), "files", name)
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if item.Kind == "lyrics" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	http.ServeFile(w, r, name)
}

func validMediaItem(item mediaItem) bool {
	if !safeRelease(item.ID) || item.Name == "" || len(item.Name) > 200 {
		return false
	}
	name := filepath.Base(item.Path)
	kind, limit := mediaKind(name)
	return kind != "" && kind == item.Kind && item.Size > 0 && item.Size <= limit && item.Path == "/_media/"+name && strings.TrimSuffix(name, filepath.Ext(name)) == item.ID
}
