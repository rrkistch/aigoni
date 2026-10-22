package server

// AI connections are private configuration, never part of public snapshots.
// Note: docs/decisions/unified-admin.md.
import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"aigoni/internal/configdoc"
)

type aiProvider struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	BaseURL      string `json:"base_url"`
	Key          string `json:"api_key,omitempty"`
	Model        string `json:"model"`
	AllowPrivate bool   `json:"allow_private"`
}
type aiSettings struct {
	Default   string       `json:"default_provider"`
	Providers []aiProvider `json:"providers"`
}

func (s *Server) aiConfigPath() string { return filepath.Join(s.root, ".aigoni-admin", "ai.json") }
func (s *Server) readAI() (configdoc.Document[aiSettings], error) {
	return configdoc.Read(s.aiConfigPath(), aiSettings{Providers: []aiProvider{}})
}
func aiPublic(d configdoc.Document[aiSettings]) map[string]any {
	providers := make([]map[string]any, 0, len(d.Value.Providers))
	for _, p := range d.Value.Providers {
		providers = append(providers, map[string]any{"id": p.ID, "name": p.Name, "base_url": p.BaseURL, "model": p.Model, "allow_private": p.AllowPrivate, "has_key": p.Key != "", "key_mask": "••••••••"})
	}
	source := "environment"
	if d.Value.Default != "" {
		source = "backend"
	}
	return map[string]any{"revision": d.Revision, "source": source, "default_provider": d.Value.Default, "providers": providers}
}
func (s *Server) aiProvidersAPI(w http.ResponseWriter, r *http.Request) {
	s.aiMu.Lock()
	defer s.aiMu.Unlock()
	d, err := s.readAI()
	if err != nil {
		writeAdminError(w, 500, "ai_config_unavailable", "无法读取 AI 配置，请检查服务端文件。")
		return
	}
	if r.Method == "GET" {
		writeAdminJSON(w, 200, aiPublic(d))
		return
	}
	var req struct {
		Revision  uint64       `json:"revision"`
		Default   string       `json:"default_provider"`
		Providers []aiProvider `json:"providers"`
	}
	if !decodeAdminWikiJSON(w, r, &req, false) {
		return
	}
	if req.Revision != d.Revision {
		writeAdminError(w, 409, "revision_conflict", "配置已被修改，请重新加载后编辑。")
		return
	}
	if len(req.Providers) > 20 {
		writeAdminError(w, 400, "invalid_providers", "最多保存 20 个服务。")
		return
	}
	old := map[string]aiProvider{}
	for _, p := range d.Value.Providers {
		old[p.ID] = p
	}
	ids := map[string]bool{}
	for i := range req.Providers {
		p := &req.Providers[i]
		p.Name = strings.TrimSpace(p.Name)
		p.BaseURL = strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
		p.Model = strings.TrimSpace(p.Model)
		if p.ID == "" {
			p.ID, err = newWikiAgentRunID()
			if err != nil {
				writeAdminError(w, 500, "id_failed", "无法创建连接标识。")
				return
			}
		}
		if !safeRelease(p.ID) || ids[p.ID] || len(p.Name) == 0 || len(p.Name) > 100 || len(p.Model) > 200 || len(p.Key) > 8192 {
			writeAdminError(w, 400, "invalid_provider", "服务名称、模型或标识无效。")
			return
		}
		ids[p.ID] = true
		if err = validateAIBase(p.BaseURL, p.AllowPrivate); err != nil {
			writeAdminError(w, 400, "invalid_base_url", err.Error())
			return
		}
		before, exists := old[p.ID]
		if exists && before.BaseURL != p.BaseURL && p.Key == "" {
			writeAdminError(w, 400, "key_confirmation_required", "服务地址变更后请重新填写密钥，确认允许向新地址发送凭据。")
			return
		}
		if p.Key == "" && exists {
			p.Key = before.Key
		}
		if strings.ContainsAny(p.Key, "\r\n") {
			writeAdminError(w, 400, "invalid_key", "密钥不能包含换行。")
			return
		}
	}
	if req.Default != "" && !ids[req.Default] {
		writeAdminError(w, 400, "invalid_default", "请选择存在的默认服务，或使用环境配置。")
		return
	}
	for _, p := range req.Providers {
		if p.ID == req.Default && (p.Key == "" || p.Model == "") {
			writeAdminError(w, 400, "incomplete_default", "默认服务需要密钥和模型。")
			return
		}
	}
	next, err := configdoc.Write(s.aiConfigPath(), d, req.Revision, aiSettings{Default: req.Default, Providers: req.Providers})
	if err != nil {
		writeAdminError(w, 500, "ai_save_failed", "AI 配置保存失败。")
		return
	}
	writeAdminJSON(w, 200, aiPublic(next))
}
func validateAIBase(raw string, allowPrivate bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return errors.New("填写完整的 HTTP(S) API 基础地址，不包含密钥、查询参数或片段。")
	}
	if u.Scheme == "http" && !allowPrivate {
		return errors.New("公网连接使用 HTTPS；内网 HTTP 服务需要明确开启内网访问。")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && (!aiAllowedIP(ip, allowPrivate) || (u.Scheme == "http" && !aiIntranetIP(ip))) {
		return errors.New("此地址不允许访问。")
	}
	return nil
}
func aiAllowedIP(ip netip.Addr, allowPrivate bool) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	for _, p := range blockedCIDRs {
		if p.Contains(ip) && (!allowPrivate || p.Bits() == 32) {
			return false
		}
	}
	return allowPrivate || (!ip.IsPrivate() && !ip.IsLoopback())
}

