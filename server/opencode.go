package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type openCodeSessionCreateRequest struct {
	ParentID string `json:"parentID,omitempty"`
	Title    string `json:"title,omitempty"`
}

type openCodeSessionCreateResponse struct {
	ID string `json:"id"`
}

type openCodeMessageRequest struct {
	MessageID string          `json:"messageID,omitempty"`
	Model     map[string]any  `json:"model,omitempty"`
	Agent     string          `json:"agent,omitempty"`
	NoReply   bool            `json:"noReply,omitempty"`
	System    string          `json:"system,omitempty"`
	Tools     map[string]bool `json:"tools,omitempty"`
	Parts     []openCodePart  `json:"parts"`
}

type openCodePart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type openCodeCallError struct {
	Code       string
	Message    string
	StatusCode int
}

func (e *openCodeCallError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

type openCodeSSEEvent struct {
	Event string
	Data  []byte
}

type openCodeStreamState struct {
	MessageID       string
	Text            string
	Thinking        string
	Tools           []string
	PartTypes       map[string]string
	ToolPartIndexes map[string]int
	ToolLabels      map[string]string
	ToolInputs      map[string]string
	ToolOutputs     map[string]string
	Done            bool
}

func (p *Plugin) createOpenCodeSession(ctx context.Context, cfg *runtimeConfiguration, baseURL, title string) (string, error) {
	endpoint, err := openCodeURL(baseURL, "session")
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(openCodeSessionCreateRequest{Title: strings.TrimSpace(title)})
	if err != nil {
		return "", err
	}
	responseBody, statusCode, err := p.doOpenCodeJSON(ctx, http.MethodPost, endpoint, body, cfg.RequestTimeout)
	if err != nil {
		return "", err
	}
	if statusCode >= http.StatusBadRequest {
		return "", classifyOpenCodeHTTPError(statusCode)
	}
	var session openCodeSessionCreateResponse
	if err := json.Unmarshal(responseBody, &session); err != nil || strings.TrimSpace(session.ID) == "" {
		return "", &openCodeCallError{Code: "parse_failed", Message: "응답 형식을 해석할 수 없습니다", StatusCode: statusCode}
	}
	return session.ID, nil
}

func (p *Plugin) streamOpenCodeMessage(ctx context.Context, cfg *runtimeConfiguration, baseURL, sessionID, channelID, rootID, prompt string) (string, error) {
	updater, err := p.createMyAgentsStreamingPost(channelID, rootID)
	if err != nil {
		return "", err
	}

	streamCtx, cancel := context.WithTimeout(ctx, cfg.RequestTimeout)
	defer cancel()

	events, errs := p.openCodeEventStream(streamCtx, baseURL)
	if err := p.sendOpenCodeAsync(ctx, cfg, baseURL, sessionID, prompt); err != nil {
		_ = updater.fail(userFacingOpenCodeError(err))
		return "", err
	}

	state := openCodeStreamState{}
	lastRendered := ""
	for {
		select {
		case event, ok := <-events:
			if !ok {
				final := finalOpenCodeMessageOrEmpty(state)
				return final, updater.complete(final)
			}
			if !applyOpenCodeSSEEvent(&state, sessionID, event) {
				continue
			}
			rendered := renderStreamingMessage(state.Text, state.Thinking, state.Tools, !state.Done)
			if rendered != "" && rendered != lastRendered {
				if err := updater.update(rendered, state.Thinking, !state.Done); err != nil {
					return "", err
				}
				lastRendered = rendered
			}
			if state.Done {
				final := finalOpenCodeMessageOrEmpty(state)
				return final, updater.complete(final)
			}
		case err := <-errs:
			if err == nil {
				continue
			}
			if final := finalOpenCodeMessage(state); final != "" {
				return final, updater.complete(final)
			}
			_ = updater.fail(userFacingOpenCodeError(err))
			return "", err
		case <-streamCtx.Done():
			err := classifyOpenCodeRequestError(streamCtx.Err())
			if final := finalOpenCodeMessage(state); final != "" {
				return final, updater.complete(final)
			}
			_ = updater.fail(userFacingOpenCodeError(err))
			return "", err
		}
	}
}

func (p *Plugin) sendOpenCodeMessage(ctx context.Context, cfg *runtimeConfiguration, baseURL, sessionID, prompt string) (string, error) {
	return p.sendOpenCodePrompt(ctx, baseURL, sessionID, "message", prompt, cfg.RequestTimeout)
}

func (p *Plugin) sendOpenCodeAsync(ctx context.Context, cfg *runtimeConfiguration, baseURL, sessionID, prompt string) error {
	_, err := p.sendOpenCodePrompt(ctx, baseURL, sessionID, "prompt_async", prompt, cfg.RequestTimeout)
	return err
}

func (p *Plugin) sendOpenCodePrompt(ctx context.Context, baseURL, sessionID, endpointName, prompt string, timeoutDuration time.Duration) (string, error) {
	endpoint, err := openCodeURL(baseURL, "session", sessionID, endpointName)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(openCodeMessageRequest{
		Parts: []openCodePart{{Type: "text", Text: prompt}},
	})
	if err != nil {
		return "", err
	}
	responseBody, statusCode, err := p.doOpenCodeJSON(ctx, http.MethodPost, endpoint, body, timeoutDuration)
	if err != nil {
		return "", err
	}
	if statusCode >= http.StatusBadRequest {
		return "", classifyOpenCodeHTTPError(statusCode)
	}
	if statusCode == http.StatusNoContent || len(responseBody) == 0 {
		return "", nil
	}
	output, err := renderOpenCodeResponse(responseBody)
	if err != nil {
		return "", &openCodeCallError{Code: "parse_failed", Message: "응답 형식을 해석할 수 없습니다", StatusCode: statusCode}
	}
	return output, nil
}

func (p *Plugin) openCodeEventStream(ctx context.Context, baseURL string) (<-chan openCodeSSEEvent, <-chan error) {
	events := make(chan openCodeSSEEvent)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		endpoint, err := openCodeURL(baseURL, "event")
		if err != nil {
			errs <- err
			return
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			errs <- err
			return
		}
		request.Header.Set("Accept", "text/event-stream")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			errs <- classifyOpenCodeRequestError(err)
			return
		}
		defer response.Body.Close()
		if response.StatusCode >= http.StatusBadRequest {
			errs <- classifyOpenCodeHTTPError(response.StatusCode)
			return
		}
		reader := bufio.NewReader(response.Body)
		for {
			event, err := readSSEEvent(reader)
			if err != nil {
				if errors.Is(err, io.EOF) || ctx.Err() != nil {
					return
				}
				errs <- err
				return
			}
			if event.Event == "" && len(event.Data) == 0 {
				continue
			}
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return events, errs
}

func readSSEEvent(reader *bufio.Reader) (openCodeSSEEvent, error) {
	var event openCodeSSEEvent
	dataLines := []string{}
	for {
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return event, err
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			event.Data = []byte(strings.Join(dataLines, "\n"))
			return event, nil
		}
		if strings.HasPrefix(line, "event:") {
			event.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
		if errors.Is(err, io.EOF) {
			event.Data = []byte(strings.Join(dataLines, "\n"))
			return event, nil
		}
	}
}

func applyOpenCodeSSEEvent(state *openCodeStreamState, sessionID string, event openCodeSSEEvent) bool {
	eventName := strings.TrimSpace(event.Event)
	var payload any
	if len(event.Data) > 0 {
		_ = json.Unmarshal(event.Data, &payload)
	}

	switch eventName {
	case "message_start":
		if data, ok := payload.(map[string]any); ok {
			state.MessageID = firstTextField(data, "id", "messageID")
		}
		return true
	case "message_delta":
		if data, ok := payload.(map[string]any); ok {
			if delta := firstRawTextField(data, "delta", "text", "content"); delta != "" {
				state.Text += delta
				return true
			}
		}
	case "message_end":
		if data, ok := payload.(map[string]any); ok {
			if content := firstTextField(data, "content", "text"); content != "" {
				state.Text = content
			}
		}
		state.Done = true
		return true
	}

	envelope := normalizeOpenCodeEventPayload(payload)
	if envelope == nil {
		if eventName == "message.part.updated" || eventName == "message.part.delta" || eventName == "message.updated" || eventName == "session.idle" || eventName == "session.error" {
			envelope = map[string]any{
				"type":       eventName,
				"properties": payload,
			}
		} else {
			return false
		}
	}
	eventType := stringValue(envelope["type"])
	props, _ := envelope["properties"].(map[string]any)
	switch eventType {
	case "message.part.updated":
		part, _ := props["part"].(map[string]any)
		if part == nil || stringValue(part["sessionID"]) != sessionID {
			return false
		}
		rememberOpenCodePartType(state, part)
		delta := rawStringValue(props["delta"])
		partID := firstTextField(part, "id", "partID")
		partType := stringValue(part["type"])
		switch partType {
		case "text":
			if delta != "" {
				state.Text += delta
			} else if text := rawStringValue(part["text"]); text != "" {
				state.Text = text
			}
			return true
		case "reasoning":
			if delta != "" {
				state.Thinking += delta
			} else if text := rawStringValue(part["text"]); text != "" {
				state.Thinking = text
			}
			return true
		case "tool":
			return rememberOpenCodeToolPart(state, partID, part)
		case "file":
			return rememberOpenCodeFilePart(state, partID, part)
		}
	case "message.part.delta":
		if props == nil || stringValue(props["sessionID"]) != sessionID {
			return false
		}
		field := stringValue(props["field"])
		delta := rawStringValue(props["delta"])
		if delta == "" {
			return false
		}
		partID := stringValue(props["partID"])
		partType := openCodePartType(state, partID)
		if partType == "tool" || partType == "file" {
			if isRenderableToolDeltaField(field) {
				return appendOpenCodeToolOutput(state, partID, delta)
			}
			return false
		}
		if field != "" && field != "text" && field != "content" {
			return false
		}
		if partType == "reasoning" {
			state.Thinking += delta
			return true
		}
		state.Text += delta
		return true
	case "message.updated":
		info, _ := props["info"].(map[string]any)
		if info == nil || stringValue(info["sessionID"]) != sessionID {
			return false
		}
		if text := extractOpenCodeTextFromEventProperties(props); text != "" {
			state.Text = text
		}
		if stringValue(info["role"]) == "assistant" {
			if timeInfo, ok := info["time"].(map[string]any); ok && timeInfo["completed"] != nil {
				state.Done = true
				return true
			}
		}
	case "session.idle":
		if props != nil && stringValue(props["sessionID"]) == sessionID {
			state.Done = true
			return true
		}
	case "session.error":
		if props == nil || stringValue(props["sessionID"]) == sessionID {
			state.Text = "개인 에이전트 서버 오류입니다"
			state.Done = true
			return true
		}
	}
	return false
}

func rememberOpenCodePartType(state *openCodeStreamState, part map[string]any) {
	partID := firstTextField(part, "id", "partID")
	partType := stringValue(part["type"])
	if partID == "" || partType == "" {
		return
	}
	if state.PartTypes == nil {
		state.PartTypes = map[string]string{}
	}
	state.PartTypes[partID] = partType
}

func openCodePartType(state *openCodeStreamState, partID string) string {
	if state.PartTypes == nil || partID == "" {
		return ""
	}
	return state.PartTypes[partID]
}

func extractOpenCodeTextFromEventProperties(props map[string]any) string {
	for _, key := range []string{"parts", "message", "data"} {
		if value, ok := props[key]; ok {
			if text := extractOpenCodeAnswerText(value); text != "" {
				return text
			}
		}
	}
	return ""
}

func normalizeOpenCodeEventPayload(payload any) map[string]any {
	typed, ok := payload.(map[string]any)
	if !ok {
		return nil
	}
	if payloadValue, ok := typed["payload"].(map[string]any); ok {
		return payloadValue
	}
	if typed["type"] != nil {
		return typed
	}
	return nil
}

var ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

func rememberOpenCodeToolPart(state *openCodeStreamState, partID string, part map[string]any) bool {
	label := renderToolLabel(part)
	input := extractToolInput(part)
	output := extractToolOutput(part)
	if partID != "" {
		if state.ToolLabels == nil {
			state.ToolLabels = map[string]string{}
		}
		if label != "" {
			state.ToolLabels[partID] = label
		}
		if input != "" {
			if state.ToolInputs == nil {
				state.ToolInputs = map[string]string{}
			}
			state.ToolInputs[partID] = input
		} else if state.ToolInputs != nil {
			input = state.ToolInputs[partID]
		}
		if output != "" {
			if state.ToolOutputs == nil {
				state.ToolOutputs = map[string]string{}
			}
			state.ToolOutputs[partID] = output
		} else if state.ToolOutputs != nil {
			output = state.ToolOutputs[partID]
		}
		label = state.ToolLabels[partID]
	}
	return rememberOpenCodeRenderedToolBlock(state, partID, renderStoredToolBlock(label, input, output))
}

func rememberOpenCodeFilePart(state *openCodeStreamState, partID string, part map[string]any) bool {
	label := renderFileLabel(part)
	content := extractFileContent(part)
	if partID != "" {
		if state.ToolLabels == nil {
			state.ToolLabels = map[string]string{}
		}
		state.ToolLabels[partID] = label
		if content != "" {
			if state.ToolOutputs == nil {
				state.ToolOutputs = map[string]string{}
			}
			state.ToolOutputs[partID] = content
		} else if state.ToolOutputs != nil {
			content = state.ToolOutputs[partID]
		}
	}
	return rememberOpenCodeRenderedToolBlock(state, partID, renderStoredToolBlock(label, "", content))
}

func appendOpenCodeToolOutput(state *openCodeStreamState, partID, delta string) bool {
	delta = cleanTerminalText(delta)
	if partID == "" || delta == "" {
		return false
	}
	if state.ToolOutputs == nil {
		state.ToolOutputs = map[string]string{}
	}
	state.ToolOutputs[partID] += delta
	label := "tool"
	if state.ToolLabels != nil && state.ToolLabels[partID] != "" {
		label = state.ToolLabels[partID]
	}
	input := ""
	if state.ToolInputs != nil {
		input = state.ToolInputs[partID]
	}
	return rememberOpenCodeRenderedToolBlock(state, partID, renderStoredToolBlock(label, input, state.ToolOutputs[partID]))
}

func rememberOpenCodeRenderedToolBlock(state *openCodeStreamState, partID, block string) bool {
	block = strings.TrimSpace(block)
	if block == "" {
		return false
	}
	if partID != "" {
		if state.ToolPartIndexes == nil {
			state.ToolPartIndexes = map[string]int{}
		}
		if index, ok := state.ToolPartIndexes[partID]; ok && index >= 0 && index < len(state.Tools) {
			if state.Tools[index] == block {
				return false
			}
			state.Tools[index] = block
			return true
		}
		state.ToolPartIndexes[partID] = len(state.Tools)
		state.Tools = append(state.Tools, block)
		return true
	}
	if !containsString(state.Tools, block) {
		state.Tools = append(state.Tools, block)
		return true
	}
	return false
}

func isRenderableToolDeltaField(field string) bool {
	switch strings.ToLower(strings.TrimSpace(field)) {
	case "", "text", "content", "output", "stdout", "stderr", "result", "data", "source", "code", "message", "error":
		return true
	default:
		return false
	}
}

func renderToolPart(part map[string]any) string {
	return renderStoredToolBlock(renderToolLabel(part), extractToolInput(part), extractToolOutput(part))
}

func renderToolLabel(part map[string]any) string {
	tool := firstTextField(part, "tool", "name", "title")
	state, _ := part["state"].(map[string]any)
	if state == nil {
		if tool == "" {
			return "tool"
		}
		return tool
	}
	title := firstTextField(state, "title")
	status := firstTextField(state, "status")
	label := strings.TrimSpace(strings.Join([]string{tool, title}, " "))
	if label == "" {
		label = "tool"
	}
	if status != "" {
		label += " (" + status + ")"
	}
	return label
}

func renderStoredToolBlock(label, input, output string) string {
	if strings.HasPrefix(strings.TrimSpace(label), "파일") {
		return renderFileBlock(label, output)
	}
	return renderToolBlock(label, input, output)
}

func renderToolBlock(label, input, output string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		label = "tool"
	}
	blocks := []string{fmt.Sprintf("> 도구 호출: `%s`", inlineCode(label))}
	if input = sanitizeTerminalText(input); input != "" {
		blocks = append(blocks, "입력:\n\n"+fencedCodeBlock(codeLanguageForTool(label, input), input))
	}
	if output = sanitizeTerminalText(output); output != "" {
		blocks = append(blocks, "터미널 출력:\n\n"+fencedCodeBlock(codeLanguageForTool(label, output), output))
	}
	return strings.TrimSpace(strings.Join(blocks, "\n\n"))
}

