package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestAISettingsLifecycleAndBoundary(t *testing.T) {
	s := testServer(t)
	cookie, csrf := createAdminAPISession(t, s)
	call := func(method, path, body string, auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if auth {
			r.AddCookie(cookie)
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := call("GET", "/api/admin/v1/ai/providers", "", false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-test-key" {
			t.Error("missing key")
		}
		if r.URL.Path == "/v1/models" {
			io.WriteString(w, `{"data":[{"id":"z"},{"id":"a"},{"id":"a"}]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"function\":{\"name\":\"connection_check\"}}]}}]}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	body := `{"revision":0,"default_provider":"","providers":[{"id":"local-test","name":"本地测试","base_url":"` + upstream.URL + `/v1","api_key":"private-test-key","model":"a","allow_private":true}]}`
	w := call("PATCH", "/api/admin/v1/ai/providers", body, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private-test-key") {
		t.Fatal("key exposed")
	}
	if w = call("PATCH", "/api/admin/v1/ai/providers", body, true); w.Code != 409 {
		t.Fatal("revision", w.Code)
	}
	if w = call("POST", "/api/admin/v1/ai/providers/local-test/models", "{}", true); w.Code != 200 || !strings.Contains(w.Body.String(), `["a","z"]`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call("POST", "/api/admin/v1/ai/providers/local-test/test", `{"model":"a"}`, true); w.Code != 400 {
		t.Fatal("missing fee consent", w.Code)
	}
	if w = call("POST", "/api/admin/v1/ai/providers/local-test/test", `{"model":"a","confirm_cost":true}`, true); w.Code != 200 || !strings.Contains(w.Body.String(), `"passed":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	body = strings.Replace(body, `"revision":0`, `"revision":1`, 1)
	body = strings.Replace(body, `"default_provider":""`, `"default_provider":"local-test"`, 1)
	if w = call("PATCH", "/api/admin/v1/ai/providers", body, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	p, version, err := s.selectedAI()
	if err != nil || p.Model != "a" || p.BaseURL != upstream.URL+"/v1" || version != "local-test:2" || !s.wikiReady() {
		t.Fatal(p.Model, version, err)
	}
	// Restart reads the same persisted connection; no environment mutation.
	restored := New(s.root, s.cfg, s.env)
	p, _, err = restored.selectedAI()
	if err != nil || p.Key != "private-test-key" {
		t.Fatal("persistence", err)
	}
	data, err := os.ReadFile(s.aiConfigPath())
	if err != nil || !json.Valid(data) {
		t.Fatal(err)
	}
	body = strings.Replace(body, `"revision":1`, `"revision":2`, 1)
	body = strings.Replace(body, `,"api_key":"private-test-key"`, "", 1)
	body = strings.Replace(body, upstream.URL+"/v1", upstream.URL+"/other", 1)
	if w = call("PATCH", "/api/admin/v1/ai/providers", body, true); w.Code != 400 {
		t.Fatal("address change requires key", w.Code)
	}
	r := httptest.NewRequest("PATCH", "/api/admin/v1/ai/providers", strings.NewReader(body))
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("csrf", w.Code)
	}
}

func TestAINetworkPolicy(t *testing.T) {
	for _, raw := range []string{"http://example.com/v1", "https://user:key@example.com/v1", "https://127.0.0.1/v1", "https://169.254.169.254/v1", "https://example.com/v1?key=x"} {
		if validateAIBase(raw, false) == nil {
			t.Fatal("allowed", raw)
		}
	}
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls++; w.WriteHeader(200) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	req, _ := http.NewRequest("GET", redirect.URL, nil)
	req.Header.Set("Authorization", "Bearer never-forward")
	if _, err := aiHTTPClient(aiProvider{AllowPrivate: true}, 0).Do(req); err == nil {
		t.Fatal("redirect allowed")
	}
	if targetCalls != 0 {
		t.Fatal("credential forwarded")
	}
	if _, err := aiHTTPClient(aiProvider{}, 0).Get(target.URL); err == nil {
		t.Fatal("private network allowed without opt-in")
	}
}
