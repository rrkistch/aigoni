package server

import (
	"aigoni/internal/configdoc"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func (s *Server) settingsHistoryAPI(w http.ResponseWriter, r *http.Request) {
	dir := filepath.Join(s.root, ".aigoni-admin", "theme-history")
	if r.Method == "GET" {
		s.themeMu.Lock()
		defer s.themeMu.Unlock()
		files, _ := os.ReadDir(dir)
		items := []map[string]any{}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, f.Name()))
			var d configdoc.Document[map[string]any]
			if err != nil || json.Unmarshal(b, &d) != nil {
				continue
			}
			info, e := f.Info()
			if e != nil {
				continue
			}
			groups := []string{}
			for group := range d.Value {
				groups = append(groups, group)
			}
			sort.Strings(groups)
			items = append(items, map[string]any{"revision": d.Revision, "groups": groups, "saved_at": info.ModTime().UTC()})
		}
		writeAdminJSON(w, 200, map[string]any{"items": items})
		return
	}
	target, err := strconv.ParseUint(r.PathValue("revision"), 10, 64)
	if err != nil {
		writeAdminError(w, 400, "invalid_revision", "历史版本号无效。")
		return
	}
	var req struct {
		Revision uint64 `json:"revision"`
	}
	if !decodeAdminWikiJSON(w, r, &req, false) {
		return
	}
	b, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("%020d.json", target)))
	var prior configdoc.Document[map[string]any]
	if err != nil || json.Unmarshal(b, &prior) != nil || prior.Version != 1 {
		writeAdminError(w, 404, "history_not_found", "历史版本已不存在。")
		return
	}
	groups := map[string]any{}
	for name := range adminSchema() {
		groups[name] = nil
	}
	for name, v := range prior.Value {
		groups[name] = v
	}
	d, err := s.changeThemeSettings(req.Revision, groups)
	if errors.Is(err, configdoc.ErrConflict) {
		writeAdminError(w, 409, "revision_conflict", "设置已变化，请重新加载再恢复。")
		return
	}
	if err != nil {
		writeAdminError(w, 400, "restore_failed", "历史配置与当前版本不兼容或无法写入。")
		return
	}
	status := "queued"
	if err = s.queueSiteBuild(false); err != nil {
		status = "failed"
	}
	if s.siteBuild == nil {
		status = "disabled"
	}
	writeAdminJSON(w, 200, map[string]any{"revision": d.Revision, "saved": true, "build_status": status})
}
