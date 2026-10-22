package server

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aigoni/internal/configdoc"
	"gopkg.in/yaml.v3"
)

var assistantDefaultScopes = []string{"public.read", "drafts.write", "public.propose", "theme.propose"}
var assistantScopes = map[string]string{
	"public.read":    "查询公开内容与发布状态",
	"drafts.write":   "创建、读取和编辑未公开的文章、页面、动态与项目",
	"public.propose": "提交发布、公开修改或撤回候选，须管理员批准",
	"theme.propose":  "提交外观与内容模块修改，须管理员批准；不管理服务凭据",
	"notes.read":     "读取私人笔记和收藏",
	"notes.write":    "读取和编辑私人笔记，不含删除",
	"wiki.read":      "只读查询 Wiki 文档，不启动收费 AI 任务",
}

type assistantCredential struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Endpoint string    `json:"endpoint"`
	Scopes   []string  `json:"scopes"`
	Hash     string    `json:"hash"`
	Created  time.Time `json:"created"`
	Revoked  bool      `json:"revoked"`
}
type assistantCredentialView struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Endpoint string     `json:"endpoint"`
	Scopes   []string   `json:"scopes"`
	Created  time.Time  `json:"created"`
	Revoked  bool       `json:"revoked"`
	LastUsed *time.Time `json:"last_used,omitempty"`
}

func (c assistantCredential) allows(scope string) bool {
	for _, v := range c.Scopes {
		if v == scope {
			return true
		}
	}
	return false
}
func (s *Server) assistantRoot() string { return filepath.Join(s.root, ".aigoni-admin", "assistant") }
func (s *Server) assistantCredentialsPath() string {
	return filepath.Join(s.assistantRoot(), "credentials.json")
}
func (s *Server) readAssistantCredentials() (configdoc.Document[[]assistantCredential], error) {
	return configdoc.Read(s.assistantCredentialsPath(), []assistantCredential{})
}
func (s *Server) credentialView(c assistantCredential) assistantCredentialView {
	v := assistantCredentialView{ID: c.ID, Name: c.Name, Endpoint: c.Endpoint, Scopes: c.Scopes, Created: c.Created, Revoked: c.Revoked}
	var used time.Time
	if data, err := os.ReadFile(filepath.Join(s.assistantRoot(), "usage", c.ID+".json")); err == nil && json.Unmarshal(data, &used) == nil {
		v.LastUsed = &used
	}
	return v
}
func normalizeMCPEndpoint(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.User != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" || strings.TrimSuffix(u.Path, "/") != "/mcp" || len(raw) > 1024 {
		return "", errors.New("连接地址必须是完整 HTTP(S) 地址并以 /mcp 结尾，不含账号、查询参数或片段")
	}
	u.Path = "/mcp"
	u.RawPath = ""
	u.Host = strings.ToLower(u.Host)
	return u.String(), nil
}
func (s *Server) defaultMCPEndpoint() string {
	s.settingsMu.Lock()
	base := s.cfg.Site.BaseURL
	s.settingsMu.Unlock()
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return "http://127.0.0.1:18080/mcp"
	}
	u.Path = "/mcp"
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
func mcpAuthority(host, scheme string) string {
	u, err := url.Parse(scheme + "://" + host)
	if err != nil {
		return ""
	}
	port := u.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}
