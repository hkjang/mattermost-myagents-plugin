package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

type Plugin struct {
	plugin.MattermostPlugin

	client *pluginapi.Client
	router *mux.Router

	configurationLock sync.RWMutex
	configuration     *configuration

	botLock sync.RWMutex
	bot     botAccount
}

type botAccount struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	UserID      string `json:"user_id"`
	Active      bool   `json:"active"`
	LastError   string `json:"last_error,omitempty"`
	UpdatedAt   int64  `json:"updated_at"`
}

type incomingMessage struct {
	Channel *model.Channel
	User    *model.User
	Prompt  string
	RootID  string
	IsDM    bool
}

func (p *Plugin) OnActivate() error {
	p.client = pluginapi.NewClient(p.API, p.Driver)
	if err := p.OnConfigurationChange(); err != nil {
		return err
	}
	p.router = p.initRouter()
	if err := p.ensureBot(); err != nil {
		p.API.LogError("Failed to ensure myagents bot during activation", "error", err)
	}
	return nil
}

func (p *Plugin) OnDeactivate() error {
	return nil
}

func (p *Plugin) MessageHasBeenPosted(_ *plugin.Context, post *model.Post) {
	if post == nil || post.UserId == "" || p.isBotUserID(post.UserId) {
		return
	}
	if post.GetProp("from_bot") != nil || post.GetProp("from_plugin") != nil || post.GetProp("from_webhook") != nil {
		return
	}
	if post.RemoteId != nil && *post.RemoteId != "" {
		return
	}

	go func() {
		if err := p.handlePostedMessage(post); err != nil {
			p.API.LogError("Failed to process myagents post trigger", "error", err, "post_id", post.Id)
		}
	}()
}

func (p *Plugin) handlePostedMessage(post *model.Post) error {
	cfg, err := p.getRuntimeConfiguration()
	if err != nil {
		return err
	}
	account, err := p.getOrEnsureBot()
	if err != nil {
		return err
	}

	channel, appErr := p.API.GetChannel(post.ChannelId)
	if appErr != nil {
		return fmt.Errorf("failed to get channel: %w", appErr)
	}

	// DM 채널인 경우, myagents 봇이 해당 DM의 멤버인지 확인.
	// 다른 플러그인(예: OCS)이 만든 봇과의 DM은 무시한다.
	if channel.Type == model.ChannelTypeDirect {
		if account.UserID == "" {
			return nil
		}
		if !strings.Contains(channel.Name, account.UserID) {
			return nil
		}
	}

	user, appErr := p.API.GetUser(post.UserId)
	if appErr != nil {
		return fmt.Errorf("failed to get user: %w", appErr)
	}
	if user.IsBot {
		return nil
	}

	prompt, triggered := extractPrompt(cfg.BotUsername, account.UserID, channel, post.Message)
	if !triggered {
		return nil
	}
	message := incomingMessage{
		Channel: channel,
		User:    user,
		Prompt:  strings.TrimSpace(prompt),
		RootID:  responseRootID(post, channel),
		IsDM:    channel.Type == model.ChannelTypeDirect,
	}
	if len(post.FileIds) > 0 {
		return p.postText(channel.Id, message.RootID, "첨부 파일은 아직 지원하지 않습니다.")
	}
	if message.Prompt == "" {
		return p.postText(channel.Id, message.RootID, buildUsageMessage(cfg.BotUsername))
	}
	return p.processMyAgentsMessage(context.Background(), cfg, message)
}

