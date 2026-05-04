package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

func (p *Plugin) ensureBot() error {
	cfg, err := p.getRuntimeConfiguration()
	if err != nil {
		p.setBot(botAccount{LastError: err.Error(), UpdatedAt: time.Now().UnixMilli()})
		return err
	}
	description := "Mattermost users talk to their personal opencode agent through myagents."
	displayName := cfg.BotDisplayName

	existingUser, appErr := p.API.GetUserByUsername(cfg.BotUsername)
	if appErr == nil && existingUser != nil {
		if !existingUser.IsBot {
			err := fmt.Errorf("username @%s is already used by a regular Mattermost account", cfg.BotUsername)
			p.setBot(botAccount{Username: cfg.BotUsername, DisplayName: displayName, LastError: err.Error(), UpdatedAt: time.Now().UnixMilli()})
			return err
		}
		if _, err := p.client.Bot.Get(existingUser.Id, true); err == nil {
			if _, patchErr := p.client.Bot.Patch(existingUser.Id, &model.BotPatch{
				DisplayName: &displayName,
				Description: &description,
			}); patchErr != nil && !isBotNotFoundError(patchErr) {
				return patchErr
			}
			if _, activeErr := p.client.Bot.UpdateActive(existingUser.Id, true); activeErr != nil && !isBotNotFoundError(activeErr) {
				return activeErr
			}
		}
		p.setBot(botAccount{Username: cfg.BotUsername, DisplayName: displayName, UserID: existingUser.Id, Active: true, UpdatedAt: time.Now().UnixMilli()})
		return nil
	}
	if appErr != nil && appErr.StatusCode != http.StatusNotFound {
		return fmt.Errorf("failed to look up bot user @%s: %w", cfg.BotUsername, appErr)
	}

	newBot := &model.Bot{
		Username:    cfg.BotUsername,
		DisplayName: displayName,
		Description: description,
	}
	if err := p.client.Bot.Create(newBot); err != nil {
		if recovered, recoverErr := p.API.GetUserByUsername(cfg.BotUsername); recoverErr == nil && recovered != nil && recovered.IsBot {
			p.setBot(botAccount{Username: cfg.BotUsername, DisplayName: displayName, UserID: recovered.Id, Active: true, UpdatedAt: time.Now().UnixMilli()})
			return nil
		}
		p.setBot(botAccount{Username: cfg.BotUsername, DisplayName: displayName, LastError: err.Error(), UpdatedAt: time.Now().UnixMilli()})
		return fmt.Errorf("failed to create bot @%s: %w", cfg.BotUsername, err)
	}
	p.setBot(botAccount{Username: cfg.BotUsername, DisplayName: displayName, UserID: newBot.UserId, Active: true, UpdatedAt: time.Now().UnixMilli()})
	return nil
}

func (p *Plugin) setBot(account botAccount) {
	p.botLock.Lock()
	defer p.botLock.Unlock()
	p.bot = account
}

func (p *Plugin) getBot() botAccount {
	p.botLock.RLock()
	defer p.botLock.RUnlock()
	return p.bot
}

func (p *Plugin) getOrEnsureBot() (botAccount, error) {
	account := p.getBot()
	if account.UserID != "" {
		return account, nil
	}
	if err := p.ensureBot(); err != nil {
		return botAccount{}, err
	}
	account = p.getBot()
	if account.UserID == "" {
		return botAccount{}, fmt.Errorf("myagents bot is not available")
	}
	return account, nil
}

func (p *Plugin) isBotUserID(userID string) bool {
	return userID != "" && userID == p.getBot().UserID
}

func (p *Plugin) ensureBotInChannel(channelID, botUserID string) error {
	if channelID == "" || botUserID == "" {
		return nil
	}
	if _, appErr := p.API.GetChannelMember(channelID, botUserID); appErr == nil {
		return nil
	}
	if _, appErr := p.API.AddUserToChannel(channelID, botUserID, ""); appErr != nil {
		return fmt.Errorf("failed to add bot to channel: %w", appErr)
	}
	return nil
}

func resolveMappedUserID(user *model.User, cfg *runtimeConfiguration) (string, error) {
	if user == nil {
		return "", fmt.Errorf("missing user")
	}
	source := user.Username
	mapped := strings.ToLower(strings.TrimSpace(source))
	mapped = strings.ReplaceAll(mapped, ".", "-")
	mapped = strings.ReplaceAll(mapped, "_", "-")
	mapped = collapseHyphens(mapped)
	mapped = strings.Trim(mapped, "-")
	if !validDomainUserID(mapped) {
		return "", fmt.Errorf("invalid mapped user id %q", mapped)
	}
	return mapped, nil
}

