package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

type pluginStatusResponse struct {
	PluginID          string     `json:"plugin_id"`
	Bot               botAccount `json:"bot"`
	BotUsername       string     `json:"bot_username"`
	BaseDomainSuffix  string     `json:"base_domain_suffix"`
	JupyterHubBaseURL string     `json:"jupyterhub_base_url"`
	ConfigError       string     `json:"config_error,omitempty"`
}

type adminConfigResponse struct {
	Config storedPluginConfig `json:"config"`
	Source string             `json:"source"`
}

func (p *Plugin) initRouter() *mux.Router {
	router := mux.NewRouter()
	router.Use(p.MattermostAuthorizationRequired)

	apiRouter := router.PathPrefix("/api/v1").Subrouter()
	apiRouter.HandleFunc("/config", p.handleAdminConfig).Methods(http.MethodGet)
	apiRouter.HandleFunc("/status", p.handleStatus).Methods(http.MethodGet)
	apiRouter.HandleFunc("/test", p.handleTestConnection).Methods(http.MethodPost)
	return router
}

func (p *Plugin) ServeHTTP(_ *plugin.Context, w http.ResponseWriter, r *http.Request) {
	p.router.ServeHTTP(w, r)
}

func (p *Plugin) MattermostAuthorizationRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Mattermost-User-ID") == "" {
			http.Error(w, "Not authorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (p *Plugin) handleStatus(w http.ResponseWriter, _ *http.Request) {
	status := pluginStatusResponse{
		PluginID: manifest.Id,
		Bot:      p.getBot(),
	}
	cfg, err := p.getRuntimeConfiguration()
	if err != nil {
		status.ConfigError = err.Error()
		writeJSON(w, http.StatusOK, status)
		return
	}
	status.BotUsername = cfg.BotUsername
	status.BaseDomainSuffix = cfg.BaseDomainSuffix
	status.JupyterHubBaseURL = cfg.JupyterHubBaseURL
	writeJSON(w, http.StatusOK, status)
}

func (p *Plugin) handleAdminConfig(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("Mattermost-User-ID")
	if !p.client.User.HasPermissionTo(userID, model.PermissionManageSystem) {
		writeError(w, http.StatusForbidden, errors.New("only system administrators can access plugin configuration"))
		return
	}

	stored, source, err := p.getConfiguration().getStoredPluginConfig()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, adminConfigResponse{Config: stored, Source: source})
}

func (p *Plugin) handleTestConnection(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("Mattermost-User-ID")
	if !p.client.User.HasPermissionTo(userID, model.PermissionManageSystem) {
		writeError(w, http.StatusForbidden, errors.New("only system administrators can test JupyterHub connectivity"))
		return
	}
	cfg, err := p.getRuntimeConfiguration()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if cfg.JupyterHubAPIToken == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": "JupyterHub API token is not configured"})
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, buildURL(cfg.JupyterHubAPIBaseURL), nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	request.Header.Set("Authorization", "token "+cfg.JupyterHubAPIToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	defer response.Body.Close()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          response.StatusCode < http.StatusBadRequest,
		"status_code": response.StatusCode,
		"url":         cfg.JupyterHubAPIBaseURL.String(),
	})
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, statusCode int, err error) {
	writeJSON(w, statusCode, map[string]string{"error": err.Error()})
}
