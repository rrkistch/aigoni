package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aigoni/internal/content"
	"gopkg.in/yaml.v3"
)

func TestBackupAPIPrivateRoundTripAndConflict(t *testing.T) {
	s := fireflyTestServer(t)
	cfg, _ := yaml.Marshal(s.cfg)
	writeFile(t, filepath.Join(s.root, "config.yaml"), string(cfg))
	writeFile(t, filepath.Join(s.root, ".env"), "SERVER_ENV_SECRET")
	writeFile(t, s.aiConfigPath(), "SERVER_AI_SECRET")
	cookie, csrf := createAdminAPISession(t, s)
	call := func(method, path string, body []byte, auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		if auth {
			r.AddCookie(cookie)
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	wait := func(id, status string) backupRecord {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			state, err := s.backupState(id)
			if err == nil {
				if state.Status == status {
					return state
				}
				if state.Status == "failed" {
					t.Fatalf("maintenance task failed: %s", state.Error)
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("maintenance timeout")
		return backupRecord{}
	}
	if w := call("POST", "/api/admin/v1/backups", nil, false); w.Code != 401 {
		t.Fatal("backup unauthenticated", w.Code)
	}
	w := call("POST", "/api/admin/v1/backups", nil, true)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var created backupRecord
	json.Unmarshal(w.Body.Bytes(), &created)
	created = wait(created.ID, "ready")
	archive, err := os.ReadFile(s.backupFile(created.ID))
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	for {
		_, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		data, _ := io.ReadAll(tr)
		if bytes.Contains(data, []byte("SERVER_ENV_SECRET")) || bytes.Contains(data, []byte("SERVER_AI_SECRET")) {
			t.Fatal("credentials in backup")
		}
	}
	gz.Close()
	if w = call("GET", "/api/admin/v1/backups/items/"+created.ID+"/download", nil, false); w.Code != 401 {
		t.Fatal("private backup exposed", w.Code)
	}
	original, err := s.repo.GetByID("2024/2024-01-01-1", content.TypePost)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.repo.Save(content.SaveInput{ID: original.ID, Type: original.Type, Title: original.Title, Date: original.Date, Slug: original.Slug, Publish: true, Body: "CHANGED_AFTER_BACKUP"})
	if err != nil {
		t.Fatal(err)
	}
	uploadBody, _ := json.Marshal(map[string]any{"name": "restore.tar.gz", "size": len(archive)})
	w = call("POST", "/api/admin/v1/backups/uploads", uploadBody, true)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"offset":0`) {
		t.Fatal(w.Code, w.Body.String())
	}
	var upload backupRecord
	json.Unmarshal(w.Body.Bytes(), &upload)
	if w = call("PUT", "/api/admin/v1/backups/uploads/"+upload.ID+"?offset=0", archive, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call("POST", "/api/admin/v1/backups/uploads/"+upload.ID+"/complete", nil, true); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	wait(upload.ID, "validated")
	if w = call("POST", "/api/admin/v1/backups/items/"+upload.ID+"/restore", []byte(`{"confirm":true}`), true); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	restored := wait(upload.ID, "restored")
	restoredTheme, e := s.readThemeSettings()
	if e != nil || restoredTheme.Revision == 0 {
		t.Fatal("restore rewound configuration revision", e)
	}
	if restored.SafetyBackup == "" {
		t.Fatal("missing safety backup")
	}
	if _, err = os.Stat(s.backupFile(restored.SafetyBackup)); err != nil {
		t.Fatal(err)
	}
	item, err := s.repo.GetByID(original.ID, original.Type)
	if err != nil || item.Body != original.Body {
		t.Fatal("content not restored", err)
	}
	secret, _ := os.ReadFile(s.aiConfigPath())
	if string(secret) != "SERVER_AI_SECRET" {
		t.Fatal("existing AI connection changed")
	}
	if !s.siteBuild.state.Blocked {
		t.Fatal("old website not blocked during restore rebuild")
	}
	// Reuse the same archive; edits after validation invalidate the destructive action.
	if w = call("POST", "/api/admin/v1/backups/items/"+upload.ID+"/validate", nil, true); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	wait(upload.ID, "validated")
	note, err := s.repo.Save(content.SaveInput{Type: content.TypeNote, Title: "新的个人内容", Date: time.Now(), Body: "MUST_KEEP"})
	if err != nil {
		t.Fatal(err)
	}
	if w = call("POST", "/api/admin/v1/backups/items/"+upload.ID+"/restore", []byte(`{"confirm":true}`), true); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	var failed backupRecord
	for time.Now().Before(deadline) {
		failed, _ = s.backupState(upload.ID)
		if failed.Status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if failed.Status != "failed" || !strings.Contains(failed.Error, "数据发生变化") {
		t.Fatal(failed)
	}
	if _, err = s.repo.GetByID(note.ID, content.TypeNote); err != nil {
		t.Fatal("unconfirmed edit lost", err)
	}
	s.restoreBlocked.Store(true)
	if w = call("GET", "/", nil, false); w.Code != 503 {
		t.Fatal("inconsistent data served", w.Code)
	}
	if w = call("GET", s.adminURL("maintenance"), nil, false); w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("independent recovery entry unavailable", w.Code)
	}
}
func TestClosedRecoveryStartsWithoutPublishing(t *testing.T) {
	s := fireflyTestServer(t)
	cfg, _ := yaml.Marshal(s.cfg)
	writeFile(t, filepath.Join(s.root, "config.yaml"), string(cfg))
	s.RequireRecovery()
	s.siteBuild.run = func(context.Context, string, string) error { t.Error("compiler ran before recovery"); return nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.StartSiteBuild(ctx)
	r := httptest.NewRecorder()
	s.Handler().ServeHTTP(r, httptest.NewRequest("GET", "/", nil))
	if r.Code != 503 {
		t.Fatal("normal data exposed in recovery", r.Code)
	}
	r = httptest.NewRecorder()
	s.Handler().ServeHTTP(r, httptest.NewRequest("GET", s.adminURL("maintenance"), nil))
	if r.Code != 200 {
		t.Fatal("recovery UI unavailable", r.Code)
	}
	cookie, csrf := createAdminAPISession(t, s)
	request := httptest.NewRequest("POST", adminAPIBasePath+"/backups/recover", strings.NewReader(`{"confirm":true}`))
	request.AddCookie(cookie)
	request.Header.Set("X-CSRF-Token", csrf)
	s.siteBuild.run = fakeRelease
	r = httptest.NewRecorder()
	s.Handler().ServeHTTP(r, request)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	waitBuild(t, s, "published")
}
