package server

import (
	"errors"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"html"
	"net/url"
	"strings"
)

// New assistant writes use Markdown only. Trusted admin and legacy API contracts are unchanged.
func validateAssistantMarkdown(body string) error {
	source := []byte(body)
	root := goldmark.DefaultParser().Parse(text.NewReader(source))
	return ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *ast.HTMLBlock, *ast.RawHTML:
			return ast.WalkStop, errors.New("助手提交只接受 Markdown；原始 HTML、脚本和嵌入标签请由管理员在编辑器维护。代码示例请放入代码块")
		case *ast.Link:
			if !safeAssistantLink(string(n.Destination)) {
				return ast.WalkStop, errors.New("链接仅允许站内路径、HTTP(S) 或 mailto")
			}
		case *ast.Image:
			if !safeAssistantLink(string(n.Destination)) {
				return ast.WalkStop, errors.New("图片地址协议不受支持")
			}
		}
		return ast.WalkContinue, nil
	})
}
func safeAssistantLink(raw string) bool {
	raw = html.UnescapeString(strings.TrimSpace(raw))
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || strings.HasPrefix(raw, "//") {
		return false
	}
	return u.Scheme == "" || strings.EqualFold(u.Scheme, "https") || strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "mailto")
}
func validateAssistantStrings(value any) error {
	switch v := value.(type) {
	case string:
		return validateAssistantMarkdown(v)
	case map[string]any:
		for _, item := range v {
			if err := validateAssistantStrings(item); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range v {
			if err := validateAssistantStrings(item); err != nil {
				return err
			}
		}
	}
	return nil
}
