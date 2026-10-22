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
	if w = call("PATCH", "/api/admin/v1/media/"+item.ID, []byte(`{"name":"renamed.lrc","revision":0}`), true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	newUpload := call("POST", "/api/admin/v1/media/uploads", []byte(`{"name":"new.lrc","size":10}`), true)
	var next mediaUpload
	json.Unmarshal(newUpload.Body.Bytes(), &next)
	newPath := "/api/admin/v1/media/uploads/" + next.ID
	if w = call("PUT", newPath+"?offset=0", []byte("newlyrics!"), true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call("POST", newPath+"/complete", nil, true)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var newer mediaItem
	json.Unmarshal(w.Body.Bytes(), &newer)
	replacement, _ := json.Marshal(map[string]any{"replacement_id": newer.ID, "revision": 1, "replacement_revision": 0, "confirm": true})
	if w = call("POST", "/api/admin/v1/media/"+item.ID+"/replace", replacement, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	theme, err := s.readThemeSettings()
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := json.Marshal(theme.Value)
	if strings.Contains(string(saved), item.Path) || !strings.Contains(string(saved), newer.Path) {
		t.Fatal("current references not replaced", string(saved))
	}
	if w = call("GET", item.Path, nil, false); w.Code != 200 || w.Body.String() != "helloworld" {
		t.Fatal("old immutable content changed", w.Code, w.Body.String())
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
