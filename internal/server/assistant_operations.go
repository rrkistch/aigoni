package server

import (
	"aigoni/internal/configdoc"
	"aigoni/internal/content"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var assistantRequestID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type assistantFields struct {
	Title       *string         `json:"title,omitempty"`
	Description *string         `json:"description,omitempty"`
	Date        *string         `json:"date,omitempty"`
	Slug        *string         `json:"slug,omitempty"`
	Category    *string         `json:"category,omitempty"`
	Tags        *[]string       `json:"tags,omitempty"`
	CoverImage  *string         `json:"cover_image,omitempty"`
	TOC         *bool           `json:"toc,omitempty"`
	Template    *string         `json:"template,omitempty"`
	SourceURL   *string         `json:"source_url,omitempty"`
	Weight      *int            `json:"weight,omitempty"`
	Body        *string         `json:"body,omitempty"`
	Display     *map[string]any `json:"display,omitempty"`
}
type assistantChange struct {
	RequestID string          `json:"request_id"`
	Type      string          `json:"type"`
	ID        string          `json:"id,omitempty"`
	Revision  string          `json:"revision,omitempty"`
	Action    string          `json:"action,omitempty"`
	Fields    assistantFields `json:"fields"`
}
type assistantThemeChange struct {
	RequestID string         `json:"request_id"`
	Revision  uint64         `json:"revision"`
	Groups    map[string]any `json:"groups"`
}
type assistantOperation struct {
	Version            int                `json:"version"`
	ID                 string             `json:"id"`
	RequestID          string             `json:"request_id,omitempty"`
	CredentialID       string             `json:"credential_id,omitempty"`
	Actor              string             `json:"actor"`
	Kind               string             `json:"kind"`
	Status             string             `json:"status"`
	Revision           uint64             `json:"revision"`
	Created            time.Time          `json:"created"`
	Updated            time.Time          `json:"updated"`
	Request            json.RawMessage    `json:"request,omitempty"`
	Title              string             `json:"title"`
	TargetID           string             `json:"target_id,omitempty"`
	Type               content.Type       `json:"type,omitempty"`
	BaseRevision       string             `json:"base_revision,omitempty"`
	BaseConfigValues   json.RawMessage    `json:"base_config_values,omitempty"`
	BaseThemeVersion   string             `json:"base_theme_version,omitempty"`
	BaseConfigRevision uint64             `json:"base_config_revision,omitempty"`
	Input              *content.SaveInput `json:"input,omitempty"`
	Groups             map[string]any     `json:"groups,omitempty"`
	Before             any                `json:"before,omitempty"`
	After              any                `json:"after,omitempty"`
	Saved              bool               `json:"saved"`
	Public             bool               `json:"public"`
	AppliedRevision    string             `json:"applied_revision,omitempty"`
	ConfigRevision     uint64             `json:"config_revision,omitempty"`
	BuildRevision      uint64             `json:"build_revision,omitempty"`
	Error              string             `json:"error,omitempty"`
}

func (s *Server) assistantOperationPath(id string) string {
	return filepath.Join(s.assistantRoot(), "operations", id+".json")
}
func (s *Server) readAssistantOperation(id string) (assistantOperation, error) {
	var op assistantOperation
	if !safeRelease(id) {
		return op, errors.New("无效操作标识")
	}
	data, err := os.ReadFile(s.assistantOperationPath(id))
	if err == nil {
		err = json.Unmarshal(data, &op)
	}
	if err == nil && (op.ID != id || op.Version != 1) {
		err = errors.New("操作记录版本不兼容")
	}
	return op, err
}
func (s *Server) saveAssistantOperation(op *assistantOperation) error {
	op.Updated = time.Now().UTC()
	data, err := json.Marshal(op)
	if err != nil {
		return err
	}
	return configdoc.Atomic(s.assistantOperationPath(op.ID), data)
}
func (s *Server) auditAssistantEvent(kind string, before, after any) error {
	id, err := newWikiAgentRunID()
	if err != nil {
		return err
	}
	title := kind
	if kind == "integration.created" {
		title = "助手凭据已创建"
	}
	if kind == "integration.revoked" {
		title = "助手凭据已撤销"
	}
	if meta, ok := after.(assistantCredentialView); ok {
		title += "：" + meta.Name
	}
	op := assistantOperation{Version: 1, ID: "admin-" + id, Actor: "管理员", Kind: kind, Title: title, Status: "recorded", Revision: 1, Created: time.Now().UTC(), Before: before, After: after}
	return s.saveAssistantOperation(&op)
}
func (s *Server) currentAssistant(c assistantCredential, scope string) error {
	doc, err := s.readAssistantCredentials()
	if err != nil {
		return err
	}
	for _, v := range doc.Value {
		if v.ID == c.ID && !v.Revoked && v.allows(scope) {
			return nil
		}
	}
	return errors.New("助手凭据已撤销或没有所需权限")
}
func assistantContentType(value string) (content.Type, error) {
	switch content.Type(value) {
	case content.TypePost, content.TypePage, content.TypeDynamic, content.TypeProject:
		return content.Type(value), nil
	}
	return "", errors.New("内容类型必须是 post、page、dynamic 或 project")
}
func inputView(input content.SaveInput) map[string]any {
	return map[string]any{"title": input.Title, "description": input.Description, "date": input.Date, "publish": input.Publish, "slug": input.Slug, "category": input.Category, "tags": input.Tags, "cover_image": input.CoverImage, "toc": input.TOC, "template": input.Template, "source_url": input.SourceURL, "weight": input.Weight, "body": input.Body, "display": publicDisplay(input.Display)}
}
func itemInput(item *content.Item) content.SaveInput {
	return content.SaveInput{ID: item.ID, Type: item.Type, Title: item.Title, Description: item.Description, Date: item.Date, Publish: item.Publish, Slug: item.Slug, Category: item.Category, Tags: item.Tags, CoverImage: item.CoverImage, TOC: item.TOC, Template: item.Template, SourceURL: item.SourceURL, Weight: item.Weight, Body: item.Body, Display: item.Display}
}
func (s *Server) newAssistantOperation(c assistantCredential, kind, requestID string, args any) (assistantOperation, bool, error) {
	var empty assistantOperation
	if !assistantRequestID.MatchString(requestID) {
		return empty, false, errors.New("request_id 必须是1至64位字母、数字、下划线或连字符；重试同一操作必须使用原值")
	}
	data, err := json.Marshal(args)
	if err != nil {
		return empty, false, err
	}
	if len(data) > 1<<20 {
		return empty, false, errors.New("单次助手操作最大1MiB")
	}
	id := c.ID + "-" + requestID
	prior, err := s.readAssistantOperation(id)
	if err == nil {
		if prior.Kind != kind || !bytes.Equal(prior.Request, data) {
			return empty, false, errors.New("request_id 已用于不同内容，请勿复用")
		}
		return prior, true, nil
	}
	if !os.IsNotExist(err) {
		return empty, false, err
	}
	return assistantOperation{Version: 1, ID: id, RequestID: requestID, CredentialID: c.ID, Actor: c.Name, Kind: kind, Revision: 1, Created: time.Now().UTC(), Request: data}, false, nil
}
func (s *Server) assistantContentChange(c assistantCredential, args assistantChange, mode string) (assistantOperation, error) {
	scope := "drafts.write"
	if mode == "public" {
		scope = "public.propose"
	}
	if mode == "note" {
		scope = "notes.write"
	}
	s.integrationMu.Lock()
	defer s.integrationMu.Unlock()
	if err := s.currentAssistant(c, scope); err != nil {
		return assistantOperation{}, err
	}
	op, prior, err := s.newAssistantOperation(c, mode, args.RequestID, args)
	if err != nil || prior {
		return op, err
	}
	typ, err := assistantContentType(args.Type)
	if mode == "note" {
		typ = content.TypeNote
		err = nil
	}
	if err != nil {
		return op, err
	}
	if mode == "public" && args.Action != "publish" && args.Action != "withdraw" {
		return op, errors.New("公开候选 action 必须是 publish 或 withdraw")
	}
	payloadBytes, _ := json.Marshal(args.Fields)
	var values any
	_ = json.Unmarshal(payloadBytes, &values)
	if err = validateAssistantStrings(values); err != nil {
		return op, err
	}
	if args.Fields.CoverImage != nil && !safeAssistantLink(*args.Fields.CoverImage) {
		return op, errors.New("封面链接协议不受支持")
	}
	if args.Fields.SourceURL != nil && !safeAssistantLink(*args.Fields.SourceURL) {
		return op, errors.New("来源链接协议不受支持")
	}
	var payload adminContentPayload
	_ = json.Unmarshal(payloadBytes, &payload)
	if payload.Body != nil {
		if err = validateAssistantMarkdown(*payload.Body); err != nil {
			return op, err
		}
	}
	s.contentWriteMu.Lock()
	var current *content.Item
	var input content.SaveInput
	if args.ID != "" {
		id, e := decodeAdminContentID(args.ID)
		if e != nil {
			s.contentWriteMu.Unlock()
			return op, e
		}
		current, err = s.repo.GetByID(id, typ)
		if err != nil {
			s.contentWriteMu.Unlock()
			return op, errors.New("目标内容不存在")
		}
		if mode == "draft" && current.Publish {
			s.contentWriteMu.Unlock()
			return op, errors.New("公开记录不能直接改写或撤回，请提交公开候选并等待管理员审批")
		}
		if mode == "public" && !current.Publish && !c.allows("drafts.write") {
			s.contentWriteMu.Unlock()
			return op, errors.New("操作未公开记录还需要 drafts.write 权限")
		}
		revision, e := content.Revision(current.Path)
		if e != nil {
			s.contentWriteMu.Unlock()
			return op, e
		}
		if strings.Trim(strings.TrimSpace(args.Revision), `"`) == "" || strings.Trim(strings.TrimSpace(args.Revision), `"`) != revision {
			s.contentWriteMu.Unlock()
			return op, content.ErrRevisionConflict
		}
		op.BaseRevision = revision
		op.Before = inputView(itemInput(current))
		var fields map[string]string
		input, fields = patchInput(payload, current)
		if len(fields) > 0 {
			s.contentWriteMu.Unlock()
			return op, fmt.Errorf("字段不合法：%v", fields)
		}
	} else {
		if mode == "public" && args.Action == "withdraw" {
			s.contentWriteMu.Unlock()
			return op, errors.New("撤回候选必须指定现有内容")
		}
		if payload.Date == nil {
			date := op.Created.Format(time.RFC3339)
			payload.Date = &date
		}
		var fields map[string]string
		input, fields = createInput(payload, typ)
		if len(fields) > 0 {
			s.contentWriteMu.Unlock()
			return op, fmt.Errorf("字段不合法：%v", fields)
		}
		input.ID = "assistant/" + op.ID
	}
	if mode == "public" {
		input.Publish = args.Action == "publish"
	} else {
		input.Publish = false
	}
	op.Title = input.Title
	op.Type = typ
	op.TargetID = input.ID
	op.Input = &input
	op.After = inputView(input)
	op.Public = mode == "public"
	if mode == "public" {
		op.Status = "pending"
		s.contentWriteMu.Unlock()
		err = s.saveAssistantOperation(&op)
		return op, err
	}
	op.Status = "applying"
	if err = s.saveAssistantOperation(&op); err != nil {
		s.contentWriteMu.Unlock()
		return op, err
	}
	var saved *content.Item
	var revision string
	oldNote := ""
	if current != nil && typ == content.TypeNote {
		oldNote = noteSourcePath(s.root, current)
	}
	if current == nil {
		saved, revision, err = content.NewService(s.repo).Create(input)
	} else {
		saved, revision, err = content.NewService(s.repo).Update(input, op.BaseRevision)
	}
	if err == nil {
		op.Saved = true
		op.AppliedRevision = revision
		op.TargetID = saved.ID
		actual := itemInput(saved)
		op.Input = &actual
		op.After = inputView(actual)
		op.Status = "saved"
		if oldNote != "" {
			if e := s.syncWikiNoteRename(oldNote, noteSourcePath(s.root, saved)); e != nil {
				op.Status = "saved_warning"
				op.Error = "笔记已保存，Wiki来源引用同步失败，请在后台检查。"
			}
		}
	} else {
		op.Status = "failed"
		op.Error = err.Error()
	}
	s.contentWriteMu.Unlock()
	if e := s.saveAssistantOperation(&op); e != nil {
		return op, errors.New("保存结果状态写入失败，请用原 request_id 查询，勿换标识重试")
	}
	return op, err
}

var assistantThemeGroups = map[string]bool{"appearance": true, "wallpaper": true, "pages": true, "navigation": true, "sidebar": true, "music": true, "announcement": true, "friends": true, "booknav": true, "reading": true, "cover": true, "effects": true, "fonts": true, "sponsor": true, "display": true, "profilelinks": true, "footer": true, "license": true, "gallery": true}

func (s *Server) assistantThemeChange(c assistantCredential, args assistantThemeChange) (assistantOperation, error) {
	s.integrationMu.Lock()
	defer s.integrationMu.Unlock()
	if err := s.currentAssistant(c, "theme.propose"); err != nil {
		return assistantOperation{}, err
	}
	op, prior, err := s.newAssistantOperation(c, "theme", args.RequestID, args)
	if err != nil || prior {
		return op, err
	}
	if len(args.Groups) == 0 {
		return op, errors.New("请选择要修改的配置组")
	}
	s.themeMu.Lock()
	doc, err := s.readThemeSettings()
	s.themeMu.Unlock()
	if err != nil {
		return op, err
	}
	if doc.Revision != args.Revision {
		return op, configdoc.ErrConflict
	}
	defaults := s.themeDefaults()
	before := map[string]any{}
	after := map[string]any{}
	prepared := map[string]any{}
	for group, value := range args.Groups {
		field, ok := adminSchema()[group]
		if !ok || !assistantThemeGroups[group] {
			return op, fmt.Errorf("助手不管理配置组 %s；服务账号、评论和统计请在后台配置", group)
		}
		if value != nil {
			if err = validateAdminValue(field, value, field.Label); err != nil {
				return op, err
			}
			if err = validateAssistantStrings(value); err != nil {
				return op, err
			}
		}
		effective := defaults[group]
		if existing, ok := doc.Value[group]; ok {
			effective = mergeAdminDefaults(effective, existing)
		}
		before[group] = projectAdminValue(field, effective)
		after[group] = projectAdminValue(field, defaults[group])
		prepared[group] = nil
		if value != nil {
			prepared[group] = mergeAdminDefaults(doc.Value[group], value)
			after[group] = projectAdminValue(field, mergeAdminDefaults(defaults[group], prepared[group]))
		}
	}
	if previous, ok := before["music"]; ok {
		at := func(value any, key string) any {
			m, _ := value.(map[string]any)
			meting, _ := m["meting"].(map[string]any)
			return meting[key]
		}
		for _, key := range []string{"api", "fallbackApis"} {
			a, _ := json.Marshal(at(previous, key))
			b, _ := json.Marshal(at(after["music"], key))
			if !bytes.Equal(a, b) {
				return op, errors.New("Meting接口地址属于服务接入，请在后台手动修改；助手可维护歌曲和歌单ID")
			}
		}
	}
	op.Status = "pending"
	op.Title = "外观与模块变更"
	op.BaseConfigRevision = doc.Revision
	op.BaseConfigValues, _ = json.Marshal(doc.Value)
	op.BaseThemeVersion = s.activeThemeVersion()
	op.Groups = prepared
	op.Before = before
	op.After = after
	op.Public = true
	err = s.saveAssistantOperation(&op)
	return op, err
}
func (s *Server) assistantOperationInfo(op assistantOperation) map[string]any {
	status := op.Status
	if status == "applying" {
		status = "needs_review"
	}
	publication := "not_applied"
	if op.Saved && !op.Public {
		publication = "draft_only"
	}
	if op.Saved && op.Public {
		publication = "queue_failed"
		if op.BuildRevision > 0 && s.siteBuild != nil {
			s.siteBuild.mu.Lock()
			build := s.siteBuild.state
			s.siteBuild.mu.Unlock()
			publication = "queued"
			if build.Blocked {
				publication = "blocked"
			} else if build.Status == "failed" {
				publication = "failed"
			} else if build.PublishedRevision >= op.BuildRevision {
				publication = "published"
				if !s.assistantPublishedMatches(op) {
					publication = "superseded"
				}
			}
		}
	}
	return map[string]any{"id": op.ID, "request_id": op.RequestID, "kind": op.Kind, "status": status, "revision": op.Revision, "title": op.Title, "actor": op.Actor, "target_id": op.TargetID, "type": op.Type, "created": op.Created, "updated": op.Updated, "saved": op.Saved, "public_applied": op.Saved && op.Public, "publication_status": publication, "build_revision": op.BuildRevision, "error": op.Error}
}
func (s *Server) applyAssistantApproval(op *assistantOperation) error {
	doc, err := s.readAssistantCredentials()
	if err != nil {
		return err
	}
	active := false
	for _, c := range doc.Value {
		if c.ID == op.CredentialID && !c.Revoked {
			scope := "public.propose"
			if op.Kind == "theme" {
				scope = "theme.propose"
			}
			active = c.allows(scope)
		}
	}
	if !active {
		return errors.New("助手凭据已撤销或权限已变化，请使用有效凭据重新提交候选")
	}
	// Persist the applying intent before the single side effect. Interrupted intents are never replayed automatically.
	if op.Kind == "theme" {
		s.themeMu.Lock()
		current, e := s.readThemeSettings()
		s.themeMu.Unlock()
		if e != nil {
			return e
		}
		currentValues, _ := json.Marshal(current.Value)
		if s.activeThemeVersion() != op.BaseThemeVersion || !bytes.Equal(currentValues, op.BaseConfigValues) {
			return configdoc.ErrConflict
		}
		if current.Revision != op.BaseConfigRevision {
			return configdoc.ErrConflict
		}
		op.Status = "applying"
		if err = s.saveAssistantOperation(op); err != nil {
			return err
		}
		saved, e := s.changeThemeSettings(op.BaseConfigRevision, op.Groups)
		if e != nil {
			return e
		}
		op.ConfigRevision = saved.Revision
		op.Saved = true
	} else {
		if op.Input == nil {
			return errors.New("候选内容不完整")
		}
		s.contentWriteMu.Lock()
		defer s.contentWriteMu.Unlock()
		var current *content.Item
		if op.BaseRevision != "" {
			current, err = s.repo.GetByID(op.TargetID, op.Type)
			if err != nil {
				return err
			}
			revision, e := content.Revision(current.Path)
			if e != nil {
				return e
			}
			if revision != op.BaseRevision {
				return content.ErrRevisionConflict
			}
		} else {
			if _, e := s.repo.GetByID(op.TargetID, op.Type); e == nil {
				return content.ErrRevisionConflict
			}
		}
		op.Status = "applying"
		if err = s.saveAssistantOperation(op); err != nil {
			return err
		}
		var saved *content.Item
		var revision string
		if current == nil {
			saved, revision, err = content.NewService(s.repo).Create(*op.Input)
		} else {
			saved, revision, err = content.NewService(s.repo).Update(*op.Input, op.BaseRevision)
		}
		if err != nil {
			return err
		}
		op.TargetID = saved.ID
		op.AppliedRevision = revision
		actual := itemInput(saved)
		op.Input = &actual
		op.After = inputView(actual)
		op.Saved = true
	}
	return nil
}
func (s *Server) approvalsAPI(w http.ResponseWriter, r *http.Request) {
	s.integrationMu.Lock()
	defer s.integrationMu.Unlock()
	id := r.PathValue("id")
	if id == "" {
		s.assistantRecordsAPI(w, r, true)
		return
	}
	op, err := s.readAssistantOperation(id)
	if err != nil {
		writeAdminError(w, 404, "approval_not_found", "审批不存在。")
		return
	}
	if r.Method == "GET" {
		writeAdminJSON(w, 200, map[string]any{"operation": s.assistantOperationInfo(op), "before": op.Before, "after": op.After, "base_revision": op.BaseRevision, "base_config_revision": op.BaseConfigRevision})
		return
	}
	var req struct {
		Revision uint64 `json:"revision"`
		Reason   string `json:"reason,omitempty"`
	}
	if !decodeAdminWikiJSON(w, r, &req, false) {
		return
	}
	if op.Saved {
		writeAdminJSON(w, 200, s.assistantOperationInfo(op))
		return
	}
	if op.Status != "pending" || op.Revision != req.Revision {
		writeAdminError(w, 409, "approval_changed", "审批已变化或已处理，请刷新；中断的应用状态需要人工核对，不能直接重放。")
		return
	}
	op.Revision++
	if strings.HasSuffix(r.URL.Path, "/reject") {
		if len(req.Reason) > 4000 {
			writeAdminError(w, 400, "invalid_reason", "拒绝说明过长。")
			return
		}
		op.Status = "rejected"
		op.Error = strings.TrimSpace(req.Reason)
		if err = s.saveAssistantOperation(&op); err != nil {
			writeAdminError(w, 500, "approval_save_failed", "拒绝状态保存失败。")
			return
		}
		writeAdminJSON(w, 200, s.assistantOperationInfo(op))
		return
	}
	err = s.applyAssistantApproval(&op)
	if err != nil {
		if op.Status == "applying" && !errors.Is(err, content.ErrRevisionConflict) && !errors.Is(err, content.ErrSlugConflict) && !errors.Is(err, configdoc.ErrConflict) {
			op.Status = "needs_review"
		} else {
			op.Status = "stale"
		}
		op.Error = err.Error()
		_ = s.saveAssistantOperation(&op)
		writeAdminError(w, 409, "approval_not_applied", op.Error)
		return
	}
	op.Status = "saved"
	op.Error = ""
	if err = s.queueSiteBuild(false); err != nil {
		op.Error = "内容已保存，但构建排队失败；请在概览重试。"
	} else if s.siteBuild != nil {
		s.siteBuild.mu.Lock()
		op.BuildRevision = s.siteBuild.state.Revision
		s.siteBuild.mu.Unlock()
	}
	if err = s.saveAssistantOperation(&op); err != nil {
		writeAdminError(w, 503, "approval_result_unknown", "修改可能已保存，请查询同一审批并人工核对，不要创建重复候选。")
		return
	}
	writeAdminJSON(w, 200, s.assistantOperationInfo(op))
}
func (s *Server) assistantRecordsAPI(w http.ResponseWriter, r *http.Request, pending bool) {
	files, _ := os.ReadDir(filepath.Join(s.assistantRoot(), "operations"))
	sort.Slice(files, func(i, j int) bool {
		a, _ := files[i].Info()
		b, _ := files[j].Info()
		if a == nil || b == nil {
			return files[i].Name() > files[j].Name()
		}
		return a.ModTime().After(b.ModTime())
	})
	items := []map[string]any{}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") || file.IsDir() {
			continue
		}
		op, err := s.readAssistantOperation(strings.TrimSuffix(file.Name(), ".json"))
		if err != nil || pending && op.Status != "pending" {
			continue
		}
		items = append(items, s.assistantOperationInfo(op))
		if len(items) >= 100 {
			break
		}
	}
	writeAdminJSON(w, 200, map[string]any{"items": items})
}
func (s *Server) auditAPI(w http.ResponseWriter, r *http.Request) {
	s.integrationMu.Lock()
	defer s.integrationMu.Unlock()
	s.assistantRecordsAPI(w, r, false)
}