func extractPrompt(botUsername, botUserID string, channel *model.Channel, message string) (string, bool) {
	message = strings.TrimSpace(message)
	if channel == nil || message == "" {
		return "", false
	}

	mentionPatterns := []string{"@" + strings.ToLower(botUsername)}
	if botUserID != "" {
		mentionPatterns = append(mentionPatterns, "@"+strings.ToLower(botUserID))
	}

	lowerMessage := strings.ToLower(message)
	mentionFound := false
	for _, mention := range mentionPatterns {
		if strings.Contains(lowerMessage, mention) {
			mentionFound = true
			break
		}
	}

	switch channel.Type {
	case model.ChannelTypeDirect:
		return stripMentions(message, botUsername, botUserID), true
	case model.ChannelTypeGroup:
		if !mentionFound {
			return "", false
		}
		return stripMentions(message, botUsername, botUserID), true
	default:
		if !mentionFound {
			return "", false
		}
		return stripMentions(message, botUsername, botUserID), true
	}
}

func stripMentions(message, botUsername, botUserID string) string {
	tokens := []string{regexp.QuoteMeta(botUsername)}
	if botUserID != "" {
		tokens = append(tokens, regexp.QuoteMeta(botUserID))
	}
	pattern := regexp.MustCompile(`(?i)@(` + strings.Join(tokens, "|") + `)\b`)
	return strings.TrimSpace(pattern.ReplaceAllString(message, ""))
}

func responseRootID(post *model.Post, channel *model.Channel) string {
	if post == nil || channel == nil {
		return ""
	}
	if post.RootId != "" {
		return post.RootId
	}
	return post.Id
}

func (p *Plugin) processMyAgentsMessage(ctx context.Context, cfg *runtimeConfiguration, msg incomingMessage) error {
	// domainUserID: OpenCode 서브도메인용 (sj.lee → sj-lee)
	domainUserID, err := resolveMappedUserID(msg.User, cfg)
	if err != nil {
		return p.postText(msg.Channel.Id, msg.RootID, "사용자 ID를 도메인으로 변환할 수 없습니다. 관리자에게 문의해주세요.")
	}
	// hubUserID: JupyterHub API용 원본 username (sj.lee 그대로)
	hubUserID := strings.ToLower(strings.TrimSpace(msg.User.Username))

	command := classifyUserCommand(msg.Prompt)
	switch command {
	case "start":
		if !cfg.AllowUserStartServer {
			return p.postText(msg.Channel.Id, msg.RootID, "서버 시작 기능이 비활성화되어 있습니다.")
		}
		return p.startUserServer(ctx, cfg, msg.Channel.Id, msg.RootID, hubUserID)
	case "stop":
		if !cfg.AllowUserStopServer {
			return p.postText(msg.Channel.Id, msg.RootID, "서버 중지 기능이 비활성화되어 있습니다.")
		}
		return p.stopUserServer(ctx, cfg, msg.Channel.Id, msg.RootID, hubUserID)
	case "status":
		status, err := p.getUserServerStatus(ctx, cfg, hubUserID)
		if err != nil {
			return p.postText(msg.Channel.Id, msg.RootID, "개인 에이전트 서버 상태를 확인할 수 없습니다.")
		}
		if status.Ready {
			return p.postText(msg.Channel.Id, msg.RootID, "개인 에이전트 서버가 켜져 있습니다.")
		}
		return p.postText(msg.Channel.Id, msg.RootID, "개인 에이전트 서버가 꺼져 있습니다.")
	case "session_reset":
		return p.handleSessionReset(ctx, cfg, msg, domainUserID)
	case "session_abort":
		return p.handleSessionAbort(ctx, cfg, msg, domainUserID)
	case "session_info":
		return p.handleSessionInfo(msg, domainUserID)
	}

	status, err := p.getUserServerStatus(ctx, cfg, hubUserID)
	if err != nil {
		return p.postText(msg.Channel.Id, msg.RootID, "개인 에이전트 서버 상태를 확인할 수 없습니다.")
	}
	if !status.Ready {
		if !cfg.AutoStartServer {
			return p.postText(msg.Channel.Id, msg.RootID, "서버를 먼저 켜주세요. `켜줘` 또는 `서버 켜줘`라고 보내면 시작할 수 있습니다.")
		}
		if err := p.startUserServerAndWait(ctx, cfg, hubUserID); err != nil {
			return p.postText(msg.Channel.Id, msg.RootID, "개인 에이전트 서버를 시작할 수 없습니다.")
		}
	}

	baseURL := buildUserOpenCodeURL(domainUserID, cfg.BaseDomainSuffix)
	sessionID, err := p.getOrCreateSessionID(ctx, cfg, baseURL, msg, domainUserID)
	if err != nil {
		return p.postText(msg.Channel.Id, msg.RootID, userFacingOpenCodeError(err))
	}
	output, err := p.streamOpenCodeMessage(ctx, cfg, baseURL, sessionID, msg.Channel.Id, msg.RootID, msg.Prompt)
	if err != nil {
		var callErr *openCodeCallError
		if errors.As(err, &callErr) && callErr.StatusCode == http.StatusNotFound {
			if newSessionID, resetErr := p.resetSessionID(ctx, cfg, baseURL, msg, domainUserID); resetErr == nil {
				output, err = p.streamOpenCodeMessage(ctx, cfg, baseURL, newSessionID, msg.Channel.Id, msg.RootID, msg.Prompt)
			}
		}
	}
	if err != nil {
		return p.postText(msg.Channel.Id, msg.RootID, userFacingOpenCodeError(err))
	}
	if strings.TrimSpace(output) == "" {
		output = "응답이 비어 있습니다."
	}
	return nil
}

