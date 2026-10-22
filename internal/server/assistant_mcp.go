package server

import (
	"aigoni/internal/configdoc"
	"aigoni/internal/content"
	"context"
	"encoding/json"
	"errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/url"
	"strings"
)

type assistantActorKey struct{}
type assistantEmpty struct{}
type assistantListArgs struct {
	Type          string `json:"type"`
	Query         string `json:"query,omitempty"`
	IncludeDrafts bool   `json:"include_drafts,omitempty"`
	Page          int    `json:"page,omitempty"`
}
type assistantGetArgs struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}
type assistantNoteArgs struct {
	RequestID string          `json:"request_id"`
	ID        string          `json:"id,omitempty"`
	Revision  string          `json:"revision,omitempty"`
	Fields    assistantFields `json:"fields"`
}
type assistantNoteGet struct {
	ID     string `json:"id"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}
type assistantQuery struct {
	Query string `json:"query,omitempty"`
	Page  int    `json:"page,omitempty"`
}
type assistantOperationQuery struct {
	RequestID string `json:"request_id"`
}
type assistantGroupQuery struct {
	Group string `json:"group"`
}
type assistantWikiQuery struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

func assistantToolResult(value any, err error) (*mcp.CallToolResult, any, error) {
	result := &mcp.CallToolResult{}
	if err != nil {
		code := "operation_failed"
		if errors.Is(err, content.ErrRevisionConflict) || errors.Is(err, configdoc.ErrConflict) {
			code = "revision_conflict"
		}
		if errors.Is(err, content.ErrSlugConflict) {
			code = "slug_conflict"
		}
		value = map[string]any{"error": map[string]string{"code": code, "message": err.Error()}}
		result.IsError = true
	}
	data, e := json.Marshal(value)
	if e != nil {
		return nil, nil, e
	}
	result.Content = []mcp.Content{&mcp.TextContent{Text: string(data)}}
	return result, nil, nil
}
func addAssistantTool[In any](server *mcp.Server, name, description string, fn func(context.Context, In) (any, error)) {
	writes := name == "draft_save" || name == "note_save" || name == "public_change_propose" || name == "theme_change_propose"
	destructive := name == "draft_save" || name == "note_save"
	openWorld := false
	mcp.AddTool(server, &mcp.Tool{Name: name, Description: description, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: !writes, IdempotentHint: true, DestructiveHint: &destructive, OpenWorldHint: &openWorld}}, func(ctx context.Context, _ *mcp.CallToolRequest, args In) (*mcp.CallToolResult, any, error) {
		if err := ctx.Err(); err != nil {
			return assistantToolResult(nil, err)
		}
		value, err := fn(ctx, args)
		return assistantToolResult(value, err)
	})
}

// Note: Dedicated authority, immutable candidates and interrupted-write handling are documented in docs/decisions/hermes-access.md.
func (s *Server) mcpHTTPHandler() http.Handler {
	transport := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		c, _ := r.Context().Value(assistantActorKey{}).(assistantCredential)
		return s.assistantMCPServer(c)
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 2 << 20, DisableLocalhostProtection: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || mcpAuthority(u.Host, u.Scheme) != mcpAuthority(r.Host, u.Scheme) {
				writeAdminError(w, 403, "origin_rejected", "MCP 不允许跨站浏览器请求。")
				return
			}
		}
		// SDK's loopback-only Host rule is replaced by a stricter credential-bound authority rule.
		// This also permits an explicitly chosen Docker service hostname behind a reverse proxy.
		c, err := s.assistantAuthenticate(r)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="aigoni-mcp"`)
			writeAdminError(w, 401, "assistant_unauthorized", err.Error())
			return
		}
		transport.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), assistantActorKey{}, c)))
	})
}
func (s *Server) assistantMCPServer(c assistantCredential) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "aigoni", Version: "1.0.0"}, &mcp.ServerOptions{Instructions: "Use one stable request_id per logical mutation and reuse it for retries. Public edits, publication, withdrawal and theme changes are candidates only until a human administrator approves. Never describe saved or queued data as published: query operation_get and require publication_status=published. needs_review means an interrupted write must be checked by a human; do not replay it under a new request_id. Write Markdown, not raw HTML or scripts. Private notes and Wiki require separate scopes. No model calls are made by these tools."})
	if c.allows("public.read") || c.allows("drafts.write") {
		addAssistantTool(server, "content_list", "List post, page, dynamic or project. Public only unless include_drafts=true and drafts.write is granted. No private notes.", func(_ context.Context, a assistantListArgs) (any, error) {
			typ, err := assistantContentType(a.Type)
			if err != nil {
				return nil, err
			}
			return s.assistantContentList(c, typ, a.Query, a.IncludeDrafts, a.Page)
		})
		addAssistantTool(server, "content_get", "Read one listed content ID and its revision. Use offset/limit for long Markdown. Never reads private notes.", func(_ context.Context, a assistantGetArgs) (any, error) {
			typ, err := assistantContentType(a.Type)
			if err != nil {
				return nil, err
			}
			return s.assistantContentGet(c, typ, a.ID, a.Offset, a.Limit)
		})
	}
	if c.allows("public.read") {
		addAssistantTool(server, "site_build_status", "Query actual public build state. saved and queued are not published.", func(_ context.Context, _ assistantEmpty) (any, error) {
			if s.siteBuild == nil {
				return map[string]any{"enabled": false}, nil
			}
			s.siteBuild.mu.Lock()
			defer s.siteBuild.mu.Unlock()
			return s.siteBuild.state, nil
		})
	}
	if c.allows("drafts.write") {
		addAssistantTool(server, "draft_save", "Create or edit an unpublished post/page/dynamic/project. Fields are a patch; new content requires title and slug. Updating requires the exact revision. Reuse request_id. Published records cannot be directly changed.", func(_ context.Context, a assistantChange) (any, error) {
			op, err := s.assistantContentChange(c, a, "draft")
			if err != nil {
				return nil, err
			}
			return s.assistantOperationInfo(op), nil
		})
	}
	if c.allows("public.propose") {
		addAssistantTool(server, "public_change_propose", "Create immutable publish/withdraw candidate for human approval. action=publish also handles edits to published records. Existing ID requires revision. No live content changes occur now. Fields are a patch; raw HTML/scripts are rejected.", func(_ context.Context, a assistantChange) (any, error) {
			op, err := s.assistantContentChange(c, a, "public")
			if err != nil {
				return nil, err
			}
			return s.assistantOperationInfo(op), nil
		})
	}
	if c.allows("theme.propose") {
		addAssistantTool(server, "theme_read", "Read one editable appearance/content group, its schema and current revision. Service credentials, comments and analytics are not manageable by this scope.", func(_ context.Context, a assistantGroupQuery) (any, error) {
			if !assistantThemeGroups[a.Group] {
				return nil, errors.New("此配置组不属于助手外观授权")
			}
			s.themeMu.Lock()
			doc, err := s.readThemeSettings()
			s.themeMu.Unlock()
			if err != nil {
				return nil, err
			}
			field := adminSchema()[a.Group]
			base := s.themeDefaults()[a.Group]
			effective := base
			if value, ok := doc.Value[a.Group]; ok {
				effective = mergeAdminDefaults(base, value)
			}
			return map[string]any{"revision": doc.Revision, "group": a.Group, "schema": field, "value": projectAdminValue(field, effective)}, nil
		})
		addAssistantTool(server, "theme_change_propose", "Submit selected theme/module fields for human approval. Existing overrides are preserved; arrays replace complete lists, null resets a group. Read theme_read first and pass its revision. No HTML/scripts or service-account management.", func(_ context.Context, a assistantThemeChange) (any, error) {
			op, err := s.assistantThemeChange(c, a)
			if err != nil {
				return nil, err
			}
			return s.assistantOperationInfo(op), nil
		})
	}
	if c.allows("notes.read") || c.allows("notes.write") {
		addAssistantTool(server, "notes_list", "Read private note/collection summaries under the explicitly granted notes scope.", func(_ context.Context, a assistantQuery) (any, error) {
			return s.assistantContentList(c, content.TypeNote, a.Query, true, a.Page)
		})
		addAssistantTool(server, "note_get", "Read a private note and revision under the explicitly granted notes scope.", func(_ context.Context, a assistantNoteGet) (any, error) {
			return s.assistantContentGet(c, content.TypeNote, a.ID, a.Offset, a.Limit)
		})
	}
	if c.allows("notes.write") {
		addAssistantTool(server, "note_save", "Create/edit a private note. Updating requires its exact revision. No publication or deletion. Reuse request_id for retries.", func(_ context.Context, a assistantNoteArgs) (any, error) {
			op, err := s.assistantContentChange(c, assistantChange{RequestID: a.RequestID, Type: "note", ID: a.ID, Revision: a.Revision, Fields: a.Fields}, "note")
			if err != nil {
				return nil, err
			}
			return s.assistantOperationInfo(op), nil
		})
	}
	if c.allows("wiki.read") {
		addAssistantTool(server, "wiki_documents", "List canonical Wiki Markdown paths. Read only; never writes files or calls a model.", func(_ context.Context, _ assistantEmpty) (any, error) {
			docs, err := s.readWikiDocs()
			if err != nil {
				return nil, errors.New("Wiki 文档暂不可读")
			}
			paths := []string{}
			for _, doc := range docs {
				if strings.HasPrefix(doc.Path, "wiki/") {
					paths = append(paths, doc.Path)
				}
			}
			return map[string]any{"paths": paths}, nil
		})
		addAssistantTool(server, "wiki_read", "Read one path returned by wiki_documents. No access to raw notes, hidden files or arbitrary filesystem paths.", func(_ context.Context, a assistantWikiQuery) (any, error) {
			docs, err := s.readWikiDocs()
			if err != nil {
				return nil, errors.New("Wiki 文档暂不可读")
			}
			found := false
			for _, doc := range docs {
				if doc.Path == a.Path && strings.HasPrefix(doc.Path, "wiki/") {
					found = true
					break
				}
			}
			if !found {
				return nil, errors.New("文档不属于已授权的规范 Wiki 路径")
			}
			data, err := s.readPreviewPath(a.Path)
			if err != nil {
				return nil, errors.New("Wiki 文档不可读")
			}
			body, _ := splitWikiPreviewFrontMatter(string(data))
			return assistantTextChunk(body, a.Offset, a.Limit), nil
		})
	}
	addAssistantTool(server, "operation_get", "Query this credential's own operation by request_id. Require publication_status=published before claiming live publication. Other credentials' operations are inaccessible.", func(_ context.Context, a assistantOperationQuery) (any, error) {
		if !assistantRequestID.MatchString(a.RequestID) {
			return nil, errors.New("request_id 格式无效")
		}
		s.integrationMu.Lock()
		defer s.integrationMu.Unlock()
		op, err := s.readAssistantOperation(c.ID + "-" + a.RequestID)
		if err != nil || op.CredentialID != c.ID {
			return nil, errors.New("操作不存在")
		}
		return s.assistantOperationInfo(op), nil
	})
	return server
}
func assistantTextChunk(body string, offset, limit int) map[string]any {
	chars := []rune(body)
	if offset < 0 {
		offset = 0
	}
	if offset > len(chars) {
		offset = len(chars)
	}
	if limit <= 0 {
		limit = 20000
	}
	if limit > 100000 {
		limit = 100000
	}
	end := offset + limit
	if end > len(chars) {
		end = len(chars)
	}
	return map[string]any{"body": string(chars[offset:end]), "offset": offset, "next_offset": end, "total_chars": len(chars), "truncated": end < len(chars)}
}
func (s *Server) assistantContentList(c assistantCredential, typ content.Type, query string, drafts bool, page int) (any, error) {
	if typ == content.TypeNote {
		if !c.allows("notes.read") && !c.allows("notes.write") {
			return nil, errors.New("未授权私人笔记")
		}
	} else if drafts && !c.allows("drafts.write") {
		return nil, errors.New("未授权草稿")
	}
	s.contentWriteMu.Lock()
	defer s.contentWriteMu.Unlock()
	items, err := s.repo.List(typ)
	if err != nil {
		return nil, errors.New("内容暂不可读")
	}
	out := []map[string]any{}
	total := 0
	if page < 1 {
		page = 1
	}
	q := strings.ToLower(strings.TrimSpace(query))
	for _, item := range items {
		if typ != content.TypeNote && !item.Publish && !drafts {
			continue
		}
		if typ != content.TypeNote && item.Publish && !c.allows("public.read") {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(item.Title+" "+item.Body), q) {
			continue
		}
		total++
		if total <= (page-1)*20 || len(out) >= 20 {
			continue
		}
		revision, err := content.Revision(item.Path)
		if err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": item.ID, "type": item.Type, "title": item.Title, "slug": item.Slug, "publish": item.Publish, "date": item.Date, "revision": revision})
	}
	return map[string]any{"items": out, "total": total, "page": page}, nil
}
func (s *Server) assistantContentGet(c assistantCredential, typ content.Type, id string, offset, limit int) (any, error) {
	if _, err := decodeAdminContentID(id); err != nil {
		return nil, errors.New("内容标识无效")
	}
	s.contentWriteMu.Lock()
	defer s.contentWriteMu.Unlock()
	item, err := s.repo.GetByID(id, typ)
	if err != nil {
		return nil, errors.New("内容不存在")
	}
	if typ == content.TypeNote {
		if !c.allows("notes.read") && !c.allows("notes.write") {
			return nil, errors.New("未授权私人笔记")
		}
	} else if item.Publish {
		if !c.allows("public.read") {
			return nil, errors.New("未授权公开查询")
		}
	} else if !c.allows("drafts.write") {
		return nil, errors.New("未授权草稿")
	}
	revision, err := content.Revision(item.Path)
	if err != nil {
		return nil, err
	}
	view := inputView(itemInput(item))
	delete(view, "body")
	view["id"] = item.ID
	view["type"] = item.Type
	view["revision"] = revision
	view["text"] = assistantTextChunk(item.Body, offset, limit)
	return view, nil
}