func validDomainUserID(value string) bool {
	if value == "" || len(value) > 63 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return false
	}
	return !strings.HasPrefix(value, "-") && !strings.HasSuffix(value, "-")
}

func collapseHyphens(value string) string {
	for strings.Contains(value, "--") {
		value = strings.ReplaceAll(value, "--", "-")
	}
	return value
}

func buildUserOpenCodeURL(userID, suffix string) string {
	return fmt.Sprintf("https://%s.%s", userID, strings.Trim(suffix, "."))
}

func (p *Plugin) getOrCreateSessionID(ctx context.Context, cfg *runtimeConfiguration, baseURL string, msg incomingMessage, mappedUserID string) (string, error) {
	key := buildSessionKey(msg, mappedUserID)
	stored, appErr := p.API.KVGet(sessionKeyPrefix + key)
	if appErr != nil {
		return "", fmt.Errorf("failed to load session: %w", appErr)
	}
	if len(stored) > 0 {
		return string(stored), nil
	}
	sessionID, err := p.createOpenCodeSession(ctx, cfg, baseURL, buildSessionTitle(msg, mappedUserID))
	if err != nil {
		return "", err
	}
	if appErr := p.API.KVSet(sessionKeyPrefix+key, []byte(sessionID)); appErr != nil {
		return "", fmt.Errorf("failed to save session: %w", appErr)
	}
	return sessionID, nil
}

func buildSessionKey(msg incomingMessage, mappedUserID string) string {
	if msg.IsDM {
		return strings.Join([]string{mappedUserID, msg.Channel.Id}, ":")
	}
	root := msg.RootID
	if root == "" {
		root = msg.Channel.Id
	}
	return strings.Join([]string{mappedUserID, msg.Channel.Id, root}, ":")
}

func (p *Plugin) resetSessionID(ctx context.Context, cfg *runtimeConfiguration, baseURL string, msg incomingMessage, mappedUserID string) (string, error) {
	key := buildSessionKey(msg, mappedUserID)
	// Best-effort: try to delete the previously stored session on the opencode server
	// so leftover sessions with bad model configs don't accumulate.
	if previous, _ := p.API.KVGet(sessionKeyPrefix + key); len(previous) > 0 {
		if err := p.deleteOpenCodeSession(ctx, cfg, baseURL, string(previous)); err != nil {
			p.API.LogWarn("Failed to delete previous opencode session", "session_id", string(previous), "error", err.Error())
		}
	}
	sessionID, err := p.createOpenCodeSession(ctx, cfg, baseURL, buildSessionTitle(msg, mappedUserID))
	if err != nil {
		return "", err
	}
	if appErr := p.API.KVSet(sessionKeyPrefix+key, []byte(sessionID)); appErr != nil {
		return "", fmt.Errorf("failed to save session: %w", appErr)
	}
	return sessionID, nil
}

func (p *Plugin) getStoredSessionID(msg incomingMessage, mappedUserID string) (string, error) {
	key := buildSessionKey(msg, mappedUserID)
	stored, appErr := p.API.KVGet(sessionKeyPrefix + key)
	if appErr != nil {
		return "", fmt.Errorf("failed to load session: %w", appErr)
	}
	return string(stored), nil
}

func buildSessionTitle(msg incomingMessage, mappedUserID string) string {
	scope := "DM"
	if msg.Channel != nil && !msg.IsDM {
		scope = msg.Channel.Name
		if scope == "" {
			scope = msg.Channel.Id
		}
	}
	return truncatePlain(fmt.Sprintf("Mattermost %s %s", mappedUserID, scope), 80)
}

func truncatePlain(value string, maxLength int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if maxLength <= 0 || len(runes) <= maxLength {
		return value
	}
	return string(runes[:maxLength])
}

