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
	userID, err := resolveMappedUserID(msg.User, cfg)
	if err != nil {
		return p.postText(msg.Channel.Id, msg.RootID, "사용자 ID를 도메인으로 변환할 수 없습니다. 관리자에게 문의해주세요.")
	}

	command := classifyUserCommand(msg.Prompt)
	switch command {
	case "start":
		if !cfg.AllowUserStartServer {
			return p.postText(msg.Channel.Id, msg.RootID, "서버 시작 기능이 비활성화되어 있습니다.")
		}
		return p.startUserServer(ctx, cfg, msg.Channel.Id, msg.RootID, userID)
	case "stop":
		if !cfg.AllowUserStopServer {
			return p.postText(msg.Channel.Id, msg.RootID, "서버 중지 기능이 비활성화되어 있습니다.")
		}
		return p.stopUserServer(ctx, cfg, msg.Channel.Id, msg.RootID, userID)
	case "status":
		status, err := p.getUserServerStatus(ctx, cfg, userID)
		if err != nil {
			return p.postText(msg.Channel.Id, msg.RootID, "개인 에이전트 서버 상태를 확인할 수 없습니다.")
		}
		if status.Ready {
			return p.postText(msg.Channel.Id, msg.RootID, "개인 에이전트 서버가 켜져 있습니다.")
		}
		return p.postText(msg.Channel.Id, msg.RootID, "개인 에이전트 서버가 꺼져 있습니다.")
	}

	status, err := p.getUserServerStatus(ctx, cfg, userID)
	if err != nil {
		return p.postText(msg.Channel.Id, msg.RootID, "개인 에이전트 서버 상태를 확인할 수 없습니다.")
	}
	if !status.Ready {
		if !cfg.AutoStartServer {
			return p.postText(msg.Channel.Id, msg.RootID, "서버를 먼저 켜주세요. `켜줘` 또는 `서버 켜줘`라고 보내면 시작할 수 있습니다.")
		}
		if err := p.startUserServerAndWait(ctx, cfg, userID); err != nil {
			return p.postText(msg.Channel.Id, msg.RootID, "개인 에이전트 서버를 시작할 수 없습니다.")
		}
	}

	baseURL := buildUserOpenCodeURL(userID, cfg.BaseDomainSuffix)
	sessionID, err := p.getOrCreateSessionID(ctx, cfg, baseURL, msg, userID)
	if err != nil {
		return p.postText(msg.Channel.Id, msg.RootID, userFacingOpenCodeError(err))
	}
	output, err := p.streamOpenCodeMessage(ctx, cfg, baseURL, sessionID, msg.Channel.Id, msg.RootID, msg.Prompt)
	if err != nil {
		var callErr *openCodeCallError
		if errors.As(err, &callErr) && callErr.StatusCode == http.StatusNotFound {
			if newSessionID, resetErr := p.resetSessionID(ctx, cfg, baseURL, msg, userID); resetErr == nil {
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
	default:
		return ""
	}
}

func buildUsageMessage(botUsername string) string {
	return strings.Join([]string{
		fmt.Sprintf("`@%s` 뒤에 질문을 입력하거나, 1:1 DM에서는 바로 질문을 보내주세요.", botUsername),
		"",
		"서버 제어: `켜줘`, `서버 켜줘`, `꺼줘`, `서버 꺼줘`, `상태 알려줘`",
	}, "\n")
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
