package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServiceChecksSavedConfigAndExplicitPrivatePermission(t *testing.T) {
	hits := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("Authorization") != "" {
			t.Error("credential sent to service probe")
		}
		w.Write([]byte("public endpoint"))
	}))
	defer remote.Close()
	s := fireflyTestServer(t)
	_, err := s.changeThemeSettings(0, map[string]any{"dynamic": map[string]any{"memos": map[string]any{"enable": true, "apiUrl": remote.URL, "parent": "users/1"}}})
	if err != nil {
		t.Fatal(err)
	}
	cookie, csrf := createAdminAPISession(t, s)
	check := func(allow bool) map[string]any {
		body, _ := json.Marshal(map[string]bool{"allow_private": allow})
		r := httptest.NewRequest("POST", adminAPIBasePath+"/services/dynamic/test", bytes.NewReader(body))
		r.AddCookie(cookie)
		r.Header.Set("X-CSRF-Token", csrf)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var result map[string]any
		json.Unmarshal(w.Body.Bytes(), &result)
		return result
	}
	check(false)
	if hits != 0 {
		t.Fatal("private network accessed without permission")
	}
	result := check(true)
	if hits != 1 {
		t.Fatal("expected one readonly probe", hits)
	}
	if result["scope"] == "" {
		t.Fatal("missing scope explanation")
	}
	if plainMediaText([]byte("[00:01] <img src=x onerror=alert(1)>")) {
		t.Fatal("executable lyrics accepted")
	}
	if !plainMediaText([]byte("[00:01] I <3 you")) {
		t.Fatal("plain lyrics rejected")
	}
}
