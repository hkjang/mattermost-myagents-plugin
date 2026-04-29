package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"time"
)

const (
	defaultBotUsername                 = "myagents"
	defaultBaseDomainSuffix            = "kubagents.koreacb.com"
	defaultUserIDMappingMode           = "username"
	defaultRequestTimeoutSec           = 60
	defaultAsyncThresholdSec           = 30
	defaultJupyterHubBaseURL           = "https://jupyterhub.kubagents-ofc.koreacb.com"
	defaultServerStartTimeoutSec       = 180
	defaultServerStopTimeoutSec        = 120
	defaultServerStatusPollIntervalSec = 3
	defaultMaxMattermostMessageLength  = 3900
	defaultBotDisplayName              = "myagents"
	sessionKeyPrefix                   = "session_"
)

type configuration struct {
	Config                      string `json:"Config"`
	BotUsername                 string `json:"BotUsername"`
	BaseDomainSuffix            string `json:"BaseDomainSuffix"`
	UserIDMappingMode           string `json:"UserIDMappingMode"`
	RequestTimeoutSec           string `json:"RequestTimeoutSec"`
	EnableAsync                 bool   `json:"EnableAsync"`
	AsyncThresholdSec           string `json:"AsyncThresholdSec"`
	JupyterHubBaseURL           string `json:"JupyterHubBaseURL"`
	JupyterHubAPIToken          string `json:"JupyterHubAPIToken"`
	AutoStartServer             bool   `json:"AutoStartServer"`
	ServerStartTimeoutSec       string `json:"ServerStartTimeoutSec"`
	ServerStopTimeoutSec        string `json:"ServerStopTimeoutSec"`
	ServerStatusPollIntervalSec string `json:"ServerStatusPollIntervalSec"`
	AllowUserStopServer         bool   `json:"AllowUserStopServer"`
	AllowUserStartServer        bool   `json:"AllowUserStartServer"`
	EnableDebugLogs             bool   `json:"EnableDebugLogs"`
}

type storedPluginConfig struct {
	BotUsername                 string `json:"bot_username"`
	BaseDomainSuffix            string `json:"base_domain_suffix"`
	UserIDMappingMode           string `json:"user_id_mapping_mode"`
	RequestTimeoutSec           int    `json:"request_timeout_sec"`
	EnableAsync                 bool   `json:"enable_async"`
	AsyncThresholdSec           int    `json:"async_threshold_sec"`
	JupyterHubBaseURL           string `json:"jupyterhub_base_url"`
	JupyterHubAPIToken          string `json:"jupyterhub_api_token"`
	AutoStartServer             bool   `json:"auto_start_server"`
	ServerStartTimeoutSec       int    `json:"server_start_timeout_sec"`
	ServerStopTimeoutSec        int    `json:"server_stop_timeout_sec"`
	ServerStatusPollIntervalSec int    `json:"server_status_poll_interval_sec"`
	AllowUserStopServer         bool   `json:"allow_user_stop_server"`
	AllowUserStartServer        bool   `json:"allow_user_start_server"`
	EnableDebugLogs             bool   `json:"enable_debug_logs"`
}

type runtimeConfiguration struct {
	BotUsername              string
	BotDisplayName           string
	BaseDomainSuffix         string
	UserIDMappingMode        string
	RequestTimeout           time.Duration
	EnableAsync              bool
	AsyncThreshold           time.Duration
	JupyterHubBaseURL        string
	JupyterHubAPIBaseURL     *url.URL
	JupyterHubAPIToken       string
	AutoStartServer          bool
	ServerStartTimeout       time.Duration
	ServerStopTimeout        time.Duration
	ServerStatusPollInterval time.Duration
	AllowUserStopServer      bool
	AllowUserStartServer     bool
	EnableDebugLogs          bool
}

func (c *configuration) Clone() *configuration {
	clone := *c
	return &clone
}

