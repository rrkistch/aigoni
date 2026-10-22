package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMediaChunkUploadAndReferences(t *testing.T) {
	s := fireflyTestServer(t)
	cookie, csrf := createAdminAPISession(t, s)
	call := func(method, path string, data []byte, auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(data))
		if auth {
			r.AddCookie(cookie)
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	w := call("POST", "/api/admin/v1/media/uploads", []byte(`{"name":"song.lrc","size":10}`), true)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var u mediaUpload
	json.Unmarshal(w.Body.Bytes(), &u)
	path := "/api/admin/v1/media/uploads/" + u.ID
	if w = call("PUT", path+"?offset=1", []byte("hello"), true); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w = call("PUT", path+"?offset=0", []byte("hello"), true); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w = call("GET", path, nil, true); !strings.Contains(w.Body.String(), `"offset":5`) {
		t.Fatal(w.Body.String())
	}
	if w = call("PUT", path+"?offset=5", []byte("world"), true); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w = call("POST", path+"/complete", nil, true); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var item mediaItem
	json.Unmarshal(w.Body.Bytes(), &item)
	if w = call("GET", item.Path, nil, false); w.Code != http.StatusOK || w.Body.String() != "helloworld" {
		t.Fatal(w.Code, w.Body.String())
	}
	body, _ := json.Marshal(map[string]any{"revision": 0, "groups": map[string]any{"music": map[string]any{"local": map[string]any{"playlist": []any{map[string]any{"name": "test", "url": "/music.mp3", "lrc": item.Path}}}}}})
	if w = call("PATCH", "/api/admin/v1/theme-settings", body, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call("DELETE", "/api/admin/v1/media/"+item.ID, nil, true); w.Code != 409 {
		t.Fatal("deleted referenced asset", w.Code)
	}
	if w = call("GET", "/api/admin/v1/media", nil, false); w.Code != 401 {
		t.Fatal("index exposed", w.Code)
	}
	if w = call("POST", "/api/admin/v1/media/uploads", []byte(`{"name":"x.svg","size":10}`), true); w.Code != 400 {
		t.Fatal("svg accepted", w.Code)
	}
}
