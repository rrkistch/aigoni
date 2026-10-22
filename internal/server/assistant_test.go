package server

import (
	"aigoni/internal/configdoc"
	"aigoni/internal/content"
	"bytes"
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type assistantTransport struct{ token string }

func (a assistantTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.Header = r.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+a.token)
	return http.DefaultTransport.RoundTrip(clone)
}
func TestAssistantTransportAndApprovalBoundary(t *testing.T) {
	s := fireflyTestServer(t)
	s.siteBuild.run = fakeRelease
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.StartSiteBuild(ctx)
	waitBuild(t, s, "published")
	host := httptest.NewServer(s.Handler())
	defer host.Close()
	cookie, csrf := createAdminAPISession(t, s)
	admin := func(method, path string, value any) *httptest.ResponseRecorder {
		body, _ := json.Marshal(value)
		r := httptest.NewRequest(method, adminAPIBasePath+path, bytes.NewReader(body))
		r.AddCookie(cookie)
		r.Header.Set("X-CSRF-Token", csrf)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	created := admin("POST", "/integrations", map[string]any{"revision": 0, "name": "Hermes test", "endpoint": host.URL + "/mcp"})
	if created.Code != 201 {
		t.Fatal(created.Code, created.Body.String())
	}
	var key struct {
		Token      string                  `json:"token"`
		Credential assistantCredentialView `json:"credential"`
	}
	json.Unmarshal(created.Body.Bytes(), &key)
	view := admin("GET", "/integrations", nil)
	if strings.Contains(view.Body.String(), key.Token) || strings.Contains(view.Body.String(), `"hash"`) {
		t.Fatal("credential or digest echoed")
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "integration-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: host.URL + "/mcp", HTTPClient: &http.Client{Transport: assistantTransport{key.Token}, Timeout: 10 * time.Second}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if strings.HasPrefix(tool.Name, "note_") || tool.Name == "notes_list" || strings.HasPrefix(tool.Name, "wiki_") || strings.Contains(tool.Name, "approve") || strings.Contains(tool.Name, "delete") {
			t.Fatal("default scope exposed privileged tool", tool.Name)
		}
	}
	call := func(name string, args any, fail bool) map[string]any {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(name, err)
		}
		if result.IsError != fail {
			t.Fatal(name, "unexpected tool result", result)
		}
		var data map[string]any
		if len(result.Content) == 0 {
			t.Fatal("empty result")
		}
		if err = json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	args := map[string]any{"request_id": "draft-1", "type": "post", "fields": map[string]any{"title": "Assistant draft", "slug": "assistant-draft", "body": "Markdown draft"}}
	first := call("draft_save", args, false)
	second := call("draft_save", args, false)
	if first["target_id"] != second["target_id"] || first["status"] != "saved" {
		t.Fatal(first, second)
	}
	posts, _ := s.repo.List(content.TypePost)
	count := 0
	for _, p := range posts {
		if p.Slug == "assistant-draft" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("duplicate drafts", count)
	}
	original, _ := s.repo.GetByID("2024/2024-01-01-1", content.TypePost)
	revision, _ := content.Revision(original.Path)
	call("draft_save", map[string]any{"request_id": "cannot-edit-public", "type": "post", "id": original.ID, "revision": revision, "fields": map[string]any{"body": "UNAPPROVED"}}, true)
	proposal := call("public_change_propose", map[string]any{"request_id": "public-1", "type": "post", "id": original.ID, "revision": revision, "action": "publish", "fields": map[string]any{"body": "APPROVED_BODY"}}, false)
	same, _ := s.repo.GetByID(original.ID, original.Type)
	if same.Body != original.Body {
		t.Fatal("candidate changed live record")
	}
	human := itemInput(same)
	human.Body = "HUMAN_NEWER_EDIT"
	s.contentWriteMu.Lock()
	_, _, err = content.NewService(s.repo).Update(human, revision)
	s.contentWriteMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	stale := admin("POST", "/approvals/"+proposal["id"].(string)+"/approve", map[string]any{"revision": proposal["revision"]})
	if stale.Code != 409 {
		t.Fatal("stale approval accepted", stale.Code, stale.Body.String())
	}
	current, _ := s.repo.GetByID(original.ID, original.Type)
	revision, _ = content.Revision(current.Path)
	proposal = call("public_change_propose", map[string]any{"request_id": "public-2", "type": "post", "id": current.ID, "revision": revision, "action": "publish", "fields": map[string]any{"body": "APPROVED_BODY"}}, false)
	approved := admin("POST", "/approvals/"+proposal["id"].(string)+"/approve", map[string]any{"revision": proposal["revision"]})
	if approved.Code != 200 {
		t.Fatal(approved.Code, approved.Body.String())
	}
	repeated := admin("POST", "/approvals/"+proposal["id"].(string)+"/approve", map[string]any{"revision": proposal["revision"]})
	if repeated.Code != 200 {
		t.Fatal("duplicate approval not idempotent")
	}
	waitBuild(t, s, "published")
	status := call("operation_get", map[string]any{"request_id": "public-2"}, false)
	if status["publication_status"] != "published" {
		t.Fatal("publication status incorrect", status)
	}
	revoked := admin("DELETE", "/integrations/"+key.Credential.ID, map[string]any{"revision": 1})
	if revoked.Code != 200 {
		t.Fatal(revoked.Code, revoked.Body.String())
	}
	r := httptest.NewRequest("POST", "http://wrong.invalid/mcp", strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer "+key.Token)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("revoked token accepted", w.Code)
	}
	r = httptest.NewRequest("POST", host.URL+"/mcp", strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer "+s.env.AigoniAPIKey)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("legacy API key gained MCP authority", w.Code)
	}
}
func TestAssistantMarkupAndPartialThemeProposal(t *testing.T) {
	for _, body := range []string{"<script>alert(1)</script>", "[x](javascript:alert(1))", "<img src=x onerror=alert(1)>"} {
		if validateAssistantMarkdown(body) == nil {
			t.Fatal("unsafe assistant markup accepted", body)
		}
	}
	if err := validateAssistantMarkdown("代码示例：\n```html\n<script>alert(1)</script>\n```\n"); err != nil {
		t.Fatal("code example rejected", err)
	}
	s := fireflyTestServer(t)
	c := assistantCredential{ID: strings.Repeat("a", 32), Name: "Hermes", Scopes: []string{"theme.propose"}}
	_, err := configdocWriteAssistantTest(s, c)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := s.changeThemeSettings(0, map[string]any{"music": map[string]any{"volume": 0.2, "showLyrics": true}})
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.assistantThemeChange(c, assistantThemeChange{RequestID: "volume-change", Revision: doc.Revision, Groups: map[string]any{"music": map[string]any{"volume": 0.4}}})
	if err != nil {
		t.Fatal(err)
	}
	music := op.Groups["music"].(map[string]any)
	if music["showLyrics"] != true || music["volume"] != 0.4 {
		t.Fatal("partial proposal lost existing settings", music)
	}
	if _, err = s.changeThemeSettings(doc.Revision, map[string]any{"appearance": map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	s.integrationMu.Lock()
	err = s.applyAssistantApproval(&op)
	s.integrationMu.Unlock()
	if err == nil {
		t.Fatal("settings revision conflict ignored")
	}
}

func configdocWriteAssistantTest(s *Server, c assistantCredential) (configdoc.Document[[]assistantCredential], error) {
	doc, err := s.readAssistantCredentials()
	if err != nil {
		return doc, err
	}
	return configdoc.Write(s.assistantCredentialsPath(), doc, doc.Revision, []assistantCredential{c})
}
func TestAssistantPrivateScopeAndInterruptedRequest(t *testing.T) {
	s := fireflyTestServer(t)
	writeFile(t, filepath.Join(s.root, "wiki", "index.md"), "# PRIVATE_WIKI_SENTINEL")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	host := httptest.NewServer(s.Handler())
	defer host.Close()
	cookie, csrf := createAdminAPISession(t, s)
	r := httptest.NewRequest("POST", adminAPIBasePath+"/integrations", strings.NewReader(`{"revision":0,"name":"Private reader","endpoint":"`+host.URL+`/mcp","scopes":["notes.read","wiki.read"]}`))
	r.AddCookie(cookie)
	r.Header.Set("X-CSRF-Token", csrf)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var issued struct {
		Token string `json:"token"`
	}
	json.Unmarshal(w.Body.Bytes(), &issued)
	client := mcp.NewClient(&mcp.Implementation{Name: "private-reader"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: host.URL + "/mcp", HTTPClient: &http.Client{Transport: assistantTransport{issued.Token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	if !names["notes_list"] || !names["wiki_read"] || names["note_save"] || names["draft_save"] {
		t.Fatal(names)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "wiki_read", Arguments: map[string]any{"path": "wiki/index.md"}})
	if err != nil || result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "PRIVATE_WIKI_SENTINEL") {
		t.Fatal(result, err)
	}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "wiki_read", Arguments: map[string]any{"path": ".aigoni-admin/ai.json"}})
	if err != nil || !result.IsError {
		t.Fatal("Wiki scope escaped canonical documents", result, err)
	}
	r = httptest.NewRequest("POST", host.URL+"/mcp", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://evil.invalid")
	r.Header.Set("Authorization", "Bearer "+issued.Token)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin accepted", w.Code)
	}
	r = httptest.NewRequest("POST", "http://other.invalid/mcp", strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer "+issued.Token)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("wrong bound authority accepted", w.Code)
	}
	testBody, _ := json.Marshal(map[string]string{"token": issued.Token})
	r = httptest.NewRequest("POST", adminAPIBasePath+"/integrations/test", bytes.NewReader(testBody))
	r.AddCookie(cookie)
	r.Header.Set("X-CSRF-Token", csrf)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("local connection check", w.Code, w.Body.String())
	}
	// A durable applying intent is never replayed after an interrupted response or process.
	c := assistantCredential{ID: strings.Repeat("b", 32), Name: "Draft writer", Scopes: []string{"drafts.write"}}
	if _, err = configdocWriteAssistantTest(s, c); err != nil {
		t.Fatal(err)
	}
	args := assistantChange{RequestID: "interrupted", Type: "post", Fields: assistantFields{}}
	op, _, err := s.newAssistantOperation(c, "draft", args.RequestID, args)
	if err != nil {
		t.Fatal(err)
	}
	op.Status = "applying"
	if err = s.saveAssistantOperation(&op); err != nil {
		t.Fatal(err)
	}
	prior, _ := s.repo.List(content.TypePost)
	repeated, err := s.assistantContentChange(c, args, "draft")
	if err != nil || s.assistantOperationInfo(repeated)["status"] != "needs_review" {
		t.Fatal(repeated, err)
	}
	after, _ := s.repo.List(content.TypePost)
	if len(prior) != len(after) {
		t.Fatal("interrupted write replayed")
	}
}