func renderFilePart(part map[string]any) string {
	return renderFileBlock(renderFileLabel(part), extractFileContent(part))
}

func renderFileLabel(part map[string]any) string {
	filename := firstTextField(part, "filename", "path", "url", "name", "title")
	if filename == "" {
		return "파일"
	}
	return "파일: " + filename
}

func renderFileBlock(label, content string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		label = "파일"
	}
	blocks := []string{fmt.Sprintf("> %s", inlineCode(label))}
	if content = sanitizeTerminalText(content); content != "" {
		blocks = append(blocks, "소스:\n\n"+fencedCodeBlock(codeLanguageForFilename(label, content), content))
	}
	return strings.TrimSpace(strings.Join(blocks, "\n\n"))
}

func extractToolInput(part map[string]any) string {
	state, _ := part["state"].(map[string]any)
	for _, source := range []map[string]any{state, part} {
		if source == nil {
			continue
		}
		if command := firstStructuredTextField(source, "command", "script"); command != "" {
			return command
		}
		if input := firstStructuredTextField(source, "input", "args", "arguments", "parameters"); input != "" {
			return input
		}
	}
	return ""
}

func extractToolOutput(part map[string]any) string {
	state, _ := part["state"].(map[string]any)
	for _, source := range []map[string]any{state, part} {
		if source == nil {
			continue
		}
		outputs := []string{}
		if stdout := firstStructuredTextField(source, "stdout"); stdout != "" {
			outputs = append(outputs, "stdout:\n"+stdout)
		}
		if stderr := firstStructuredTextField(source, "stderr"); stderr != "" {
			outputs = append(outputs, "stderr:\n"+stderr)
		}
		if len(outputs) > 0 {
			return strings.Join(outputs, "\n\n")
		}
		if output := firstStructuredTextField(source, "output", "result", "results", "response", "error", "message", "content", "data"); output != "" {
			return output
		}
	}
	return ""
}

