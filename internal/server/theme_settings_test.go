package server

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestThemeSettingsPublishConflictAndDefaults(t *testing.T) {
	s := fireflyTestServer(t)
	s.env.FireflySourceDir = filepath.Join("..", "..", "firefly")
	// Resolve against repository, not the test's content root.
	source, err := filepath.Abs(filepath.Join("..", "..", "firefly"))
	if err != nil {
		t.Fatal(err)
	}
	s.env.FireflySourceDir = source
	cookie, csrf := createAdminAPISession(t, s)
	call := func(method, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/admin/v1/theme-settings", strings.NewReader(body))
		r.AddCookie(cookie)
		r.Header.Set("X-CSRF-Token", csrf)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	w := call("GET", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var state struct {
		Effective map[string]any `json:"effective"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	for name, field := range adminSchema() {
		if err = validateAdminValue(field, state.Effective[name], name); err != nil {
			t.Fatal("default not editable", err)
		}
	}
	body := `{"revision":0,"groups":{"appearance":{"themeColor":{"hue":220,"defaultMode":"dark"}},"music":{"volume":0.3}}}`
	if w = call("PATCH", body); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call("PATCH", body); w.Code != 409 {
		t.Fatal("lost conflict", w.Code)
	}
	snap, err := s.publicSnapshot()
	if err != nil || !strings.Contains(string(snap), `"hue":220`) {
		t.Fatal(string(snap), err)
	}
	if w = call("PATCH", `{"revision":1,"groups":{"music":{"meting":{"auth":"secret"}}}}`); w.Code != 400 {
		t.Fatal("secret field accepted", w.Code)
	}
	if w = call("PATCH", `{"revision":1,"groups":{"navigation":{"links":[{"name":"bad","url":"javascript:alert(1)"}]}}}`); w.Code != 400 {
		t.Fatal("script URL accepted", w.Code)
	}
	if w = call("PATCH", `{"revision":1,"groups":{"appearance":null}}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	d, err := s.readThemeSettings()
	if err != nil || d.Value["appearance"] != nil || d.Value["music"] == nil {
		t.Fatal("group reset", err)
	}
}

func TestThemeWithdrawalDistinguishesEditsFromRemoval(t *testing.T) {
	old := map[string]any{"friends": []any{map[string]any{"title": "Old", "siteurl": "https://example.com"}}}
	edited := map[string]any{"friends": []any{map[string]any{"title": "New", "siteurl": "https://example.com"}}}
	if themeContentWithdrawn(old, edited) {
		t.Fatal("ordinary edit should preserve last good release on failure")
	}
	if !themeContentWithdrawn(old, map[string]any{"friends": []any{}}) {
		t.Fatal("removed public content must block old release")
	}
	old = map[string]any{"gallery": map[string]any{"albums": []any{map[string]any{"id": "old"}}}}
	if !themeContentWithdrawn(old, map[string]any{"gallery": map[string]any{"albums": []any{map[string]any{"id": "new"}}}}) {
		t.Fatal("old gallery route remains exposed")
	}
}