func splitMessage(message string, maxLength int) []string {
	message = strings.TrimSpace(message)
	if message == "" {
		return []string{"응답이 비어 있습니다."}
	}
	if maxLength <= 0 || len(message) <= maxLength {
		return []string{message}
	}
	parts := []string{}
	for len(message) > maxLength {
		cut := strings.LastIndex(message[:maxLength], "\n")
		if cut < maxLength/2 {
			cut = maxLength
		}
		parts = append(parts, strings.TrimSpace(message[:cut]))
		message = strings.TrimSpace(message[cut:])
	}
	if message != "" {
		parts = append(parts, message)
	}
	return parts
}

func isBotNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "resource bot not found") ||
		strings.Contains(lower, "bot does not exist") ||
		strings.Contains(lower, "unable to get bot")
}

const myAgentsPostSafetyMargin = 256
const myAgentsPostFallbackMaxRunes = 4000

type myAgentsStreamingUpdater struct {
	plugin       *Plugin
	botUserID    string
	channelID    string
	rootID       string
	post         *model.Post
	maxRunes     int
	flushedTools int
}

func (p *Plugin) createMyAgentsStreamingPost(channelID, rootID string) (*myAgentsStreamingUpdater, error) {
	account, err := p.getOrEnsureBot()
	if err != nil {
		return nil, err
	}
	if err := p.ensureBotInChannel(channelID, account.UserID); err != nil {
		return nil, err
	}
	post, err := p.createMyAgentsBotPost(account.UserID, channelID, rootID)
	if err != nil {
		return nil, err
	}
	updater := &myAgentsStreamingUpdater{
		plugin:    p,
		botUserID: account.UserID,
		channelID: channelID,
		rootID:    rootID,
		post:      post,
		maxRunes:  p.maxPostRunes() - myAgentsPostSafetyMargin,
	}
	if updater.maxRunes < 1024 {
		updater.maxRunes = 1024
	}
	updater.publish("start", post.Message)
	return updater, nil
}

func (p *Plugin) createMyAgentsBotPost(botUserID, channelID, rootID string) (*model.Post, error) {
	post, appErr := p.API.CreatePost(&model.Post{
		UserId:    botUserID,
		ChannelId: channelID,
		RootId:    rootID,
		Type:      "custom_myagents_bot",
		Message:   "응답 생성 중...",
		Props: map[string]any{
			"from_bot":                    "true",
			"myagents_bot":                "true",
			"myagents_streaming":          "true",
			"myagents_stream_status":      "streaming",
			"myagents_stream_placeholder": "true",
			"myagents_thinking":           "false",
		},
	})
	if appErr != nil {
		return nil, fmt.Errorf("failed to create myagents streaming post: %w", appErr)
	}
	return post, nil
}

func (p *Plugin) maxPostRunes() int {
	return myAgentsPostFallbackMaxRunes
}

func (u *myAgentsStreamingUpdater) update(message, thinking string, streaming bool) error {
	return u.writePost(message, thinking, streaming, "streaming")
}

func (u *myAgentsStreamingUpdater) writePost(message, thinking string, streaming bool, status string) error {
	if u == nil || u.post == nil {
		return nil
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "응답 생성 중..."
	}
	updated := *u.post
	updated.Message = message
	updated.Props = clonePostProps(u.post.Props)
	updated.Props["myagents_streaming"] = boolString(streaming)
	updated.Props["myagents_stream_status"] = status
	updated.Props["myagents_stream_placeholder"] = "false"
	updated.Props["myagents_thinking"] = boolString(strings.TrimSpace(thinking) != "")
	post, appErr := u.plugin.API.UpdatePost(&updated)
	if appErr != nil {
		return fmt.Errorf("failed to update myagents streaming post: %w", appErr)
	}
	u.post = post
	u.publish("delta", message)
	return nil
}

func (u *myAgentsStreamingUpdater) updateState(text, thinking string, tools []string, streaming bool) error {
	if u == nil {
		return nil
	}
	for {
		visibleTools := tools[u.flushedTools:]
		message := renderStreamingMessage(text, thinking, visibleTools, streaming)
		if u.fits(message) || len(visibleTools) <= 1 {
			if !u.fits(message) {
				message = truncateRunes(message, u.maxRunes)
			}
			return u.update(message, thinking, streaming)
		}
		// Rollover: finalize current post with all but the last tool, then start a new post.
		keep := len(tools) - 1
		chunk := tools[u.flushedTools:keep]
		chunkMessage := renderStreamingMessage("", thinking, chunk, false)
		if !u.fits(chunkMessage) {
			chunkMessage = truncateRunes(chunkMessage, u.maxRunes)
		}
		if err := u.writePost(chunkMessage+"\n\n_(이어서)_", thinking, false, "completed"); err != nil {
			return err
		}
		newPost, err := u.plugin.createMyAgentsBotPost(u.botUserID, u.channelID, u.rootID)
		if err != nil {
			return err
		}
		u.post = newPost
		u.flushedTools = keep
		u.publish("start", newPost.Message)
	}
}

