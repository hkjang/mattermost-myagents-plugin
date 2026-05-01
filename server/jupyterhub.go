package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type userServerStatus struct {
	Ready bool
}

type jupyterHubHTTPError struct {
	StatusCode int
	Body       string
}

func (e *jupyterHubHTTPError) Error() string {
	return fmt.Sprintf("JupyterHub API returned %d: %s", e.StatusCode, e.Body)
}

func (p *Plugin) startUserServer(ctx context.Context, cfg *runtimeConfiguration, channelID, rootID, userID string) error {
	if err := p.startUserServerAndWait(ctx, cfg, userID); err != nil {
		return p.postText(channelID, rootID, "개인 에이전트 서버를 시작할 수 없습니다.")
	}
	return p.postText(channelID, rootID, "개인 에이전트 서버가 켜졌습니다.")
}

func (p *Plugin) stopUserServer(ctx context.Context, cfg *runtimeConfiguration, channelID, rootID, userID string) error {
	if cfg.JupyterHubAPIToken == "" {
		return p.postText(channelID, rootID, "JupyterHub API 토큰이 설정되어 있지 않습니다.")
	}
	if err := p.jupyterHubRequest(ctx, cfg, http.MethodDelete, "users", userID, "server"); err != nil {
		return p.postText(channelID, rootID, "개인 에이전트 서버를 중지할 수 없습니다.")
	}

	deadline := time.Now().Add(cfg.ServerStopTimeout)
	for {
		status, err := p.getUserServerStatus(ctx, cfg, userID)
		if err == nil && !status.Ready {
			return p.postText(channelID, rootID, "개인 에이전트 서버가 꺼졌습니다.")
		}
		if time.Now().After(deadline) {
			return p.postText(channelID, rootID, "서버 중지 대기 시간이 초과되었습니다.")
		}
		time.Sleep(cfg.ServerStatusPollInterval)
	}
}

func (p *Plugin) startUserServerAndWait(ctx context.Context, cfg *runtimeConfiguration, userID string) error {
	if cfg.JupyterHubAPIToken == "" {
		return fmt.Errorf("JupyterHub API token is not configured")
	}
	if err := p.jupyterHubRequest(ctx, cfg, http.MethodPost, "users", userID, "server"); err != nil {
		if !isJupyterHubAlreadyStartingOrRunning(err) {
			return err
		}
	}

	deadline := time.Now().Add(cfg.ServerStartTimeout)
	for {
		ready, progressErr := p.getUserServerProgressReady(ctx, cfg, userID)
		if progressErr == nil && ready {
			return nil
		}
		status, err := p.getUserServerStatus(ctx, cfg, userID)
		if err == nil && status.Ready {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("server start timeout")
		}
		time.Sleep(cfg.ServerStatusPollInterval)
	}
}

func (p *Plugin) getUserServerStatus(ctx context.Context, cfg *runtimeConfiguration, userID string) (userServerStatus, error) {
	if cfg.JupyterHubAPIToken == "" {
		return userServerStatus{}, fmt.Errorf("JupyterHub API token is not configured")
	}
	body, err := p.jupyterHubRequestBody(ctx, cfg, http.MethodGet, "users", userID)
	if err != nil {
		return userServerStatus{}, err
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return userServerStatus{}, err
	}
	return userServerStatus{Ready: jupyterHubUserServerReady(payload)}, nil
}

func jupyterHubUserServerReady(payload map[string]any) bool {
	if server, ok := payload["server"]; ok && server != nil && stringValue(server) != "" {
		return true
	}
	if servers, ok := payload["servers"].(map[string]any); ok {
		if defaultServer, ok := servers[""].(map[string]any); ok {
			if ready, ok := defaultServer["ready"].(bool); ok {
				return ready
			}
			return len(defaultServer) > 0
		}
		for _, value := range servers {
			if server, ok := value.(map[string]any); ok {
				if ready, ok := server["ready"].(bool); ok && ready {
					return true
				}
			}
		}
	}
	return false
}

func (p *Plugin) getUserServerProgressReady(ctx context.Context, cfg *runtimeConfiguration, userID string) (bool, error) {
	body, err := p.jupyterHubRequestBody(ctx, cfg, http.MethodGet, "users", userID, "server", "progress")
	if err != nil {
		return false, err
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return false, nil
	}
	if strings.Contains(trimmed, `"ready": true`) || strings.Contains(trimmed, `"ready":true`) {
		return true, nil
	}
	if strings.Contains(strings.ToLower(trimmed), `"progress": 100`) || strings.Contains(strings.ToLower(trimmed), `"progress":100`) {
		return true, nil
	}
	var items []map[string]any
	if err := json.Unmarshal(body, &items); err == nil {
		for _, item := range items {
			if ready, ok := item["ready"].(bool); ok && ready {
				return true, nil
			}
			if progress, ok := item["progress"].(float64); ok && progress >= 100 {
				return true, nil
			}
		}
	}
	var item map[string]any
	if err := json.Unmarshal(body, &item); err == nil {
		if ready, ok := item["ready"].(bool); ok && ready {
			return true, nil
		}
		if progress, ok := item["progress"].(float64); ok && progress >= 100 {
			return true, nil
		}
	}
	return false, nil
}

func (p *Plugin) jupyterHubRequest(ctx context.Context, cfg *runtimeConfiguration, method string, segments ...string) error {
	_, err := p.jupyterHubRequestBody(ctx, cfg, method, segments...)
	return err
}

func (p *Plugin) jupyterHubRequestBody(ctx context.Context, cfg *runtimeConfiguration, method string, segments ...string) ([]byte, error) {
	endpoint := *cfg.JupyterHubAPIBaseURL
	endpoint.Path = joinURLPath(endpoint.Path, segments...)

	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(requestCtx, method, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "token "+cfg.JupyterHubAPIToken)
	request.Header.Set("Accept", "application/json")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
	if response.StatusCode >= http.StatusBadRequest {
		return body, &jupyterHubHTTPError{
			StatusCode: response.StatusCode,
			Body:       strings.TrimSpace(string(body)),
		}
	}
	return body, nil
}

func isJupyterHubAlreadyStartingOrRunning(err error) bool {
	var httpErr *jupyterHubHTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	if httpErr.StatusCode != http.StatusBadRequest && httpErr.StatusCode != http.StatusConflict {
		return false
	}
	body := strings.ToLower(httpErr.Body)
	return strings.Contains(body, "pending") ||
		strings.Contains(body, "already") ||
		strings.Contains(body, "running") ||
		strings.Contains(body, "spawn")
}

func buildURL(base *url.URL, segments ...string) string {
	if base == nil {
		return ""
	}
	next := *base
	next.Path = joinURLPath(next.Path, segments...)
	return strings.TrimSpace(next.String())
}