func (s *Server) assistantPublishedMatches(op assistantOperation) bool {
	if s.siteBuild == nil {
		return false
	}
	s.siteBuild.mu.Lock()
	data := append([]byte(nil), s.siteBuild.published...)
	s.siteBuild.mu.Unlock()
	var snap fireflySnapshot
	if json.Unmarshal(data, &snap) != nil {
		return false
	}
	equal := func(a, b any) bool { x, _ := json.Marshal(a); y, _ := json.Marshal(b); return bytes.Equal(x, y) }
	if op.Kind == "theme" {
		for group, value := range op.Groups {
			current, exists := snap.Theme[group]
			if value == nil {
				if exists {
					return false
				}
			} else if !equal(current, value) {
				return false
			}
		}
		return true
	}
	if op.Input == nil {
		return false
	}
	var items []fireflyPost
	switch op.Type {
	case content.TypePost:
		items = snap.Posts
	case content.TypePage:
		items = snap.Pages
	case content.TypeDynamic:
		items = snap.Dynamics
	case content.TypeProject:
		items = snap.Projects
	}
	for _, item := range items {
		if item.Slug == op.Input.Slug {
			if !op.Input.Publish {
				return false
			}
			return item.Title == op.Input.Title && item.Description == op.Input.Description && item.Body == op.Input.Body && item.Category == op.Input.Category && item.Image == op.Input.CoverImage && item.TOC == op.Input.TOC && item.Weight == op.Input.Weight && item.SourceLink == op.Input.SourceURL && item.Published.Equal(op.Input.Date) && (len(item.Tags) == 0 && len(op.Input.Tags) == 0 || equal(item.Tags, op.Input.Tags)) && equal(publicDisplay(item.Display), publicDisplay(op.Input.Display))
		}
	}
	return !op.Input.Publish
}
