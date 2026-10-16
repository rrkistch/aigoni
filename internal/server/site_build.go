package server

// Note: Firefly stays upstream-first; see docs/decisions/firefly-integration.md.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"aigoni/internal/config"
	"aigoni/internal/content"
)

type fireflyPost struct {
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Published   time.Time `json:"published"`
	Updated     time.Time `json:"updated"`
	Category    string    `json:"category"`
	Tags        []string  `json:"tags"`
	Image       string    `json:"image"`
	TOC         bool      `json:"toc"`
	Weight      int       `json:"weight"`
	Body        string    `json:"body"`
}
type fireflySnapshot struct {
	Site      map[string]any `json:"site"`
	Posts     []fireflyPost  `json:"posts"`
	Pages     []fireflyPost  `json:"pages"`
	AdminPath string         `json:"adminPath"`
}
type siteBuildStatus struct {
	Enabled           bool      `json:"enabled"`
	Status            string    `json:"status"`
	Revision          uint64    `json:"revision"`
	PublishedRevision uint64    `json:"published_revision"`
	Active            string    `json:"active,omitempty"`
	Blocked           bool      `json:"blocked"`
	Error             string    `json:"error,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
}
type siteBuilder struct {
	mu        sync.Mutex
	refreshMu sync.Mutex
	state     siteBuildStatus
	pending   []byte
	published []byte
	root      string
	wake      chan struct{}
	run       func(context.Context, string, string) error
}

func newSiteBuilder(s *Server) *siteBuilder {
	dir := s.env.FireflyBuildDir
	if dir == "" {
		dir = ".firefly"
	}
	b := &siteBuilder{root: config.Abs(s.root, dir), wake: make(chan struct{}, 1), state: siteBuildStatus{Enabled: true, Status: "idle"}}
	data, err := os.ReadFile(filepath.Join(b.root, "state.json"))
	if err == nil {
		var state siteBuildStatus
		if json.Unmarshal(data, &state) == nil && safeRelease(state.Active) {
			if _, err := os.Stat(filepath.Join(b.root, "releases", state.Active, "index.html")); err == nil {
				b.state = state
				b.state.Enabled = true
				b.published, err = os.ReadFile(filepath.Join(b.root, "snapshots", state.Active+".json"))
				if err != nil || !json.Valid(b.published) {
					b.published = nil
					b.state.Blocked = true
				}
			}
		}
	}
	b.run = s.runFireflyBuild
	return b
}
func safeRelease(v string) bool {
	return v != "" && v == filepath.Base(v) && !strings.ContainsAny(v, "/\\.")
}
func (b *siteBuilder) hasActive() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.state.Active != "" }
func (b *siteBuilder) persist() error {
	if err := os.MkdirAll(b.root, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(b.state)
	if err != nil {
		return err
	}
	tmp := filepath.Join(b.root, "state.tmp")
	if err = os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(b.root, "state.json"))
}
func (s *Server) publicSnapshot() ([]byte, error) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	s.contentWriteMu.Lock()
	defer s.contentWriteMu.Unlock()
	snap := fireflySnapshot{AdminPath: s.adminPath(), Site: map[string]any{
		"name": s.cfg.Site.Name, "description": s.cfg.Site.Description, "author": s.cfg.Site.Author,
		"base_url": s.cfg.Site.BaseURL, "logo": s.cfg.Site.Logo, "avatar": s.cfg.Site.AuthorAvatar,
		"utc_offset": s.cfg.Site.UTCOffset, "posts_per_page": s.cfg.Pagination.PostsPerPage,
	}}
	collect := func(typ content.Type) ([]fireflyPost, error) {
		items, err := s.repo.List(typ)
		if err != nil {
			return nil, err
		}
		items = publicPosts(items)
		sortPublic(items)
		out := make([]fireflyPost, 0, len(items))
		for _, v := range items {
			tags := v.Tags
			if tags == nil {
				tags = []string{}
			}
			out = append(out, fireflyPost{Slug: v.Slug, Title: v.Title, Description: v.Description, Published: v.Date, Updated: v.Lastmod, Category: v.Category, Tags: tags, Image: v.CoverImage, TOC: v.TOC, Weight: v.Weight, Body: v.Body})
		}
		return out, nil
	}
	var err error
	if snap.Posts, err = collect(content.TypePost); err != nil {
		return nil, err
	}
	if snap.Pages, err = collect(content.TypePage); err != nil {
		return nil, err
	}
	return json.Marshal(snap)
}
func withdrawn(old, new []byte) bool {
	var a, b fireflySnapshot
	if json.Unmarshal(old, &a) != nil {
		return false
	}
	if json.Unmarshal(new, &b) != nil {
		return true
	}
	keys := map[string]bool{}
	for _, v := range b.Posts {
		keys["post/"+v.Slug] = true
	}
	for _, v := range b.Pages {
		keys["page/"+v.Slug] = true
	}
	for _, v := range a.Posts {
		if !keys["post/"+v.Slug] {
			return true
		}
	}
	for _, v := range a.Pages {
		if !keys["page/"+v.Slug] {
			return true
		}
	}
	return false
}
func (s *Server) queueSiteBuild(force bool) error {
	b := s.siteBuild
	if b == nil {
		return nil
	}
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()
	data, err := s.publicSnapshot()
	b.mu.Lock()
	defer b.mu.Unlock()
	if err != nil {
		b.state.Status = "failed"
		b.state.Error = "无法读取公开内容，请检查 Markdown 文件"
		b.state.Blocked = true
		_ = b.persist()
		return err
	}
	if !force && (bytes.Equal(data, b.pending) || (b.pending == nil && !b.state.Blocked && bytes.Equal(data, b.published))) {
		return nil
	}
	b.pending = data
	b.state.Revision++
	b.state.Error = ""
	b.state.UpdatedAt = time.Now().UTC()
	if withdrawn(b.published, data) {
		b.state.Blocked = true
	}
	if b.state.Status != "building" {
		b.state.Status = "queued"
	}
	if err = b.persist(); err != nil {
		b.state.Blocked = true
		b.state.Status = "failed"
		b.state.Error = "无法保存发布状态"
		return err
	}
	select {
	case b.wake <- struct{}{}:
	default:
	}
	return nil
}
func (s *Server) StartSiteBuild(ctx context.Context) {
	if s.siteBuild == nil {
		return
	}
	// Containers opt in so a new image refreshes the theme even with unchanged content.
	_ = s.queueSiteBuild(s.env.FireflyRebuildOnStart)
	go s.siteBuildLoop(ctx)
	// Reconcile external Markdown/config edits without putting private data into snapshots.
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = s.queueSiteBuild(false)
			}
		}
	}()
}
func (s *Server) siteBuildLoop(ctx context.Context) {
	b := s.siteBuild
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.wake:
		}
		b.mu.Lock()
		if b.pending == nil {
			b.mu.Unlock()
			continue
		}
		data := append([]byte(nil), b.pending...)
		rev := b.state.Revision
		b.state.Status = "building"
		b.state.UpdatedAt = time.Now().UTC()
		b.mu.Unlock()
		release := fmt.Sprintf("%d-%d", time.Now().UnixNano(), rev)
		input := filepath.Join(b.root, "snapshots", release+".json")
		output := filepath.Join(b.root, "releases", release)
		err := os.MkdirAll(filepath.Dir(input), 0700)
		if err == nil {
			err = os.WriteFile(input, data, 0600)
		}
		if err == nil {
			err = os.MkdirAll(output, 0755)
		}
		if err == nil {
			job, cancel := context.WithTimeout(ctx, 15*time.Minute)
			err = b.run(job, input, output)
			cancel()
		}
		if err == nil {
			_, err = os.Stat(filepath.Join(output, "index.html"))
		}
		if err == nil {
			_, err = os.Stat(filepath.Join(output, "_workspace", "index.html"))
		}
		// Recheck the public snapshot before activation, including out-of-band file edits.
		_ = s.queueSiteBuild(false)
		b.mu.Lock()
		if rev != b.state.Revision {
			b.state.Status = "queued"
			b.mu.Unlock()
			continue
		}
		if err != nil {
			log.Printf("Firefly build failed: %v; see %s", err, filepath.Join(b.root, "build.log"))
			b.state.Status = "failed"
			b.state.Error = "构建失败；内容已保存。请查看服务器构建日志后重试。"
		} else {
			previous := b.state
			b.state.Active = release
			b.state.PublishedRevision = rev
			b.state.Status = "published"
			b.state.Blocked = false
			b.state.Error = ""
			if e := b.persist(); e != nil {
				b.state = previous
				b.state.Status = "failed"
				b.state.Error = "构建完成但版本切换失败"
			} else {
				b.published = data
				b.pending = nil
			}
		}
		b.state.UpdatedAt = time.Now().UTC()
		_ = b.persist()
		b.mu.Unlock()
	}
}
func (s *Server) runFireflyBuild(ctx context.Context, input, output string) error {
	source := s.env.FireflySourceDir
	if source == "" {
		source = "firefly"
	}
	source = config.Abs(s.root, source)
	node := s.env.FireflyNode
	if node == "" {
		node = "node"
	}
	cmd := exec.CommandContext(ctx, node, filepath.Join(source, "scripts", "aigoni-build.mjs"), input, output)
	configureBuildProcess(cmd)
	cmd.Dir = source
	// The static compiler never receives the Go server's passwords/model/API credentials.
	for _, key := range []string{"PATH", "Path", "SYSTEMROOT", "SystemRoot", "WINDIR", "COMSPEC", "TEMP", "TMP", "HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA", "NODE_EXTRA_CA_CERTS"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	logPath := filepath.Join(s.siteBuild.root, "build.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	return cmd.Run()
}
func (s *Server) siteBuildAPI(w http.ResponseWriter, r *http.Request) {
	if s.siteBuild == nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(siteBuildStatus{Status: "disabled"})
		return
	}
	if r.Method == http.MethodPost {
		if err := s.queueSiteBuild(true); err != nil {
			writeAdminAPIError(w, 503, "build_unavailable", "无法排队构建", nil)
			return
		}
	}
	b := s.siteBuild
	b.mu.Lock()
	state := b.state
	b.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodPost {
		w.WriteHeader(http.StatusAccepted)
	}
	_ = json.NewEncoder(w).Encode(state)
}

type buildResponse struct {
	http.ResponseWriter
	status int
}

func (w *buildResponse) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }
func (w *buildResponse) Write(v []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(v)
}
func (s *Server) siteBuildMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		mutation := r.Method == "POST" || r.Method == "PATCH" || r.Method == "PUT" || r.Method == "DELETE"
		relevant := p == "/api/content" || strings.HasPrefix(p, adminAPIBasePath+"/posts") || strings.HasPrefix(p, adminAPIBasePath+"/pages") || strings.HasPrefix(p, adminAPIBasePath+"/settings")
		if !mutation || !relevant {
			next.ServeHTTP(w, r)
			return
		}
		out := &buildResponse{ResponseWriter: w}
		next.ServeHTTP(out, r)
		if out.status >= 200 && out.status < 300 {
			_ = s.queueSiteBuild(strings.Contains(p, "/assets"))
		}
	})
}
func (s *Server) serveFirefly(w http.ResponseWriter, r *http.Request, admin bool) {
	b := s.siteBuild
	b.mu.Lock()
	state := b.state
	b.mu.Unlock()
	if state.Active == "" {
		http.Error(w, "Firefly 首次构建尚未完成，请在管理后台检查构建状态。", 503)
		return
	}
	root := filepath.Join(b.root, "releases", state.Active)
	requestPath := r.URL.Path
	// Versioned compiler assets let an already-open page survive an atomic release switch.
	if strings.HasPrefix(requestPath, "/_firefly/") {
		parts := strings.SplitN(strings.TrimPrefix(requestPath, "/_firefly/"), "/", 2)
		if len(parts) != 2 || !safeRelease(parts[0]) || !strings.HasPrefix(parts[1], "_astro/") {
			http.NotFound(w, r)
			return
		}
		root = filepath.Join(b.root, "releases", parts[0])
		requestPath = "/" + parts[1]
	}
	if admin {
		requestPath = "/_workspace/index.html"
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		w.Header().Set("Cache-Control", "no-store")
	}
	if !admin && strings.HasPrefix(requestPath, "/_workspace") {
		http.NotFound(w, r)
		return
	}
	asset := strings.HasPrefix(requestPath, "/_astro/")
	if state.Blocked && !admin && !asset {
		http.Error(w, "站点正在更新已撤回的内容，请稍后重试。", 503)
		return
	}
	if !admin {
		if slug, ok := frontendArticleSlug(requestPath); ok {
			http.Redirect(w, r, "/posts/"+url.PathEscape(slug)+"/", http.StatusMovedPermanently)
			return
		}
		for _, pair := range [][2]string{{"/category/", "category"}, {"/tag/", "tag"}} {
			if strings.HasPrefix(requestPath, pair[0]) {
				value := strings.TrimSuffix(strings.TrimPrefix(requestPath, pair[0]), "/")
				query := pair[1] + "=" + url.QueryEscape(value)
				if pair[1] == "category" && value == "__none__" {
					query = "uncategorized=true"
				}
				http.Redirect(w, r, "/archive/?"+query, 301)
				return
			}
		}
		if strings.HasPrefix(requestPath, "/archive/") && requestPath != "/archive/" {
			year := strings.Trim(strings.TrimPrefix(requestPath, "/archive/"), "/")
			http.Redirect(w, r, "/archive/?year="+url.QueryEscape(year), 301)
			return
		}
	}
	clean := path.Clean("/" + requestPath)
	if strings.Contains(requestPath, "..") || strings.ContainsAny(requestPath, "\\\x00") {
		http.NotFound(w, r)
		return
	}
	file := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
	info, err := os.Stat(file)
	if err == nil && info.IsDir() {
		file = filepath.Join(file, "index.html")
		info, err = os.Stat(file)
	}
	if err != nil || info.IsDir() {
		if !errors.Is(err, os.ErrNotExist) && err != nil {
			http.NotFound(w, r)
			return
		}
		data, e := os.ReadFile(filepath.Join(root, "404.html"))
		if e != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(404)
		_, _ = w.Write(data)
		return
	}
	// Inspect only release descendants: resolving protected Windows ancestors
	// can fail for a sandbox user even when the release itself is readable.
	relative, relErr := filepath.Rel(root, file)
	if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		http.NotFound(w, r)
		return
	}
	cursor := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		cursor = filepath.Join(cursor, part)
		info, err := os.Lstat(cursor)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			http.NotFound(w, r)
			return
		}
	}
	if admin {
		data, e := os.ReadFile(file)
		if e != nil {
			http.Error(w, "工作区不可用", 503)
			return
		}
		meta := fmt.Sprintf("<meta name=\"aigoni-admin-path\" content=\"%s\">", html.EscapeString(s.adminPath()))
		data = bytes.Replace(data, []byte("</head>"), []byte(meta+"</head>"), 1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if asset {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFile(w, r, file)
}

var _ io.Writer = (*buildResponse)(nil)
