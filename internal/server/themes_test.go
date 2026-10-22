package server

import (
	"aigoni/internal/content"
	"aigoni/internal/themebundle"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func installedTheme(t *testing.T, s *Server, version string) {
	t.Helper()
	s.themeInstallMu.Lock()
	defer s.themeInstallMu.Unlock()
	dir := filepath.Join(s.themesRoot(), "versions", version)
	m := themebundle.Manifest{Version: version, Format: 1, SourceCommit: strings.Repeat("a", 40), OS: "linux", Arch: "amd64", NodeMajor: 24, PNPM: themebundle.PNPM, AdminAPI: 1, Settings: 1}
	b, _ := json.Marshal(themeUpload{Tag: version, Manifest: &m})
	writeFile(t, filepath.Join(dir, "installation.json"), string(b))
	writeFile(t, filepath.Join(dir, "source/scripts/aigoni-build.mjs"), "// fixture")
	writeFile(t, filepath.Join(dir, "source/aigoni-settings-schema.json"), string(adminSchemaJSON))
	err := s.saveThemeUpload(themeUpload{ID: "fixture-upload", Tag: version, Name: themebundle.AssetName(version), Size: 1, Offset: 1, Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
}
func TestThemeActivationFailureAndRollbackUseCurrentContent(t *testing.T) {
	s := fireflyTestServer(t)
	s.siteBuild.run = fakeRelease
	cookie, csrf := createAdminAPISession(t, s)
	call := func(route, body string, token bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", adminAPIBasePath+route, bytes.NewBufferString(body))
		r.AddCookie(cookie)
		if token {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := call("/theme/activate", `{"version":"theme-v1.0.0"}`, false); w.Code != 403 {
		t.Fatal("missing CSRF accepted", w.Code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.StartSiteBuild(ctx)
	waitBuild(t, s, "published")
	installedTheme(t, s, "theme-v1.0.0")
	if w := call("/theme/activate", `{"version":"theme-v1.0.0"}`, true); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	state := waitBuild(t, s, "published")
	if state.Theme != "theme-v1.0.0" {
		t.Fatal(state)
	}
	installedTheme(t, s, "theme-v1.1.0")
	var fail atomic.Bool
	fail.Store(true)
	s.siteBuild.buildMu.Lock()
	s.siteBuild.run = func(ctx context.Context, in, out string) error {
		if fail.Load() {
			return errors.New("fixture build failure")
		}
		return fakeRelease(ctx, in, out)
	}
	s.siteBuild.buildMu.Unlock()
	if w := call("/theme/activate", `{"version":"theme-v1.1.0"}`, true); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	failed := waitBuild(t, s, "failed")
	if failed.Theme != state.Theme || failed.Active != state.Active || failed.PendingTheme != "theme-v1.1.0" {
		t.Fatal("failed candidate activated", failed)
	}
	restarted := newSiteBuilder(s)
	if restarted.state.PendingTheme != "theme-v1.1.0" {
		t.Fatal("pending theme lost on restart")
	}
	fail.Store(false)
	if err := s.queueSiteBuild(true); err != nil {
		t.Fatal(err)
	}
	state = waitBuild(t, s, "published")
	if state.Theme != "theme-v1.1.0" || state.PreviousTheme != "theme-v1.0.0" {
		t.Fatal(state)
	}
	original, err := s.repo.GetByID("2024/2024-01-01-1", content.TypePost)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.repo.Save(content.SaveInput{ID: original.ID, Type: original.Type, Title: original.Title, Date: original.Date, Slug: original.Slug, Publish: true, Body: "CURRENT_CONTENT_AFTER_UPGRADE"})
	if err != nil {
		t.Fatal(err)
	}
	if w := call("/theme/rollback", `{}`, true); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	state = waitBuild(t, s, "published")
	if state.Theme != "theme-v1.0.0" {
		t.Fatal(state)
	}
	data, _ := os.ReadFile(filepath.Join(s.siteBuild.root, "releases", state.Active, "index.html"))
	if !bytes.Contains(data, []byte("CURRENT_CONTENT_AFTER_UPGRADE")) {
		t.Fatal("rollback restored stale content")
	}
}
