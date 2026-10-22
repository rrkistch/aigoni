package server

import (
	"net/http"
	"strings"

	"aigoni/internal/content"
)

func (s *Server) registerAdminAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST "+adminAPIBasePath+"/session", s.adminSession)
	mux.HandleFunc("GET "+adminAPIBasePath+"/session", s.adminSession)
	mux.Handle("DELETE "+adminAPIBasePath+"/session", s.requireAdminAPIAuth(http.HandlerFunc(s.adminSession)))

	auth := func(pattern string, handler http.HandlerFunc) {
		mux.Handle(pattern, s.requireAdminAPIAuth(handler))
	}
	auth("GET "+adminAPIBasePath+"/integrations", s.integrationsAPI)
	auth("POST "+adminAPIBasePath+"/integrations", s.integrationsAPI)
	auth("DELETE "+adminAPIBasePath+"/integrations/{id}", s.integrationsAPI)
	auth("POST "+adminAPIBasePath+"/integrations/test", s.integrationTestAPI)
	auth("GET "+adminAPIBasePath+"/approvals", s.approvalsAPI)
	auth("GET "+adminAPIBasePath+"/approvals/{id}", s.approvalsAPI)
	auth("POST "+adminAPIBasePath+"/approvals/{id}/approve", s.approvalsAPI)
	auth("POST "+adminAPIBasePath+"/approvals/{id}/reject", s.approvalsAPI)
	auth("GET "+adminAPIBasePath+"/audit", s.auditAPI)
	auth("GET "+adminAPIBasePath+"/dashboard", s.adminAPIDashboard)
	auth("GET "+adminAPIBasePath+"/theme", s.themeAPI)
	auth("POST "+adminAPIBasePath+"/theme/uploads", s.themeUploadAPI)
	auth("GET "+adminAPIBasePath+"/theme/uploads/{id}", s.themeUploadAPI)
	auth("PUT "+adminAPIBasePath+"/theme/uploads/{id}", s.themeUploadAPI)
	auth("DELETE "+adminAPIBasePath+"/theme/uploads/{id}", s.themeUploadAPI)
	auth("POST "+adminAPIBasePath+"/theme/uploads/{id}/complete", s.themeVerifyAPI)
	auth("POST "+adminAPIBasePath+"/theme/activate", s.themeActivateAPI)
	auth("POST "+adminAPIBasePath+"/theme/rollback", s.themeActivateAPI)
	auth("POST "+adminAPIBasePath+"/theme/cancel", s.themeCancelAPI)
	auth("GET "+adminAPIBasePath+"/health", s.siteHealthAPI)
	auth("GET "+adminAPIBasePath+"/backups", s.backupsAPI)
	auth("POST "+adminAPIBasePath+"/backups", s.backupsAPI)
	auth("GET "+adminAPIBasePath+"/backups/items/{id}", s.backupItemAPI)
	auth("DELETE "+adminAPIBasePath+"/backups/items/{id}", s.backupItemAPI)
	auth("GET "+adminAPIBasePath+"/backups/items/{id}/download", s.backupDownload)
	auth("POST "+adminAPIBasePath+"/backups/items/{id}/restore", s.backupRestoreAPI)
	auth("POST "+adminAPIBasePath+"/backups/items/{id}/validate", s.backupValidateAPI)
	auth("POST "+adminAPIBasePath+"/backups/recover", s.backupRecoverAPI)
	auth("POST "+adminAPIBasePath+"/backups/uploads", s.backupUploadAPI)
	auth("GET "+adminAPIBasePath+"/backups/uploads/{id}", s.backupUploadAPI)
	auth("PUT "+adminAPIBasePath+"/backups/uploads/{id}", s.backupUploadAPI)
	auth("POST "+adminAPIBasePath+"/backups/uploads/{id}/complete", s.backupValidateAPI)
	auth("GET "+adminAPIBasePath+"/onboarding", s.onboardingAPI)
	auth("PATCH "+adminAPIBasePath+"/onboarding", s.onboardingAPI)
	auth("POST "+adminAPIBasePath+"/onboarding/samples", s.onboardingSamplesAPI)
	auth("POST "+adminAPIBasePath+"/previews", s.previewsAPI)
	auth("GET "+adminAPIBasePath+"/previews/{id}", s.previewsAPI)
	auth("GET "+adminAPIBasePath+"/media", s.mediaAPI)
	auth("DELETE "+adminAPIBasePath+"/media/{id}", s.mediaAPI)
	auth("POST "+adminAPIBasePath+"/media/uploads", s.mediaUploadAPI)
	auth("GET "+adminAPIBasePath+"/media/uploads/{id}", s.mediaUploadAPI)
	auth("PUT "+adminAPIBasePath+"/media/uploads/{id}", s.mediaUploadAPI)
	auth("DELETE "+adminAPIBasePath+"/media/uploads/{id}", s.mediaUploadAPI)
	auth("POST "+adminAPIBasePath+"/media/uploads/{id}/complete", s.mediaUploadAPI)
	auth("GET "+adminAPIBasePath+"/theme-settings", s.themeSettingsAPI)
	auth("GET "+adminAPIBasePath+"/settings-history", s.settingsHistoryAPI)
	auth("POST "+adminAPIBasePath+"/settings-history/{revision}/restore", s.settingsHistoryAPI)
	auth("PATCH "+adminAPIBasePath+"/theme-settings", s.themeSettingsAPI)
	auth("GET "+adminAPIBasePath+"/ai/providers", s.aiProvidersAPI)
	auth("PATCH "+adminAPIBasePath+"/ai/providers", s.aiProvidersAPI)
	auth("POST "+adminAPIBasePath+"/ai/providers/{id}/models", s.aiModelsAPI)
	auth("POST "+adminAPIBasePath+"/ai/providers/{id}/test", s.aiTestAPI)
	auth("GET "+adminAPIBasePath+"/site-build", s.siteBuildAPI)
	auth("POST "+adminAPIBasePath+"/site-build", s.siteBuildAPI)
	auth("GET "+adminAPIBasePath+"/settings", s.settingsAPI)
	auth("PATCH "+adminAPIBasePath+"/settings", s.settingsAPI)
	auth("PUT "+adminAPIBasePath+"/settings/assets/{kind}", s.settingsAssetsAPI)
	auth("DELETE "+adminAPIBasePath+"/settings/assets/{kind}", s.settingsAssetsAPI)
	registerAdminContentRoutes(s, auth)
	s.registerAdminWikiAPIRoutes(mux)
	s.registerAdminAssetsAPIRoutes(mux)
	mux.HandleFunc("GET "+adminAPIBasePath+"/", s.adminAPINotFound)
}