func classifyUserCommand(prompt string) string {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	normalized = strings.Join(strings.Fields(normalized), " ")
	switch normalized {
	case "켜줘", "서버 켜줘":
		return "start"
	case "꺼줘", "서버 꺼줘":
		return "stop"
	case "상태 알려줘":
		return "status"
	case "새 세션", "세션 초기화", "초기화", "세션 새로", "세션 리셋", "리셋", "reset", "/reset", "new session", "/new":
		return "session_reset"
	case "중단", "취소", "세션 중단", "세션 취소", "abort", "/abort", "cancel", "/cancel", "stop":
		return "session_abort"
	case "세션 정보", "세션 상태", "세션 보기", "세션", "session", "/session":
		return "session_info"
	default:
		return ""
	}
}

func buildUsageMessage(botUsername string) string {
	return strings.Join([]string{
		fmt.Sprintf("`@%s` 뒤에 질문을 입력하거나, 1:1 DM에서는 바로 질문을 보내주세요.", botUsername),
		"",
		"서버 제어: `켜줘`, `서버 켜줘`, `꺼줘`, `서버 꺼줘`, `상태 알려줘`",
		"세션 관리: `세션 정보`, `세션 초기화` (모델 설정 문제 등으로 응답이 안 올 때), `중단` (진행 중 작업 취소)",
	}, "\n")
}

const sessionResetHint = "현재 세션이 망가졌을 수 있습니다. `세션 초기화`라고 보내면 새 세션을 시작할 수 있습니다."

func (p *Plugin) handleSessionReset(ctx context.Context, cfg *runtimeConfiguration, msg incomingMessage, domainUserID string) error {
	baseURL := buildUserOpenCodeURL(domainUserID, cfg.BaseDomainSuffix)
	previous, _ := p.getStoredSessionID(msg, domainUserID)
	newSessionID, err := p.resetSessionID(ctx, cfg, baseURL, msg, domainUserID)
	if err != nil {
		return p.postText(msg.Channel.Id, msg.RootID, "세션 초기화에 실패했습니다: "+userFacingOpenCodeError(err))
	}
	body := fmt.Sprintf("새 세션을 시작했습니다.\n- 새 세션 ID: `%s`", newSessionID)
	if previous != "" && previous != newSessionID {
		body += fmt.Sprintf("\n- 이전 세션 ID: `%s` (정리됨)", previous)
	}
	body += "\n\n다시 질문을 보내주세요."
	return p.postText(msg.Channel.Id, msg.RootID, body)
}