func extractFileContent(part map[string]any) string {
	return firstStructuredTextField(part, "content", "text", "source", "code", "data")
}

func firstStructuredTextField(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if text := structuredTextValue(values[key]); text != "" {
			return text
		}
	}
	return ""
}

func structuredTextValue(value any) string {
	switch typed := value.(type) {
	case string:
		return sanitizeTerminalText(typed)
	case nil:
		return ""
	case map[string]any, []any:
		body, err := json.MarshalIndent(typed, "", "  ")
		if err != nil {
			return ""
		}
		return sanitizeTerminalText(string(body))
	default:
		return sanitizeTerminalText(fmt.Sprint(typed))
	}
}

func sanitizeTerminalText(value string) string {
	return strings.TrimSpace(cleanTerminalText(value))
}

func cleanTerminalText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return ansiEscapePattern.ReplaceAllString(value, "")
}

func fencedCodeBlock(language, text string) string {
	text = sanitizeTerminalText(text)
	if text == "" {
		return ""
	}
	fence := markdownFence(text)
	language = strings.TrimSpace(language)
	if language != "" {
		return fmt.Sprintf("%s%s\n%s\n%s", fence, language, text, fence)
	}
	return fmt.Sprintf("%s\n%s\n%s", fence, text, fence)
}

func markdownFence(text string) string {
	maxRun := 2
	run := 0
	for _, r := range text {
		if r == '`' {
			run++
			if run > maxRun {
				maxRun = run
			}
			continue
		}
		run = 0
	}
	return strings.Repeat("`", maxRun+1)
}