func registerAdminContentRoutes(s *Server, auth func(string, http.HandlerFunc)) {
	for _, route := range []struct {
		name string
		typ  content.Type
	}{
		{name: "posts", typ: content.TypePost},
		{name: "dynamics", typ: content.TypeDynamic},
		{name: "projects", typ: content.TypeProject},
		{name: "pages", typ: content.TypePage},
		{name: "notes", typ: content.TypeNote},
	} {
		route := route
		base := adminAPIBasePath + "/" + route.name
		auth("GET "+base, func(w http.ResponseWriter, r *http.Request) {
			s.adminAPIContentList(w, r, route.typ)
		})
		auth("POST "+base, s.adminAPIContent)
		auth("GET "+base+"/", func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(strings.TrimPrefix(r.URL.Path, base+"/"), "/assets") {
				s.adminResourcesAPI(w, r)
				return
			}
			s.adminAPIContentDetail(w, r, route.typ, base+"/")
		})
		auth("POST "+base+"/", s.adminResourcesAPI)
		auth("PUT "+base+"/", s.adminResourcesAPI)
		auth("PATCH "+base+"/", s.adminAPIContent)
		auth("DELETE "+base+"/", func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(strings.TrimPrefix(r.URL.Path, base+"/"), "/assets") {
				s.adminResourcesAPI(w, r)
				return
			}
			s.adminAPIContent(w, r)
		})
	}
	auth("GET "+adminAPIBasePath+"/categories", s.adminAPICategories)
	auth("GET "+adminAPIBasePath+"/note-categories", s.adminAPINoteCategories)
	auth("GET "+adminAPIBasePath+"/tags", s.adminAPITags)
	auth("GET "+adminAPIBasePath+"/note-tags", s.adminAPINoteTags)
	auth("GET "+adminAPIBasePath+"/search", s.adminAPISearch)
}