func (u *myAgentsStreamingUpdater) completeState(text, thinking string, tools []string) error {
	if u == nil {
		return nil
	}
	for {
		visibleTools := tools[u.flushedTools:]
		message := renderStreamingMessage(text, thinking, visibleTools, false)
		if message == "" {
			message = "응답이 비어 있습니다."
		}
		if u.fits(message) || len(visibleTools) <= 1 {
			if !u.fits(message) {
				message = truncateRunes(message, u.maxRunes)
			}
			return u.complete(message)
		}
		keep := len(tools) - 1
		chunk := tools[u.flushedTools:keep]
		chunkMessage := renderStreamingMessage("", thinking, chunk, false)
		if !u.fits(chunkMessage) {
			chunkMessage = truncateRunes(chunkMessage, u.maxRunes)
		}
		if err := u.writePost(chunkMessage+"\n\n_(이어서)_", thinking, false, "completed"); err != nil {
			return err
		}
		newPost, err := u.plugin.createMyAgentsBotPost(u.botUserID, u.channelID, u.rootID)
		if err != nil {
			return err
		}
		u.post = newPost
		u.flushedTools = keep
		u.publish("start", newPost.Message)
	}
}

func (u *myAgentsStreamingUpdater) fits(message string) bool {
	if u.maxRunes <= 0 {
		return true
	}
	count := 0
	for range message {
		count++
		if count > u.maxRunes {
			return false
		}
	}
	return true
}

func truncateRunes(message string, maxRunes int) string {
	if maxRunes <= 0 {
		return message
	}
	runes := []rune(message)
	if len(runes) <= maxRunes {
		return message
	}
	suffix := "\n\n…(잘림)"
	cut := maxRunes - len([]rune(suffix))
	if cut < 1 {
		cut = maxRunes
		suffix = ""
	}
	return string(runes[:cut]) + suffix
}

func (u *myAgentsStreamingUpdater) complete(message string) error {
	if u == nil || u.post == nil {
		return nil
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "응답이 비어 있습니다."
	}
	updated := *u.post
	updated.Message = message
	updated.Props = clonePostProps(u.post.Props)
	updated.Props["myagents_streaming"] = "false"
	updated.Props["myagents_stream_status"] = "completed"
	updated.Props["myagents_stream_placeholder"] = "false"
	updated.Props["myagents_thinking"] = boolString(strings.Contains(message, "myagents-thinking"))
	post, appErr := u.plugin.API.UpdatePost(&updated)
	if appErr != nil {
		return fmt.Errorf("failed to complete myagents streaming post: %w", appErr)
	}
	u.post = post
	u.publish("end", message)
	return nil
}

func (u *myAgentsStreamingUpdater) fail(message string) error {
	if u == nil || u.post == nil {
		return nil
	}
	updated := *u.post
	updated.Message = strings.TrimSpace(message)
	if updated.Message == "" {
		updated.Message = "개인 에이전트 서버 오류입니다"
	}
	updated.Props = clonePostProps(u.post.Props)
	updated.Props["myagents_streaming"] = "false"
	updated.Props["myagents_stream_status"] = "failed"
	updated.Props["myagents_error"] = "true"
	post, appErr := u.plugin.API.UpdatePost(&updated)
	if appErr != nil {
		return fmt.Errorf("failed to fail myagents streaming post: %w", appErr)
	}
	u.post = post
	u.publish("end", updated.Message)
	return nil
}

func (u *myAgentsStreamingUpdater) publish(control, message string) {
	if u == nil || u.post == nil {
		return
	}
	u.plugin.API.PublishWebSocketEvent("postupdate", map[string]any{
		"post_id": u.post.Id,
		"control": control,
		"next":    message,
	}, &model.WebsocketBroadcast{ChannelId: u.post.ChannelId})
}

func clonePostProps(source model.StringInterface) model.StringInterface {
	props := model.StringInterface{}
	for key, value := range source {
		props[key] = value
	}
	return props
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

var _ = pluginapi.BotOwner