func codeLanguageForTool(label, content string) string {
	lowerLabel := strings.ToLower(label)
	switch {
	case strings.Contains(lowerLabel, "powershell") || strings.Contains(lowerLabel, "pwsh"):
		return "powershell"
	case strings.Contains(lowerLabel, "bash") || strings.Contains(lowerLabel, "shell") || strings.Contains(lowerLabel, "terminal") || strings.Contains(lowerLabel, "command"):
		return "console"
	case json.Valid([]byte(strings.TrimSpace(content))):
		return "json"
	default:
		return "text"
	}
}

func codeLanguageForFilename(label, content string) string {
	lower := strings.ToLower(label)
	switch {
	case strings.HasSuffix(lower, ".go"):
		return "go"
	case strings.HasSuffix(lower, ".ts"):
		return "typescript"
	case strings.HasSuffix(lower, ".tsx"):
		return "tsx"
	case strings.HasSuffix(lower, ".js"):
		return "javascript"
	case strings.HasSuffix(lower, ".jsx"):
		return "jsx"
	case strings.HasSuffix(lower, ".json") || json.Valid([]byte(strings.TrimSpace(content))):
		return "json"
	case strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml"):
		return "yaml"
	case strings.HasSuffix(lower, ".md"):
		return "markdown"
	case strings.HasSuffix(lower, ".py"):
		return "python"
	case strings.HasSuffix(lower, ".sh"):
		return "bash"
	case strings.HasSuffix(lower, ".ps1"):
		return "powershell"
	case strings.HasSuffix(lower, ".sql"):
		return "sql"
	case strings.HasSuffix(lower, ".html"):
		return "html"
	case strings.HasSuffix(lower, ".css"):
		return "css"
	case strings.HasSuffix(lower, ".scss"):
		return "scss"
	default:
		return "text"
	}
}