func (p *Plugin) handleSessionAbort(ctx context.Context, cfg *runtimeConfiguration, msg incomingMessage, domainUserID string) error {
	sessionID, err := p.getStoredSessionID(msg, domainUserID)
	if err != nil {
		return p.postText(msg.Channel.Id, msg.RootID, "세션 정보를 불러오지 못했습니다.")
	}
	if sessionID == "" {
		return p.postText(msg.Channel.Id, msg.RootID, "이 대화에는 진행 중인 세션이 없습니다.")
	}
	baseURL := buildUserOpenCodeURL(domainUserID, cfg.BaseDomainSuffix)
	if err := p.abortOpenCodeSession(ctx, cfg, baseURL, sessionID); err != nil {
		return p.postText(msg.Channel.Id, msg.RootID, "세션을 중단하지 못했습니다: "+userFacingOpenCodeError(err))
	}
	return p.postText(msg.Channel.Id, msg.RootID, fmt.Sprintf("세션 `%s`의 진행 중 작업을 중단했습니다.", sessionID))
}

func (p *Plugin) handleSessionInfo(msg incomingMessage, domainUserID string) error {
	sessionID, err := p.getStoredSessionID(msg, domainUserID)
	if err != nil {
		return p.postText(msg.Channel.Id, msg.RootID, "세션 정보를 불러오지 못했습니다.")
	}
	scope := "DM"
	if msg.Channel != nil && !msg.IsDM {
		if msg.RootID != "" {
			scope = fmt.Sprintf("스레드(루트 `%s`)", msg.RootID)
		} else {
			channelName := msg.Channel.Name
			if channelName == "" {
				channelName = msg.Channel.Id
			}
			scope = "채널 `" + channelName + "`"
		}
	}
	if sessionID == "" {
		body := fmt.Sprintf("이 %s에는 아직 세션이 없습니다. 첫 질문을 보내면 자동으로 생성됩니다.", scope)
		return p.postText(msg.Channel.Id, msg.RootID, body)
	}
	body := strings.Join([]string{
		fmt.Sprintf("- 스코프: %s", scope),
		fmt.Sprintf("- 세션 ID: `%s`", sessionID),
		"",
		"명령어: `세션 초기화` (새 세션), `중단` (진행 중 작업 취소)",
	}, "\n")
	return p.postText(msg.Channel.Id, msg.RootID, body)
}

func (p *Plugin) postText(channelID, rootID, message string) error {
	account, err := p.getOrEnsureBot()
	if err != nil {
		return err
	}
	if err := p.ensureBotInChannel(channelID, account.UserID); err != nil {
		return p.postAsPluginFallback(channelID, rootID, "봇을 채널에 초대하거나 권한을 확인해야 합니다.")
	}
	_, appErr := p.API.CreatePost(&model.Post{
		UserId:    account.UserID,
		ChannelId: channelID,
		RootId:    rootID,
		Type:      "custom_myagents_bot",
		Message:   strings.TrimSpace(message),
		Props: map[string]any{
			"from_bot":     "true",
			"myagents_bot": "true",
		},
	})
	if appErr != nil {
		return fmt.Errorf("failed to create myagents post: %w", appErr)
	}
	return nil
}

func (p *Plugin) postLongText(channelID, rootID, message string) error {
	parts := splitMessage(message, defaultMaxMattermostMessageLength)
	for _, part := range parts {
		if err := p.postText(channelID, rootID, part); err != nil {
			return err
		}
	}
	return nil
}

func (p *Plugin) postAsPluginFallback(channelID, rootID, message string) error {
	_, appErr := p.API.CreatePost(&model.Post{
		ChannelId: channelID,
		RootId:    rootID,
		Message:   strings.TrimSpace(message),
		Props: map[string]any{
			"from_plugin":  "true",
			"myagents_bot": "true",
		},
	})
	if appErr != nil {
		return fmt.Errorf("failed to create fallback post: %w", appErr)
	}
	return nil
}
