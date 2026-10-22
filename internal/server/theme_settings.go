package server

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"aigoni/internal/configdoc"
)

//go:embed admin_schema.json
var adminSchemaJSON []byte

type adminField struct {
	Required   []string              `json:"required,omitempty"`
	Type       string                `json:"type"`
	Label      string                `json:"label"`
	Format     string                `json:"format,omitempty"`
	Enum       []string              `json:"enum,omitempty"`
	Min        *float64              `json:"min,omitempty"`
	Max        *float64              `json:"max,omitempty"`
	MaxItems   int                   `json:"maxItems,omitempty"`
	Properties map[string]adminField `json:"properties,omitempty"`
	Items      *adminField           `json:"items,omitempty"`
}

func adminSchema() map[string]adminField {
	var schema map[string]adminField
	if err := json.Unmarshal(adminSchemaJSON, &schema); err != nil {
		panic(err)
	}
	return schema
}
func validateAdminValue(f adminField, v any, where string) error {
	bad := func() error { return fmt.Errorf("%s：值的类型或范围不正确", where) }
	switch f.Type {
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			return bad()
		}
		for _, key := range f.Required {
			val, exists := m[key]
			text, isText := val.(string)
			if !exists || val == nil || (isText && strings.TrimSpace(text) == "") {
				return fmt.Errorf("%s：%s 不能为空", where, f.Properties[key].Label)
			}
		}
		for k, val := range m {
			child, ok := f.Properties[k]
			if !ok {
				return fmt.Errorf("%s：不支持配置项 %s", where, k)
			}
			if err := validateAdminValue(child, val, where+" / "+child.Label); err != nil {
				return err
			}
		}
	case "array":
		list, ok := v.([]any)
		if !ok || len(list) > f.MaxItems || f.Items == nil {
			return bad()
		}
		for i, val := range list {
			if err := validateAdminValue(*f.Items, val, fmt.Sprintf("%s [%d]", where, i+1)); err != nil {
				return err
			}
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return bad()
		}
	case "number":
		n, ok := v.(float64)
		if !ok || (f.Min != nil && n < *f.Min) || (f.Max != nil && n > *f.Max) {
			return bad()
		}
	case "string":
		s, ok := v.(string)
		if !ok || len(s) > 16384 {
			return bad()
		}
		if f.Format == "slug" && (!safeRelease(s) || s == "" || strings.ContainsAny(s, " ?#%")) {
			return bad()
		}
		if len(f.Enum) > 0 {
			found := false
			for _, option := range f.Enum {
				if s == option {
					found = true
				}
			}
			if !found {
				return bad()
			}
		}
		if (f.Format == "url" && !safeConfigURL(s)) || (f.Format == "contact-url" && !safeContactURL(s)) {
			return fmt.Errorf("%s：只允许站内资源路径或 HTTP(S) 地址", where)
		}
	default:
		return bad()
	}
	return nil
}
func safeConfigURL(v string) bool {
	if v == "" || v == "#" {
		return true
	}
	if strings.ContainsAny(v, "\\\r\n\x00") || strings.HasPrefix(v, "//") {
		return false
	}
	u, err := url.Parse(v)
	if err != nil || u.User != nil {
		return false
	}
	if u.Scheme != "" {
		return (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != ""
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}
func (s *Server) themeSettingsPath() string {
	return filepath.Join(s.root, ".aigoni-admin", "theme.json")
}
func (s *Server) readThemeSettings() (configdoc.Document[map[string]any], error) {
	return configdoc.Read(s.themeSettingsPath(), map[string]any{})
}
func (s *Server) publicThemeSettings() (map[string]any, error) {
	s.themeMu.Lock()
	defer s.themeMu.Unlock()
	d, err := s.readThemeSettings()
	return d.Value, err
}
func (s *Server) themeDefaults() map[string]any {
	source := s.activeThemeSource()
	b, err := os.ReadFile(filepath.Join(source, "aigoni-defaults.json"))
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if json.Unmarshal(b, &out) != nil {
		return map[string]any{}
	}
	return out
}
func mergeAdminDefaults(base, patch any) any {
	a, ok := base.(map[string]any)
	b, ok2 := patch.(map[string]any)
	if !ok || !ok2 {
		return patch
	}
	result := map[string]any{}
	for k, v := range a {
		result[k] = v
	}
	for k, v := range b {
		if old, exists := result[k]; exists {
			result[k] = mergeAdminDefaults(old, v)
		} else {
			result[k] = v
		}
	}
	return result
}
func projectAdminValue(f adminField, v any) any {
	if f.Type == "object" {
		m, _ := v.(map[string]any)
		out := map[string]any{}
		for k, child := range f.Properties {
			if val, ok := m[k]; ok {
				out[k] = projectAdminValue(child, val)
			}
		}
		return out
	}
	if f.Type == "array" {
		items, ok := v.([]any)
		if !ok {
			if text, yes := v.(string); yes && text != "" {
				items = []any{text}
			}
		}
		out := []any{}
		for _, item := range items {
			out = append(out, projectAdminValue(*f.Items, item))
		}
		return out
	}
	return v
}
func (s *Server) themeSettingsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		s.themeMu.Lock()
		d, err := s.readThemeSettings()
		s.themeMu.Unlock()
		if err != nil {
			writeAdminError(w, 500, "theme_config_unavailable", "无法读取外观设置。")
			return
		}
		defaults := s.themeDefaults()
		schema := adminSchema()
		effective := map[string]any{}
		cleanDefaults := map[string]any{}
		for name, field := range schema {
			base := defaults[name]
			cleanDefaults[name] = projectAdminValue(field, base)
			if patch, ok := d.Value[name]; ok {
				base = mergeAdminDefaults(base, patch)
			}
			effective[name] = projectAdminValue(field, base)
		}
		writeAdminJSON(w, 200, map[string]any{"revision": d.Revision, "groups": d.Value, "effective": effective, "defaults": cleanDefaults, "schema": json.RawMessage(adminSchemaJSON)})
		return
	}
	var req struct {
		Revision uint64         `json:"revision"`
		Groups   map[string]any `json:"groups"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if !decodeAdminWikiJSON(w, r, &req, false) {
		return
	}
	schema := adminSchema()
	for name, v := range req.Groups {
		f, ok := schema[name]
		if !ok {
			writeAdminError(w, 400, "unknown_group", "未知配置分组。")
			return
		}
		if v != nil {
			if err := validateAdminValue(f, v, f.Label); err != nil {
				writeAdminError(w, 400, "invalid_setting", err.Error())
				return
			}
		}
	}
	d, err := s.changeThemeSettings(req.Revision, req.Groups)
	if errors.Is(err, configdoc.ErrConflict) {
		writeAdminError(w, 409, "revision_conflict", "设置已被其他页面修改，请重新加载。")
		return
	}
	if errors.Is(err, errThemeIncompatible) {
		writeAdminError(w, 409, "theme_incompatible", err.Error())
		return
	}
	if err != nil {
		writeAdminError(w, 500, "theme_save_failed", "外观设置保存失败。")
		return
	}
	buildErr := s.queueSiteBuild(false)
	status := "queued"
	if buildErr != nil {
		status = "failed"
	}
	if s.siteBuild == nil {
		status = "disabled"
	}
	writeAdminJSON(w, 200, map[string]any{"revision": d.Revision, "groups": d.Value, "saved": true, "build_status": status})
}
func (s *Server) trimThemeHistory() {
	dir := filepath.Join(s.root, ".aigoni-admin", "theme-history")
	files, _ := os.ReadDir(dir)
	names := []string{}
	for _, f := range files {
		if !f.IsDir() && strings.HasSuffix(f.Name(), ".json") {
			names = append(names, f.Name())
		}
	}
	sort.Strings(names)
	for len(names) > 20 {
		_ = os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
}

func safeContactURL(value string) bool {
	if strings.HasPrefix(value, "mailto:") {
		raw := strings.TrimPrefix(value, "mailto:")
		if strings.ContainsAny(raw, "?\r\n") {
			return false
		}
		_, err := mail.ParseAddress(raw)
		return err == nil
	}
	return safeConfigURL(value)
}

var errThemeIncompatible = errors.New("theme configuration incompatible")

// changeThemeSettings is the revision-checked write boundary shared by admin, history and future MCP approvals.
func (s *Server) changeThemeSettings(expected uint64, groups map[string]any) (configdoc.Document[map[string]any], error) {
	schema := adminSchema()
	for name, value := range groups {
		field, ok := schema[name]
		if !ok {
			return configdoc.Document[map[string]any]{}, fmt.Errorf("unknown configuration group")
		}
		if value != nil {
			if err := validateAdminValue(field, value, field.Label); err != nil {
				return configdoc.Document[map[string]any]{}, err
			}
		}
	}
	s.themeMu.Lock()
	d, err := s.readThemeSettings()
	if err == nil && d.Revision != expected {
		err = configdoc.ErrConflict
	}
	if err == nil {
		next := map[string]any{}
		for k, v := range d.Value {
			next[k] = v
		}
		for k, v := range groups {
			if v == nil {
				delete(next, k)
			} else {
				next[k] = v
			}
		}
		// Save the previous revision before replacing it, so failed writes never erase history.
		if d.Revision > 0 {
			b, _ := json.Marshal(d)
			err = configdoc.Atomic(filepath.Join(s.root, ".aigoni-admin", "theme-history", fmt.Sprintf("%020d.json", d.Revision)), b)
		}
		if err == nil {
			if s.siteBuild != nil {
				s.siteBuild.mu.Lock()
				version := s.siteBuild.state.Theme
				candidate := s.siteBuild.state.PendingTheme
				s.siteBuild.mu.Unlock()
				for _, v := range []string{version, candidate} {
					if v != "" && v != "source" {
						source, e := s.themeSource(v)
						if e == nil {
							e = validateThemeOverrides(source, next)
						}
						if e != nil {
							s.themeMu.Unlock()
							return d, fmt.Errorf("%w: %v", errThemeIncompatible, e)
						}
					}
				}
			}
			d, err = configdoc.Write(s.themeSettingsPath(), d, expected, next)
		}
		if err == nil {
			s.trimThemeHistory()
		}
	}
	s.themeMu.Unlock()
	return d, err
}