func inlineCode(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(value), "`", "'")
}

func renderStreamingMessage(text, thinking string, tools []string, streaming bool) string {
	visibleText, completedThinking, activeThinking := splitThinkBlocks(text)
	if activeThinking != "" {
		thinking = strings.TrimSpace(thinking + "\n" + activeThinking)
	}
	text = strings.TrimSpace(visibleText)
	blocks := make([]string, 0, len(completedThinking)+1)
	for _, item := range completedThinking {
		item = strings.TrimSpace(removeThinkTags(item))
		if item != "" {
			blocks = append(blocks, fmt.Sprintf("<div class=\"myagents-thinking myagents-thinking-complete\">%s</div>", item))
		}
	}
	thinking = strings.TrimSpace(removeThinkTags(thinking))
	if thinking != "" {
		className := "myagents-thinking"
		if !streaming {
			className += " myagents-thinking-complete"
		}
		blocks = append(blocks, fmt.Sprintf("<div class=\"%s\">%s</div>", className, thinking))
	}
	for _, tool := range tools {
		if strings.TrimSpace(tool) != "" {
			blocks = append(blocks, tool)
		}
	}
	blocks = append(blocks, text)
	return strings.TrimSpace(strings.Join(blocks, "\n\n"))
}

func finalOpenCodeMessage(state openCodeStreamState) string {
	return strings.TrimSpace(renderStreamingMessage(state.Text, state.Thinking, state.Tools, false))
}

