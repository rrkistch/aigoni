package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type serviceCheck struct {
	Label      string `json:"label"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	URL        string `json:"url,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

func nestedString(value any, keys ...string) string {
	for _, key := range keys {
		m, ok := value.(map[string]any)
		if !ok {
			return ""
		}
		value = m[key]
	}
	text, _ := value.(string)
	return text
}
func nestedBool(value any, keys ...string) bool {
	for _, key := range keys {
		m, ok := value.(map[string]any)
		if !ok {
			return false
		}
		value = m[key]
	}
	b, _ := value.(bool)
	return b
}
func (s *Server) serviceCheckAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AllowPrivate bool `json:"allow_private"`
	}
	if !decodeAdminWikiJSON(w, r, &req, true) {
		return
	}
	group := r.PathValue("group")
	if group != "comments" && group != "analytics" && group != "dynamic" && group != "collections" {
		writeAdminError(w, 404, "service_not_found", "没有此连接检查。")
		return
	}
	s.themeMu.Lock()
	doc, err := s.readThemeSettings()
	s.themeMu.Unlock()
	if err != nil {
		writeAdminError(w, 500, "service_config_failed", "无法读取已保存服务配置。")
		return
	}
	config := s.themeDefaults()[group]
	if override, ok := doc.Value[group]; ok {
		config = mergeAdminDefaults(config, override)
	}
	checks := []serviceCheck{}
	targets := []serviceCheck{}
	required := func(label, value string) {
		status := "configured"
		message := "已填写；未核验账号权限、公开可见性或第三方后台。"
		if strings.TrimSpace(value) == "" {
			status = "attention"
			message = "尚未填写。"
		}
		checks = append(checks, serviceCheck{Label: label, Kind: "configuration", Status: status, Message: message})
	}
	target := func(label, address string) {
		if address == "" {
			required(label, address)
			return
		}
		if strings.HasPrefix(address, "/") && !strings.HasPrefix(address, "//") {
			checks = append(checks, serviceCheck{Label: label, Kind: "local-path", Status: "configured", Message: "使用站内地址，待网站上线后在公开页面检查实际加载。", URL: address})
			return
		}
		targets = append(targets, serviceCheck{Label: label, Kind: "http-reachability", URL: address})
	}
	switch group {
	case "dynamic":
		if !nestedBool(config, "memos", "enable") {
			checks = append(checks, serviceCheck{Label: "动态来源", Kind: "configuration", Status: "configured", Message: "当前使用本站动态；内容在“动态”中编辑，不连接Memos。"})
		} else {
			target("Memos实例地址", nestedString(config, "memos", "apiUrl"))
			required("Memos用户标识", nestedString(config, "memos", "parent"))
		}
	case "comments":
		provider := nestedString(config, "type")
		switch provider {
		case "none", "":
			checks = append(checks, serviceCheck{Label: "评论", Kind: "configuration", Status: "configured", Message: "评论未启用，不发送请求。"})
		case "twikoo":
			target("Twikoo服务地址", nestedString(config, "twikoo", "envId"))
		case "waline":
			target("Waline服务地址", nestedString(config, "waline", "serverURL"))
		case "artalk":
			target("Artalk服务地址", nestedString(config, "artalk", "server"))
		case "giscus":
			required("Giscus仓库", nestedString(config, "giscus", "repo"))
			required("Giscus仓库ID", nestedString(config, "giscus", "repoId"))
			required("Giscus分类ID", nestedString(config, "giscus", "categoryId"))
		case "disqus":
			required("Disqus站点名", nestedString(config, "disqus", "shortname"))
		}
	case "analytics":
		enabled := false
		for _, item := range []struct{ key, label string }{{"googleAnalyticsId", "Google Analytics"}, {"microsoftClarityId", "Clarity"}} {
			if id := nestedString(config, item.key); id != "" {
				required(item.label+"公开站点ID", id)
				enabled = true
			}
		}
		if id := nestedString(config, "umamiAnalytics", "websiteId"); id != "" {
			required("Umami站点ID", id)
			target("Umami脚本可达性", nestedString(config, "umamiAnalytics", "scriptUrl"))
			enabled = true
		}
		if id := nestedString(config, "la51Analytics", "Id"); id != "" {
			required("51la站点ID", id)
			enabled = true
		}
		if !enabled {
			checks = append(checks, serviceCheck{Label: "公开统计", Kind: "configuration", Status: "configured", Message: "未配置统计账号；不会在本检查执行追踪脚本或发送访问事件。"})
		}
	case "collections":
		found := false
		for _, item := range []struct{ path, key, label string }{{"bilibili", "uid", "Bilibili UID"}, {"bangumi", "userId", "Bangumi用户ID"}, {"vndb", "userId", "VNDB公开用户ID"}, {"mal", "username", "MyAnimeList用户名"}} {
			if id := nestedString(config, item.path, item.key); id != "" {
				required(item.label, id)
				found = true
			}
		}
		if username := nestedString(config, "mal", "username"); username != "" {
			required("MyAnimeList公开Client ID", nestedString(config, "mal", "clientId"))
		}
		if !found {
			checks = append(checks, serviceCheck{Label: "追番与收藏", Kind: "configuration", Status: "attention", Message: "尚未填写用户标识；收藏内容在原平台维护。"})
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	for _, check := range targets {
		check.Status = "attention"
		parsed, e := url.Parse(check.URL)
		if e != nil || parsed.User != nil || parsed.Fragment != "" {
			check.Message = "地址无效，不发送请求。"
			checks = append(checks, check)
			continue
		}
		base := *parsed
		base.RawQuery = ""
		if e = validateAIBase(base.String(), req.AllowPrivate); e != nil {
			check.Message = "地址不符合网络规则；公网使用HTTPS，内网地址须明确许可。"
			checks = append(checks, check)
			continue
		}
		client := aiHTTPClient(aiProvider{BaseURL: base.String(), AllowPrivate: req.AllowPrivate}, 15*time.Second)
		probe, e := http.NewRequestWithContext(ctx, "GET", check.URL, nil)
		if e != nil {
			check.Message = "无法构造检查请求。"
			checks = append(checks, check)
			continue
		}
		probe.Header.Set("User-Agent", "aigoni-site-check/1")
		response, e := client.Do(probe)
		if e != nil {
			check.Message = "连接未通过；请检查DNS、TLS、网络、地址或重定向。不会自动换用其他地址。"
			checks = append(checks, check)
			continue
		}
		check.HTTPStatus = response.StatusCode
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 2<<20))
		response.Body.Close()
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			check.Status = "reachable"
		}
		check.Message = fmt.Sprintf("该地址返回HTTP %d。仅检查连通性，不代表API版本、用户标识、评论发布或事件统计已正确。", response.StatusCode)
		checks = append(checks, check)
	}
	writeAdminJSON(w, 200, map[string]any{"checks": checks, "scope": "仅检查已保存配置与列出的HTTP地址，不验证第三方账号权限、收藏内容、评论审核或统计报表；不执行脚本、不发访问事件。", "configuration_revision": doc.Revision, "private_pages_tracking": false})
}