// Resolve and dial the checked address, preventing DNS rebinding. Never follow redirects with a key.
func aiHTTPClient(p aiProvider, timeout time.Duration) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("invalid model host")
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, errors.New("model host DNS failed")
		}
		for _, ip := range ips {
			if !aiAllowedIP(ip, p.AllowPrivate) || (strings.HasPrefix(p.BaseURL, "http://") && !aiIntranetIP(ip)) {
				return nil, errors.New("model network address denied")
			}
		}
		var dialer net.Dialer
		dialer.Timeout = 15 * time.Second
		for _, ip := range ips {
			conn, e := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if e == nil {
				return conn, nil
			}
		}
		return nil, errors.New("model connection failed")
	}
	return &http.Client{Transport: aiResponseTransport{base: tr}, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("model redirects are disabled") }}
}
func (s *Server) selectedAI() (aiProvider, string, error) {
	s.aiMu.Lock()
	defer s.aiMu.Unlock()
	d, err := s.readAI()
	if err != nil {
		return aiProvider{}, "", err
	}
	if d.Value.Default == "" {
		return aiProvider{ID: "environment", Key: s.env.WikiAPIKey, BaseURL: wikiAgentBaseURL(s.env.WikiBaseURL), Model: s.env.WikiModel}, "environment", nil
	}
	for _, p := range d.Value.Providers {
		if p.ID == d.Value.Default {
			return p, fmt.Sprintf("%s:%d", p.ID, d.Revision), nil
		}
	}
	return aiProvider{}, "", errors.New("default AI connection unavailable")
}
func (s *Server) providerByID(id string) (aiProvider, error) {
	s.aiMu.Lock()
	defer s.aiMu.Unlock()
	d, err := s.readAI()
	if err != nil {
		return aiProvider{}, err
	}
	for _, p := range d.Value.Providers {
		if p.ID == id {
			return p, nil
		}
	}
	return aiProvider{}, errors.New("provider not found")
}
func (s *Server) aiModelsAPI(w http.ResponseWriter, r *http.Request) {
	p, err := s.providerByID(r.PathValue("id"))
	if err != nil {
		writeAdminError(w, 404, "provider_not_found", "请先保存服务连接。")
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), "GET", p.BaseURL+"/models", nil)
	if err != nil {
		writeAdminError(w, 400, "invalid_base_url", "服务地址无效。")
		return
	}
	req.Header.Set("Authorization", "Bearer "+p.Key)
	res, err := aiHTTPClient(p, 30*time.Second).Do(req)
	if err != nil {
		writeAdminError(w, 502, "model_discovery_failed", "无法连接模型列表接口，请检查地址、网络与内网访问设置；也可手填模型 ID。")
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		writeAdminError(w, 502, "model_discovery_failed", fmt.Sprintf("模型列表接口返回 HTTP %d；检查密钥权限，或手填模型 ID。", res.StatusCode))
		return
	}
	var data struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil || len(b) > 2<<20 || json.Unmarshal(b, &data) != nil {
		writeAdminError(w, 502, "invalid_model_list", "服务返回的模型列表格式不受支持，可手填模型 ID。")
		return
	}
	models := []string{}
	seen := map[string]bool{}
	for _, m := range data.Data {
		if m.ID != "" && len(m.ID) <= 200 && !seen[m.ID] {
			models = append(models, m.ID)
			seen[m.ID] = true
		}
	}
	sort.Strings(models)
	writeAdminJSON(w, 200, map[string]any{"models": models})
}
func (s *Server) aiTestAPI(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Model   string `json:"model"`
		Confirm bool   `json:"confirm_cost"`
	}
	if !decodeAdminWikiJSON(w, r, &input, false) {
		return
	}
	if !input.Confirm {
		writeAdminError(w, 400, "cost_confirmation_required", "实际模型测试可能产生费用，请先确认。")
		return
	}
	p, err := s.providerByID(r.PathValue("id"))
	if err != nil {
		writeAdminError(w, 404, "provider_not_found", "服务不存在。")
		return
	}
	if input.Model != "" {
		p.Model = input.Model
	}
	if p.Model == "" {
		writeAdminError(w, 400, "model_required", "请选择或填写模型。")
		return
	}
	body, _ := json.Marshal(map[string]any{"model": p.Model, "stream": true, "max_tokens": 64, "messages": []any{map[string]string{"role": "user", "content": "Call connection_check with ok=true. This is a connection test."}}, "tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": "connection_check", "description": "A harmless connection test; no action is executed.", "parameters": map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]string{"type": "boolean"}}, "required": []string{"ok"}}}}}, "tool_choice": map[string]any{"type": "function", "function": map[string]string{"name": "connection_check"}}})
	req, err := http.NewRequestWithContext(r.Context(), "POST", p.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		writeAdminError(w, 400, "invalid_base_url", "地址无效。")
		return
	}
	req.Header.Set("Authorization", "Bearer "+p.Key)
	req.Header.Set("Content-Type", "application/json")
	res, err := aiHTTPClient(p, 45*time.Second).Do(req)
	if err != nil {
		writeAdminError(w, 502, "model_test_failed", "模型测试连接失败或超时。")
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		writeAdminError(w, 502, "model_test_failed", fmt.Sprintf("模型测试返回 HTTP %d，请检查模型权限及流式、工具调用支持。", res.StatusCode))
		return
	}
	scanner := bufio.NewScanner(io.LimitReader(res.Body, 2<<20))
	scanner.Buffer(make([]byte, 4096), 128<<10)
	stream, tool, done := false, false, false
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if raw == "[DONE]" {
			done = true
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					ToolCalls []struct {
						Function struct {
							Name string `json:"name"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(raw), &chunk) == nil && len(chunk.Choices) > 0 {
			stream = true
			for _, c := range chunk.Choices {
				for _, t := range c.Delta.ToolCalls {
					if t.Function.Name == "connection_check" {
						tool = true
					}
				}
			}
		}
	}
	writeAdminJSON(w, 200, map[string]any{"streaming": stream, "tool_calling": tool, "complete": done && scanner.Err() == nil, "passed": stream && tool && done && scanner.Err() == nil, "message": "测试仅验证本次流式响应与工具调用，不执行任何 Wiki 操作。"})
}

// Model responses are bounded even when a provider streams forever without reaching the task deadline.
type aiResponseTransport struct{ base http.RoundTripper }

func (t aiResponseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if res.ContentLength > 16<<20 {
		res.Body.Close()
		return nil, errors.New("model response exceeds limit")
	}
	res.Body = &aiResponseBody{ReadCloser: res.Body, remaining: 16 << 20}
	return res, nil
}

type aiResponseBody struct {
	io.ReadCloser
	remaining int64
}

func (b *aiResponseBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		b.Close()
		return 0, errors.New("model response exceeds limit")
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	return n, err
}

func aiIntranetIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsPrivate() || ip.IsLoopback() || netip.MustParsePrefix("100.64.0.0/10").Contains(ip)
}