func (s *Server) assistantAuthenticate(r *http.Request) (assistantCredential, error) {
	var none assistantCredential
	fields := strings.Fields(r.Header.Get("Authorization"))
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
		return none, errors.New("需要专用助手 Bearer 凭据")
	}
	raw := fields[1]
	parts := strings.Split(strings.TrimPrefix(raw, "aigoni_mcp_"), ".")
	if !strings.HasPrefix(raw, "aigoni_mcp_") || len(parts) != 2 || len(parts[0]) != 32 || !safeRelease(parts[0]) {
		return none, errors.New("助手凭据无效或已撤销")
	}
	s.integrationMu.Lock()
	defer s.integrationMu.Unlock()
	doc, err := s.readAssistantCredentials()
	if err != nil {
		return none, errors.New("助手配置暂时无法读取")
	}
	sum := sha256.Sum256([]byte(raw))
	digest := hex.EncodeToString(sum[:])
	for _, c := range doc.Value {
		if c.ID != parts[0] || c.Revoked || subtle.ConstantTimeCompare([]byte(c.Hash), []byte(digest)) != 1 {
			continue
		}
		u, err := url.Parse(c.Endpoint)
		if err != nil || mcpAuthority(r.Host, u.Scheme) != mcpAuthority(u.Host, u.Scheme) {
			return none, errors.New("请求 Host 与此凭据绑定的连接地址不匹配，请检查 Hermes URL 和代理 Host 设置")
		}
		now, _ := json.Marshal(time.Now().UTC())
		_ = configdoc.Atomic(filepath.Join(s.assistantRoot(), "usage", c.ID+".json"), now)
		return c, nil
	}
	return none, errors.New("助手凭据无效或已撤销")
}
func hermesConfig(endpoint, token string) string {
	data, _ := yaml.Marshal(map[string]any{"mcp_servers": map[string]any{"aigoni": map[string]any{"url": endpoint, "headers": map[string]string{"Authorization": "Bearer " + token}, "timeout": 30, "connect_timeout": 15}}})
	return string(data)
}
func (s *Server) integrationsAPI(w http.ResponseWriter, r *http.Request) {
	defaultEndpoint := s.defaultMCPEndpoint()
	s.integrationMu.Lock()
	defer s.integrationMu.Unlock()
	doc, err := s.readAssistantCredentials()
	if err != nil {
		writeAdminError(w, 500, "integrations_unavailable", "无法读取助手凭据。")
		return
	}
	if r.Method == "GET" {
		items := []assistantCredentialView{}
		for _, c := range doc.Value {
			items = append(items, s.credentialView(c))
		}
		writeAdminJSON(w, 200, map[string]any{"revision": doc.Revision, "items": items, "scopes": assistantScopes, "default_scopes": assistantDefaultScopes, "endpoint": defaultEndpoint, "config_template": hermesConfig(defaultEndpoint, "${AIGONI_MCP_TOKEN}")})
		return
	}
	var req struct {
		Name     string   `json:"name"`
		Endpoint string   `json:"endpoint"`
		Scopes   []string `json:"scopes"`
		Revision uint64   `json:"revision"`
	}
	if !decodeAdminWikiJSON(w, r, &req, false) {
		return
	}
	if req.Revision != doc.Revision {
		writeAdminError(w, 409, "revision_conflict", "助手配置已变化，请刷新后操作。")
		return
	}
	if r.Method == "DELETE" {
		id := r.PathValue("id")
		for i := range doc.Value {
			if doc.Value[i].ID != id {
				continue
			}
			before := s.credentialView(doc.Value[i])
			doc.Value[i].Revoked = true
			_, err = configdoc.Write(s.assistantCredentialsPath(), doc, req.Revision, doc.Value)
			if err != nil {
				writeAdminError(w, 500, "integration_save_failed", "撤销状态保存失败。")
				return
			}
			_ = s.auditAssistantEvent("integration.revoked", before, s.credentialView(doc.Value[i]))
			writeAdminJSON(w, 200, map[string]bool{"revoked": true})
			return
		}
		writeAdminError(w, 404, "integration_not_found", "凭据不存在。")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 160 {
		writeAdminError(w, 400, "invalid_name", "请填写助手名称，最长160字节。")
		return
	}
	endpoint, err := normalizeMCPEndpoint(req.Endpoint)
	if err != nil {
		writeAdminError(w, 400, "invalid_endpoint", err.Error())
		return
	}
	if req.Scopes == nil {
		req.Scopes = append([]string{}, assistantDefaultScopes...)
	}
	if len(req.Scopes) == 0 {
		writeAdminError(w, 400, "invalid_scope", "至少选择一项权限。")
		return
	}
	seen := map[string]bool{}
	for _, scope := range req.Scopes {
		if _, ok := assistantScopes[scope]; !ok || seen[scope] {
			writeAdminError(w, 400, "invalid_scope", "权限项无效或重复。")
			return
		}
		seen[scope] = true
	}
	id, err := newWikiAgentRunID()
	if err != nil {
		writeAdminError(w, 500, "integration_create_failed", "无法创建凭据。")
		return
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		writeAdminError(w, 500, "integration_create_failed", "无法生成凭据。")
		return
	}
	raw := "aigoni_mcp_" + id + "." + base64.RawURLEncoding.EncodeToString(secret)
	sum := sha256.Sum256([]byte(raw))
	c := assistantCredential{ID: id, Name: req.Name, Endpoint: endpoint, Scopes: req.Scopes, Hash: hex.EncodeToString(sum[:]), Created: time.Now().UTC()}
	next := append(doc.Value, c)
	if _, err = configdoc.Write(s.assistantCredentialsPath(), doc, doc.Revision, next); err != nil {
		writeAdminError(w, 500, "integration_save_failed", "无法保存助手凭据。")
		return
	}
	_ = s.auditAssistantEvent("integration.created", nil, s.credentialView(c))
	writeAdminJSON(w, 201, map[string]any{"credential": s.credentialView(c), "token": raw, "config": hermesConfig(c.Endpoint, "${AIGONI_MCP_TOKEN}"), "message": "凭据仅显示这一次，请保存到 Hermes 的私密环境配置；不要放入主题或公开文章。"})
}
func (s *Server) integrationTestAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if !decodeAdminWikiJSON(w, r, &req, false) {
		return
	}
	// This checks the real local transport/auth handler, not the remote Hermes host's network.
	s.integrationMu.Lock()
	doc, err := s.readAssistantCredentials()
	endpoint := ""
	if err == nil {
		for _, c := range doc.Value {
			if strings.HasPrefix(req.Token, "aigoni_mcp_"+c.ID+".") {
				endpoint = c.Endpoint
				break
			}
		}
	}
	s.integrationMu.Unlock()
	if endpoint == "" {
		writeAdminError(w, 400, "invalid_token", "请输入此站点有效的助手凭据。")
		return
	}
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"aigoni-connection-test","version":"1"}}}`)
	check := httptest.NewRequest("POST", endpoint, bytes.NewReader(body))
	check.Header.Set("Authorization", "Bearer "+req.Token)
	check.Header.Set("Content-Type", "application/json")
	check.Header.Set("Accept", "application/json, text/event-stream")
	out := httptest.NewRecorder()
	s.mcpHTTPHandler().ServeHTTP(out, check)
	if out.Code != 200 {
		writeAdminError(w, 400, "mcp_test_failed", "凭据或 MCP 初始化未通过，请检查凭据、地址绑定和应用日志。")
		return
	}
	var result map[string]any
	if json.Unmarshal(out.Body.Bytes(), &result) != nil || result["result"] == nil {
		writeAdminError(w, 400, "mcp_test_failed", "MCP 初始化未返回有效结果。")
		return
	}
	writeAdminJSON(w, 200, map[string]any{"ok": true, "scope": "本应用中的专用凭据与 MCP 初始化，不检查 Hermes 宿主机网络、DNS 或 HTTPS 证书", "message": "协议和凭据检查通过；请在 Hermes 重启后确认工具已发现。"})
}