func (c *configuration) normalize() (*runtimeConfiguration, error) {
	stored, _, err := c.getStoredPluginConfig()
	if err != nil {
		return nil, err
	}
	return stored.normalize()
}

func (c *configuration) getStoredPluginConfig() (storedPluginConfig, string, error) {
	if strings.TrimSpace(c.Config) != "" {
		cfg := defaultStoredPluginConfig()
		if err := json.Unmarshal([]byte(c.Config), &cfg); err != nil {
			return storedPluginConfig{}, "config", fmt.Errorf("invalid Config JSON: %w", err)
		}
		return cfg, "config", nil
	}

	cfg := defaultStoredPluginConfig()
	if strings.TrimSpace(c.BotUsername) != "" {
		cfg.BotUsername = c.BotUsername
	}
	if strings.TrimSpace(c.BaseDomainSuffix) != "" {
		cfg.BaseDomainSuffix = c.BaseDomainSuffix
	}
	if strings.TrimSpace(c.UserIDMappingMode) != "" {
		cfg.UserIDMappingMode = c.UserIDMappingMode
	}
	cfg.RequestTimeoutSec = parsePositiveInt(c.RequestTimeoutSec, cfg.RequestTimeoutSec)
	cfg.EnableAsync = c.EnableAsync
	cfg.AsyncThresholdSec = parsePositiveInt(c.AsyncThresholdSec, cfg.AsyncThresholdSec)
	if strings.TrimSpace(c.JupyterHubBaseURL) != "" {
		cfg.JupyterHubBaseURL = c.JupyterHubBaseURL
	}
	cfg.JupyterHubAPIToken = strings.TrimSpace(c.JupyterHubAPIToken)
	cfg.AutoStartServer = c.AutoStartServer
	cfg.ServerStartTimeoutSec = parsePositiveInt(c.ServerStartTimeoutSec, cfg.ServerStartTimeoutSec)
	cfg.ServerStopTimeoutSec = parsePositiveInt(c.ServerStopTimeoutSec, cfg.ServerStopTimeoutSec)
	cfg.ServerStatusPollIntervalSec = parsePositiveInt(c.ServerStatusPollIntervalSec, cfg.ServerStatusPollIntervalSec)
	cfg.AllowUserStopServer = c.AllowUserStopServer
	cfg.AllowUserStartServer = c.AllowUserStartServer
	cfg.EnableDebugLogs = c.EnableDebugLogs
	return cfg, "legacy", nil
}

func defaultStoredPluginConfig() storedPluginConfig {
	return storedPluginConfig{
		BotUsername:                 defaultBotUsername,
		BaseDomainSuffix:            defaultBaseDomainSuffix,
		UserIDMappingMode:           defaultUserIDMappingMode,
		RequestTimeoutSec:           defaultRequestTimeoutSec,
		EnableAsync:                 true,
		AsyncThresholdSec:           defaultAsyncThresholdSec,
		JupyterHubBaseURL:           defaultJupyterHubBaseURL,
		ServerStartTimeoutSec:       defaultServerStartTimeoutSec,
		ServerStopTimeoutSec:        defaultServerStopTimeoutSec,
		ServerStatusPollIntervalSec: defaultServerStatusPollIntervalSec,
		AllowUserStopServer:         true,
		AllowUserStartServer:        true,
	}
}