func finalOpenCodeMessageOrEmpty(state openCodeStreamState) string {
	final := finalOpenCodeMessage(state)
	if final == "" {
		return "응답이 비어 있습니다."
	}
	return final
}

func splitThinkBlocks(text string) (string, []string, string) {
	remaining := text
	visible := strings.Builder{}
	completed := []string{}
	active := ""
	for {
		lower := strings.ToLower(remaining)
		start := strings.Index(lower, "<think>")
		endBeforeStart := strings.Index(lower, "</think>")
		if start < 0 && endBeforeStart < 0 {
			visible.WriteString(removeThinkTags(remaining))
			break
		}
		if endBeforeStart >= 0 && (start < 0 || endBeforeStart < start) {
			if before := strings.TrimSpace(remaining[:endBeforeStart]); before != "" {
				completed = append(completed, before)
			}
			remaining = remaining[endBeforeStart+len("</think>"):]
			continue
		}

		visible.WriteString(remaining[:start])
		afterStart := remaining[start+len("<think>"):]
		end := strings.Index(strings.ToLower(afterStart), "</think>")
		if end < 0 {
			active = strings.TrimSpace(afterStart)
			break
		}
		if block := strings.TrimSpace(afterStart[:end]); block != "" {
			completed = append(completed, block)
		}
		remaining = afterStart[end+len("</think>"):]
	}
	return strings.TrimSpace(visible.String()), completed, active
}

