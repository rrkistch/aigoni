package server

import (
	"aigoni/internal/content"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestOnboardingSamplesKeepPersonalEdits(t *testing.T) {
	s := fireflyTestServer(t)
	s.env.FireflySourceDir = filepath.Join(s.root, "source")
	for path, body := range map[string]string{"dynamic/example.md": "---\npublished: 2026-01-01\n---\n示例动态", "projects/example.md": "---\ntitle: 示例项目\npublished: 2026-01-01\norder: 100\n---\n示例项目正文", "spec/about.md": "# 关于作者\n示例关于页"} {
		writeFile(t, filepath.Join(s.env.FireflySourceDir, "src", "content", filepath.FromSlash(path)), body)
	}
	cookie, csrf := createAdminAPISession(t, s)
	call := func(mode string, rev uint64) *httptest.ResponseRecorder {
		data, _ := json.Marshal(map[string]any{"mode": mode, "confirm": true, "revision": rev})
		r := httptest.NewRequest("POST", "/api/admin/v1/onboarding/samples", strings.NewReader(string(data)))
		r.AddCookie(cookie)
		r.Header.Set("X-CSRF-Token", csrf)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	w := call("keep", 0)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	state, err := s.readOnboarding()
	if err != nil || !state.Value.Imported || len(state.Value.Owned) != 3 {
		t.Fatal(state, err)
	}
	personal, err := s.repo.GetByID("samples/example", content.TypeProject)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.repo.Save(content.SaveInput{ID: personal.ID, Type: personal.Type, Title: "我的项目", Date: personal.Date, Slug: personal.Slug, Publish: true, Body: "PERSONAL_EDIT"})
	if err != nil {
		t.Fatal(err)
	}
	if w = call("clear", state.Revision); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	personal, err = s.repo.GetByID("samples/example", content.TypeProject)
	if err != nil || personal.Body != "PERSONAL_EDIT" {
		t.Fatal("personal content deleted", err)
	}
	if _, err = s.repo.GetByID("samples/example", content.TypeDynamic); err == nil {
		t.Fatal("untouched sample not cleared")
	}
	snapshot, err := s.publicSnapshot()
	if err != nil || !strings.Contains(string(snapshot), `"use_theme_examples":false`) {
		t.Fatal(string(snapshot), err)
	}
	state, _ = s.readOnboarding()
	if w = call("keep", state.Revision); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err = s.repo.GetByID("samples/example", content.TypeDynamic); err == nil {
		t.Fatal("samples imported twice")
	}
}
