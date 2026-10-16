package server

import (
	"aigoni/internal/content"
	"context"
	"errors"
	"gopkg.in/yaml.v3"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fireflyTestServer(t *testing.T) *Server {
	s := testServer(t)
	s.env.FrontendMode = "firefly"
	s.env.AdminFrontendDir = ""
	s.siteBuild = newSiteBuilder(s)
	return s
}
func fakeRelease(_ context.Context, input, output string) error {
	data, err := os.ReadFile(input)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Join(output, "_workspace"), 0755); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(output, "index.html"), data, 0644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(output, "_workspace", "index.html"), []byte("<html><head></head><body>private shell</body></html>"), 0644)
}
func waitBuild(t *testing.T, s *Server, status string) siteBuildStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.siteBuild.mu.Lock()
		state := s.siteBuild.state
		s.siteBuild.mu.Unlock()
		if state.Status == status {
			return state
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("did not reach %s", status)
	return siteBuildStatus{}
}
func TestFireflySnapshotNeverExportsPrivateContent(t *testing.T) {
	s := fireflyTestServer(t)
	writeFile(t, filepath.Join(s.root, "content/posts/draft.md"), "---\ntitle: Draft\nslug: draft\ndate: 2024-01-01T00:00:00Z\npublish: false\n---\nTOP SECRET")
	data, err := s.publicSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"Private Note", "Secret", "TOP SECRET", "AdminPassword", "WikiAPIKey", "content/notes"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("leaked %q", secret)
		}
	}
	if !strings.Contains(string(data), "hello") {
		t.Fatal("missing public post")
	}
}
func TestFireflyBuildPublishAndRestart(t *testing.T) {
	s := fireflyTestServer(t)
	s.siteBuild.run = fakeRelease
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.StartSiteBuild(ctx)
	state := waitBuild(t, s, "published")
	if state.Active == "" || state.PublishedRevision != state.Revision {
		t.Fatalf("bad state %#v", state)
	}
	r := httptest.NewRecorder()
	s.Handler().ServeHTTP(r, httptest.NewRequest("GET", "/", nil))
	if r.Code != 200 || !strings.Contains(r.Body.String(), "hello") {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	b := newSiteBuilder(s)
	if b.state.Active != state.Active || b.state.Blocked {
		t.Fatal("active release not restored")
	}
	r = httptest.NewRecorder()
	s.Handler().ServeHTTP(r, httptest.NewRequest("GET", "/admin", nil))
	if !strings.Contains(r.Body.String(), "aigoni-admin-path") {
		t.Fatalf("missing admin path: %s", r.Body.String())
	}
	r = httptest.NewRecorder()
	s.Handler().ServeHTTP(r, httptest.NewRequest("GET", "/_workspace/", nil))
	if r.Code != 404 {
		t.Fatal("internal shell route exposed")
	}
}
func TestFireflyFailedWithdrawalBlocksOldPagesAndSearch(t *testing.T) {
	s := fireflyTestServer(t)
	var fail atomic.Bool
	s.siteBuild.run = func(ctx context.Context, i, o string) error {
		if fail.Load() {
			return errors.New("compiler failure")
		}
		return fakeRelease(ctx, i, o)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.StartSiteBuild(ctx)
	waitBuild(t, s, "published")
	fail.Store(true)
	p := filepath.Join(s.root, "content/posts/2024/2024-01-01-1.md")
	data, _ := os.ReadFile(p)
	writeFile(t, p, strings.Replace(string(data), "publish: true", "publish: false", 1))
	if err := s.queueSiteBuild(false); err != nil {
		t.Fatal(err)
	}
	state := waitBuild(t, s, "failed")
	if !state.Blocked || state.Active == "" {
		t.Fatalf("unsafe state %#v", state)
	}
	for _, url := range []string{"/", "/posts/hello/", "/pagefind/pagefind.js", "/sitemap.xml"} {
		r := httptest.NewRecorder()
		s.Handler().ServeHTTP(r, httptest.NewRequest("GET", url, nil))
		if r.Code != 503 {
			t.Errorf("%s: %d", url, r.Code)
		}
	}
	if !newSiteBuilder(s).state.Blocked {
		t.Fatal("restart loses withdrawal block")
	}
	r := httptest.NewRecorder()
	s.Handler().ServeHTTP(r, httptest.NewRequest("GET", "/admin", nil))
	if r.Code != 200 {
		t.Fatal("admin recovery blocked")
	}
}
func TestFireflyConcurrentChangesDoNotPublishStaleBuild(t *testing.T) {
	s := fireflyTestServer(t)
	started := make(chan struct{})
	resume := make(chan struct{})
	var count atomic.Int32
	s.siteBuild.run = func(ctx context.Context, input, out string) error {
		if count.Add(1) == 1 {
			close(started)
			select {
			case <-resume:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return fakeRelease(ctx, input, out)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.StartSiteBuild(ctx)
	<-started
	p := filepath.Join(s.root, "content/posts/2024/2024-01-01-1.md")
	data, _ := os.ReadFile(p)
	writeFile(t, p, strings.Replace(string(data), "Body", "Newest Body", 1))
	if err := s.queueSiteBuild(false); err != nil {
		t.Fatal(err)
	}
	close(resume)
	state := waitBuild(t, s, "published")
	if count.Load() != 2 || state.Revision != state.PublishedRevision {
		t.Fatalf("stale build activated %#v", state)
	}
	data, _ = os.ReadFile(filepath.Join(s.siteBuild.root, "releases", state.Active, "index.html"))
	if !strings.Contains(string(data), "Newest Body") {
		t.Fatal("published stale snapshot")
	}
}
func TestFireflyBuildAPIRequiresAuthenticationAndCSRF(t *testing.T) {
	s := fireflyTestServer(t)
	r := httptest.NewRecorder()
	s.Handler().ServeHTTP(r, httptest.NewRequest("GET", "/api/admin/v1/site-build", nil))
	if r.Code != 401 {
		t.Fatalf("anonymous %d", r.Code)
	}
	r = httptest.NewRecorder()
	s.Handler().ServeHTTP(r, adminAPIRequest(t, s, "POST", "/api/admin/v1/site-build", nil, ""))
	if r.Code != 403 {
		t.Fatalf("missing csrf %d", r.Code)
	}
	r = httptest.NewRecorder()
	s.Handler().ServeHTTP(r, adminAPIRequest(t, s, "POST", "/api/admin/v1/site-build", nil, adminCSRFToken(t, s)))
	if r.Code != 202 {
		t.Fatalf("queue %d %s", r.Code, r.Body.String())
	}
}
func TestFireflyPrivateMutationsDoNotQueueBuild(t *testing.T) {
	s := fireflyTestServer(t)
	if err := s.queueSiteBuild(false); err != nil {
		t.Fatal(err)
	}
	rev := s.siteBuild.state.Revision
	_, err := s.repo.Save(content.SaveInput{Type: content.TypeNote, Title: "Private", Body: "hidden", Date: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.queueSiteBuild(false); err != nil {
		t.Fatal(err)
	}
	if s.siteBuild.state.Revision != rev {
		t.Fatal("private update triggered build")
	}
}
func TestPrivateNoteAttachmentsRequireSession(t *testing.T) {
	s := testServer(t)
	writeFile(t, filepath.Join(s.root, "content/notes/2024/2024-01-01-1.assets/private.png"), "private image")
	path := "/assets/notes/2024/2024-01-01-1.assets/private.png"
	r := httptest.NewRecorder()
	s.Handler().ServeHTTP(r, httptest.NewRequest("GET", path, nil))
	if r.Code != 404 {
		t.Fatalf("anonymous asset %d", r.Code)
	}
	r = httptest.NewRecorder()
	s.Handler().ServeHTTP(r, adminAPIRequest(t, s, "GET", path, nil, ""))
	if r.Code != 200 {
		t.Fatalf("authenticated asset %d", r.Code)
	}
}

func TestFireflyRealCompiler(t *testing.T) {
	if os.Getenv("AIGONI_FIREFLY_REAL_BUILD") != "1" {
		t.Skip("opt-in real Astro compiler smoke test")
	}
	s := fireflyTestServer(t)
	source, err := filepath.Abs("../../firefly")
	if err != nil {
		t.Fatal(err)
	}
	s.env.FireflySourceDir = source
	s.env.FireflyNode = "node"
	s.cfg.Site.BaseURL = "http://localhost:8080"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.StartSiteBuild(ctx)
	await := func(after uint64) siteBuildStatus {
		deadline := time.Now().Add(4 * time.Minute)
		for time.Now().Before(deadline) {
			s.siteBuild.mu.Lock()
			st := s.siteBuild.state
			s.siteBuild.mu.Unlock()
			if st.Status == "failed" {
				log, _ := os.ReadFile(filepath.Join(s.siteBuild.root, "build.log"))
				t.Fatalf("compiler failed: %s\n%s", st.Error, log)
			}
			if st.Status == "published" && st.PublishedRevision >= after {
				return st
			}
			time.Sleep(250 * time.Millisecond)
		}
		t.Fatal("real build timeout")
		return siteBuildStatus{}
	}
	first := await(1)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/posts/hello/", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "Hello") {
		t.Fatalf("article %d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "Private Note") {
		t.Fatal("private content in article")
	}
	rr = httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/admin", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "Workspace") {
		t.Fatalf("workspace %d", rr.Code)
	}
	req := adminAPIRequest(t, s, "PATCH", "/api/admin/v1/posts/2024/2024-01-01-1", strings.NewReader("{\"title\":\"Published through aigoni\"}"), adminCSRFToken(t, s))
	rr = httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("save %d %s", rr.Code, rr.Body.String())
	}
	after := await(first.Revision + 1)
	if after.Active == first.Active {
		t.Fatal("release was not switched")
	}
	rr = httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/posts/hello/", nil))
	if !strings.Contains(rr.Body.String(), "Published through aigoni") {
		t.Fatal("new article not served")
	}
}

func TestFireflyOrdinaryBuildFailureRetainsPreviousRelease(t *testing.T) {
	s := fireflyTestServer(t)
	var fail atomic.Bool
	s.siteBuild.run = func(ctx context.Context, i, o string) error {
		if fail.Load() {
			return errors.New("failure")
		}
		return fakeRelease(ctx, i, o)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.StartSiteBuild(ctx)
	previous := waitBuild(t, s, "published")
	fail.Store(true)
	p := filepath.Join(s.root, "content/posts/2024/2024-01-01-1.md")
	data, _ := os.ReadFile(p)
	writeFile(t, p, strings.Replace(string(data), "Hello", "New Title", 1))
	if err := s.queueSiteBuild(false); err != nil {
		t.Fatal(err)
	}
	state := waitBuild(t, s, "failed")
	if state.Blocked || state.Active != previous.Active {
		t.Fatal("ordinary failure removed working site")
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "Hello") {
		t.Fatalf("previous site unavailable %d", rr.Code)
	}
}

func TestFireflyOpenAPIContract(t *testing.T) {
	data, err := os.ReadFile("../../docs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths      map[string]any
		Components struct{ Schemas map[string]any }
	}
	if err = yaml.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.Paths["/api/admin/v1/site-build"] == nil || spec.Components.Schemas["SiteBuildStatus"] == nil {
		t.Fatal("missing build API contract")
	}
}

func TestFireflyRetainsLegacyAssetNamespaces(t *testing.T) {
	s := fireflyTestServer(t)
	s.siteBuild.state.Active = "fixture"
	s.adminFrontendFS = os.DirFS(filepath.Join(s.root, "frontend/dist/admin"))
	writeFile(t, filepath.Join(s.root, "frontend/dist/admin/assets/app.js"), "legacy-admin")
	writeFile(t, filepath.Join(s.root, "frontend/dist/web/assets/app.js"), "legacy-web")
	for _, tc := range []struct{ url, body string }{{"/_app/admin/assets/app.js", "legacy-admin"}, {"/_app/web/assets/app.js", "legacy-web"}} {
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", tc.url, nil))
		if rr.Code != 200 || rr.Body.String() != tc.body {
			t.Fatalf("%s: %d %s", tc.url, rr.Code, rr.Body.String())
		}
	}
}

func TestFireflyContainerRestartRebuildsUnchangedContent(t *testing.T) {
	s := fireflyTestServer(t)
	s.siteBuild.run = fakeRelease
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.StartSiteBuild(ctx)
	previous := waitBuild(t, s, "published")
	env := *s.env
	env.FireflyRebuildOnStart = true
	next := New(s.root, s.cfg, &env)
	next.siteBuild.run = fakeRelease
	nextCtx, stop := context.WithCancel(context.Background())
	defer stop()
	next.StartSiteBuild(nextCtx)
	updated := waitBuild(t, next, "published")
	if updated.PublishedRevision <= previous.PublishedRevision || updated.Active == previous.Active {
		t.Fatal("new image did not get a new release")
	}
}