func (c storedPluginConfig) normalize() (*runtimeConfiguration, error) {
	botUsername := sanitizeBotUsername(c.BotUsername)
	if botUsername == "" {
		botUsername = defaultBotUsername
	}

	suffix := strings.ToLower(strings.Trim(strings.TrimSpace(c.BaseDomainSuffix), "."))
	if suffix == "" {
		suffix = defaultBaseDomainSuffix
	}

	hubBase := strings.TrimRight(strings.TrimSpace(c.JupyterHubBaseURL), "/")
	if hubBase == "" {
		hubBase = defaultJupyterHubBaseURL
	}
	apiBase, err := buildJupyterHubAPIBaseURL(hubBase)
	if err != nil {
		return nil, err
	}

	pollIntervalSec := positiveOrDefault(c.ServerStatusPollIntervalSec, defaultServerStatusPollIntervalSec)
	if pollIntervalSec <= 0 {
		pollIntervalSec = defaultServerStatusPollIntervalSec
	}

	return &runtimeConfiguration{
		BotUsername:              botUsername,
		BotDisplayName:           defaultBotDisplayName,
		BaseDomainSuffix:         suffix,
		UserIDMappingMode:        normalizeMappingMode(c.UserIDMappingMode),
		RequestTimeout:           time.Duration(positiveOrDefault(c.RequestTimeoutSec, defaultRequestTimeoutSec)) * time.Second,
		EnableAsync:              c.EnableAsync,
		AsyncThreshold:           time.Duration(positiveOrDefault(c.AsyncThresholdSec, defaultAsyncThresholdSec)) * time.Second,
		JupyterHubBaseURL:        hubBase,
		JupyterHubAPIBaseURL:     apiBase,
		JupyterHubAPIToken:       strings.TrimSpace(c.JupyterHubAPIToken),
		AutoStartServer:          c.AutoStartServer,
		ServerStartTimeout:       time.Duration(positiveOrDefault(c.ServerStartTimeoutSec, defaultServerStartTimeoutSec)) * time.Second,
		ServerStopTimeout:        time.Duration(positiveOrDefault(c.ServerStopTimeoutSec, defaultServerStopTimeoutSec)) * time.Second,
		ServerStatusPollInterval: time.Duration(pollIntervalSec) * time.Second,
		AllowUserStopServer:      c.AllowUserStopServer,
		AllowUserStartServer:     c.AllowUserStartServer,
		EnableDebugLogs:          c.EnableDebugLogs,
	}, nil
}

func buildJupyterHubAPIBaseURL(base string) (*url.URL, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("invalid JupyterHub base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("JupyterHub base URL must include scheme and host")
	}
	segments := splitPathSegments(parsed.Path)
	if len(segments) < 2 || segments[len(segments)-2] != "hub" || segments[len(segments)-1] != "api" {
		segments = append(segments, "hub", "api")
	}
	parsed.Path = "/" + strings.Join(segments, "/")
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func normalizeMappingMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "username":
		return defaultUserIDMappingMode
	default:
		return defaultUserIDMappingMode
	}
}

func sanitizeBotUsername(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "@")
	var builder strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func parsePositiveInt(raw string, fallback int) int {
	var value int
	if _, err := fmt.Sscanf(strings.TrimSpace(raw), "%d", &value); err != nil || value <= 0 {
		return fallback
	}
	return value
}

func positiveOrDefault(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func (p *Plugin) getConfiguration() *configuration {
	p.configurationLock.RLock()
	defer p.configurationLock.RUnlock()
	if p.configuration == nil {
		return &configuration{}
	}
	return p.configuration
}

func (p *Plugin) getRuntimeConfiguration() (*runtimeConfiguration, error) {
	return p.getConfiguration().normalize()
}

func (p *Plugin) setConfiguration(configuration *configuration) {
	p.configurationLock.Lock()
	defer p.configurationLock.Unlock()
	if configuration != nil && p.configuration == configuration {
		if reflect.ValueOf(*configuration).NumField() == 0 {
			return
		}
		panic("setConfiguration called with the existing configuration")
	}
	p.configuration = configuration
}

func (p *Plugin) OnConfigurationChange() error {
	configuration := new(configuration)
	if err := p.API.LoadPluginConfiguration(configuration); err != nil {
		return fmt.Errorf("failed to load plugin configuration: %w", err)
	}
	p.setConfiguration(configuration)
	if p.client != nil {
		if err := p.ensureBot(); err != nil {
			p.API.LogError("Failed to ensure myagents bot after configuration change", "error", err)
		}
	}
	return nil
}

func splitPathSegments(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, "/")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			segments = append(segments, part)
		}
	}
	return segments
}
