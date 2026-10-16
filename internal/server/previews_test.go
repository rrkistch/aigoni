package server

import (
	"aigoni/internal/content"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuthenticatedPreviewNeverActivatesPublicDraft(t *testing.T) {
	s := fireflyTestServer(t)
	s.siteBuild.run = fakeRelease
	item, err := s.repo.Save(content.SaveInput{Type: content.TypePost, Title: "私有草稿", Slug: "private-draft", Date: time.Now(), Publish: false, Body: "PREVIEW_PRIVATE_SENTINEL"})
	if err != nil {
		t.Fatal(err)
	}
	cookie, csrf := createAdminAPISession(t, s)
	payload, _ := json.Marshal(map[string]string{"type": "posts", "id": item.ID})
	r := httptest.NewRequest("POST", "/api/admin/v1/previews", strings.NewReader(string(payload)))
	r.AddCookie(cookie)
	r.Header.Set("X-CSRF-Token", csrf)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var state previewState
	json.Unmarshal(w.Body.Bytes(), &state)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r = httptest.NewRequest("GET", "/api/admin/v1/previews/"+state.ID, nil)
		r.AddCookie(cookie)
		w = httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		json.Unmarshal(w.Body.Bytes(), &state)
		if state.Status == "ready" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if state.Status != "ready" {
		t.Fatal(state)
	}
	// Fake compiler exposes its input at index.html; the auth boundary must protect it.
	target := "/_preview/" + state.ID + "/"
	r = httptest.NewRequest("GET", target, nil)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("anonymous preview", w.Code)
	}
	r = httptest.NewRequest("GET", target, nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "PREVIEW_PRIVATE_SENTINEL") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	writeFile(t, filepath.Join(s.siteBuild.root, "previews", state.ID, "_astro", "private.js"), "PRIVATE_PREVIEW_ASSET")
	assetURL := "/_firefly/" + state.ID + "/_astro/private.js"
	r = httptest.NewRequest("GET", assetURL, nil)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("anonymous preview asset", w.Code)
	}
	r = httptest.NewRequest("GET", assetURL, nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("private asset cache", w.Code, w.Header())
	}
	if s.siteBuild.state.Active != "" {
		t.Fatal("preview activated public version")
	}
	public, err := s.publicSnapshot()
	if err != nil || strings.Contains(string(public), "PREVIEW_PRIVATE_SENTINEL") {
		t.Fatal("draft exported", err)
	}
}
