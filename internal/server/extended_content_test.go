package server

import (
	"aigoni/internal/content"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtendedContentPublishingAndLegacyEdit(t *testing.T) {
	s := fireflyTestServer(t)
	cookie, csrf := createAdminAPISession(t, s)
	for _, kind := range []string{"dynamics", "projects"} {
		body := `{"title":"Demo","date":"2026-10-16T00:00:00Z","slug":"demo","publish":true,"body":"PUBLIC_MODULE_BODY","display":{"series":"开始","seriesOrder":0,"comment":false,"location":"上海","status":"进行中"}}`
		r := httptest.NewRequest("POST", "/api/admin/v1/"+kind, strings.NewReader(body))
		r.AddCookie(cookie)
		r.Header.Set("X-CSRF-Token", csrf)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 201 {
			t.Fatal(kind, w.Code, w.Body.String())
		}
		typ, _ := adminAPIContentType(kind)
		items, err := s.repo.List(typ)
		if err != nil || len(items) != 1 {
			t.Fatal(kind, err)
		}
		item := items[0]
		// Legacy callers not supplying display metadata must retain it.
		_, err = s.repo.Save(content.SaveInput{ID: item.ID, Type: typ, Title: item.Title, Date: item.Date, Slug: item.Slug, Publish: true, Body: item.Body})
		if err != nil {
			t.Fatal(err)
		}
		current, _ := s.repo.GetByID(item.ID, typ)
		if current.Display["series"] != "开始" {
			t.Fatal("legacy edit lost metadata")
		}
	}
	data, err := s.publicSnapshot()
	if err != nil || !strings.Contains(string(data), `"dynamics"`) || !strings.Contains(string(data), `"projects"`) || !strings.Contains(string(data), `"comment":false`) {
		t.Fatal(string(data), err)
	}
}