func removeThinkBlocks(text string) string {
	visible, _, _ := splitThinkBlocks(text)
	return visible
}

func removeThinkTags(text string) string {
	replacer := strings.NewReplacer("<think>", "", "</think>", "", "<THINK>", "", "</THINK>", "")
	return strings.TrimSpace(replacer.Replace(text))
}

func (p *Plugin) doOpenCodeJSON(ctx context.Context, method, endpoint string, body []byte, timeoutDuration time.Duration) ([]byte, int, error) {
	requestCtx, cancel := context.WithTimeout(ctx, timeoutDuration)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, 0, classifyOpenCodeRequestError(err)
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
	if readErr != nil {
		return nil, response.StatusCode, &openCodeCallError{Code: "parse_failed", Message: "응답 형식을 해석할 수 없습니다", StatusCode: response.StatusCode}
	}
	return responseBody, response.StatusCode, nil
}

func renderOpenCodeResponse(body []byte) (string, error) {
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return strings.TrimSpace(string(body)), nil
	}
	if message := extractOpenCodeErrorMessage(payload); message != "" {
		return "오류: " + message, nil
	}
	text := extractOpenCodeText(payload)
	if text != "" {
		return text, nil
	}
	pretty, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(pretty)), nil
}

func extractOpenCodeErrorMessage(value any) string {
	typed, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	info, ok := typed["info"].(map[string]any)
	if !ok {
		return ""
	}
	errValue, ok := info["error"].(map[string]any)
	if !ok {
		return ""
	}
	if data, ok := errValue["data"].(map[string]any); ok {
		if message := stringValue(data["message"]); message != "" {
			return message
		}
	}
	return stringValue(errValue["name"])
}

func extractOpenCodeText(value any) string {
	parts := make([]string, 0)
	if typed, ok := value.(map[string]any); ok {
		if responseParts, ok := typed["parts"]; ok {
			collectOpenCodeParts(responseParts, &parts)
			return strings.TrimSpace(strings.Join(parts, "\n\n"))
		}
	}
	collectOpenCodeParts(value, &parts)
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func extractOpenCodeAnswerText(value any) string {
	parts := make([]string, 0)
	collectOpenCodeAnswerText(value, &parts)
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func collectOpenCodeAnswerText(value any, parts *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		partType := strings.ToLower(stringValue(typed["type"]))
		if partType == "text" {
			if text := firstTextField(typed, "text", "content", "message"); text != "" {
				*parts = append(*parts, text)
				return
			}
		}
		if partType == "reasoning" || partType == "tool" || partType == "file" {
			return
		}
		for key, nested := range typed {
			lowerKey := strings.ToLower(key)
			if lowerKey == "parts" || lowerKey == "messages" || lowerKey == "message" || lowerKey == "content" || lowerKey == "data" {
				collectOpenCodeAnswerText(nested, parts)
			}
		}
	case []any:
		for _, item := range typed {
			collectOpenCodeAnswerText(item, parts)
		}
	}
}

func collectOpenCodeParts(value any, parts *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		partType := strings.ToLower(stringValue(typed["type"]))
		switch partType {
		case "text", "reasoning":
			if text := firstTextField(typed, "text", "content", "message"); text != "" {
				*parts = append(*parts, text)
				return
			}
		case "tool":
			if label := renderToolPart(typed); label != "" {
				*parts = append(*parts, label)
				return
			}
		case "file":
			if file := renderFilePart(typed); file != "" {
				*parts = append(*parts, file)
				return
			}
		}
		for key, nested := range typed {
			lowerKey := strings.ToLower(key)
			if lowerKey == "parts" || lowerKey == "messages" || lowerKey == "message" || lowerKey == "content" || lowerKey == "data" {
				collectOpenCodeParts(nested, parts)
			}
		}
	case []any:
		for _, item := range typed {
			collectOpenCodeParts(item, parts)
		}
	case string:
		if strings.TrimSpace(typed) != "" {
			*parts = append(*parts, strings.TrimSpace(typed))
		}
	}
}

func firstTextField(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if text := stringValue(values[key]); text != "" {
			return text
		}
	}
	return ""
}

func firstRawTextField(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if text := rawStringValue(values[key]); text != "" {
			return text
		}
	}
	return ""
}

func containsString(values []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func classifyOpenCodeHTTPError(statusCode int) error {
	switch {
	case statusCode == http.StatusNotFound:
		return &openCodeCallError{Code: "not_found", Message: "세션 없음. 새 대화를 시작합니다", StatusCode: statusCode}
	case statusCode == http.StatusRequestTimeout || statusCode == http.StatusGatewayTimeout:
		return &openCodeCallError{Code: "timeout", Message: "응답 시간이 초과되었습니다", StatusCode: statusCode}
	case statusCode >= 400 && statusCode < 500:
		return &openCodeCallError{Code: "bad_request", Message: "요청을 처리할 수 없습니다", StatusCode: statusCode}
	case statusCode >= 500:
		return &openCodeCallError{Code: "server_error", Message: "개인 에이전트 서버 오류입니다", StatusCode: statusCode}
	default:
		return &openCodeCallError{Code: "unexpected", Message: "응답 형식을 해석할 수 없습니다", StatusCode: statusCode}
	}
}

func classifyOpenCodeRequestError(err error) error {
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return &openCodeCallError{Code: "timeout", Message: "응답 시간이 초과되었습니다"}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &openCodeCallError{Code: "timeout", Message: "응답 시간이 초과되었습니다"}
	}
	var dnsErr *net.DNSError
	var hostErr x509.HostnameError
	var caErr x509.UnknownAuthorityError
	if errors.As(err, &dnsErr) || errors.As(err, &hostErr) || errors.As(err, &caErr) {
		return &openCodeCallError{Code: "connect_failed", Message: "개인 에이전트 서버에 연결할 수 없습니다"}
	}
	return &openCodeCallError{Code: "connect_failed", Message: "개인 에이전트 서버에 연결할 수 없습니다"}
}

func userFacingOpenCodeError(err error) string {
	var callErr *openCodeCallError
	if errors.As(err, &callErr) {
		return callErr.Message
	}
	return "개인 에이전트 서버에 연결할 수 없습니다"
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func rawStringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case nil:
		return ""
	default:
		return fmt.Sprint(typed)
	}
}

func openCodeURL(base string, segments ...string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("opencode base URL must include scheme and host")
	}
	parsed.Path = joinURLPath(parsed.Path, segments...)
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func joinURLPath(base string, segments ...string) string {
	parts := splitPathSegments(base)
	for _, segment := range segments {
		segment = strings.Trim(segment, "/")
		if segment != "" {
			parts = append(parts, url.PathEscape(segment))
		}
	}
	if len(parts) == 0 {
		return "/"
	}
	return "/" + strings.Join(parts, "/")
}
