package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestApplyOpenCodeSSEEventHandlesOfficialPartDeltaAfterToolCalls(t *testing.T) {
	state := openCodeStreamState{}
	sessionID := "ses_123"

	events := []map[string]any{
		{
			"type": "message.part.updated",
			"properties": map[string]any{
				"sessionID": sessionID,
				"part": map[string]any{
					"id":        "part_tool",
					"sessionID": sessionID,
					"type":      "tool",
					"tool":      "bash",
					"state": map[string]any{
						"status": "completed",
						"title":  "go test ./...",
					},
				},
			},
		},
		{
			"type": "message.part.updated",
			"properties": map[string]any{
				"sessionID": sessionID,
				"part": map[string]any{
					"id":        "part_text",
					"sessionID": sessionID,
					"type":      "text",
					"text":      "",
				},
			},
		},
		{
			"type": "message.part.delta",
			"properties": map[string]any{
				"sessionID": sessionID,
				"messageID": "msg_1",
				"partID":    "part_text",
				"field":     "text",
				"delta":     "최종 ",
			},
		},
		{
			"type": "message.part.delta",
			"properties": map[string]any{
				"sessionID": sessionID,
				"messageID": "msg_1",
				"partID":    "part_text",
				"field":     "text",
				"delta":     "답변입니다.",
			},
		},
		{
			"type": "session.idle",
			"properties": map[string]any{
				"sessionID": sessionID,
			},
		},
	}

	for _, event := range events {
		if !applyOpenCodeSSEEvent(&state, sessionID, mustOpenCodeEvent(t, event)) {
			t.Fatalf("event was not applied: %#v", event)
		}
	}

	if got, want := state.Text, "최종 답변입니다."; got != want {
		t.Fatalf("state.Text = %q, want %q", got, want)
	}
	if !state.Done {
		t.Fatal("state.Done = false, want true")
	}
	rendered := renderStreamingMessage(state.Text, state.Thinking, state.Tools, true)
	if !strings.Contains(rendered, "최종 답변입니다.") {
		t.Fatalf("rendered message does not contain final answer: %q", rendered)
	}
	if !strings.Contains(rendered, "bash") {
		t.Fatalf("rendered message does not contain tool summary: %q", rendered)
	}
}

func TestApplyOpenCodeSSEEventRoutesReasoningDelta(t *testing.T) {
	state := openCodeStreamState{}
	sessionID := "ses_123"

	events := []map[string]any{
		{
			"type": "message.part.updated",
			"properties": map[string]any{
				"sessionID": sessionID,
				"part": map[string]any{
					"id":        "part_reasoning",
					"sessionID": sessionID,
					"type":      "reasoning",
					"text":      "",
				},
			},
		},
		{
			"type": "message.part.delta",
			"properties": map[string]any{
				"sessionID": sessionID,
				"partID":    "part_reasoning",
				"field":     "text",
				"delta":     "생각 중",
			},
		},
	}

	for _, event := range events {
		if !applyOpenCodeSSEEvent(&state, sessionID, mustOpenCodeEvent(t, event)) {
			t.Fatalf("event was not applied: %#v", event)
		}
	}

	if got, want := state.Thinking, "생각 중"; got != want {
		t.Fatalf("state.Thinking = %q, want %q", got, want)
	}
	if state.Text != "" {
		t.Fatalf("state.Text = %q, want empty", state.Text)
	}
}

func TestSplitThinkBlocksCompletesEachClosingThinkTag(t *testing.T) {
	visible, completed, active := splitThinkBlocks("첫 추론</think>둘째 추론</think>최종 답변")

	if active != "" {
		t.Fatalf("active = %q, want empty", active)
	}
	if got, want := visible, "최종 답변"; got != want {
		t.Fatalf("visible = %q, want %q", got, want)
	}
	if len(completed) != 2 {
		t.Fatalf("completed len = %d, want 2: %#v", len(completed), completed)
	}
	if completed[0] != "첫 추론" || completed[1] != "둘째 추론" {
		t.Fatalf("completed = %#v", completed)
	}
}

func TestFinalOpenCodeMessageKeepsThinkingAndTools(t *testing.T) {
	state := openCodeStreamState{
		Text: "<think>계획 수립</think>",
		Tools: []string{
			renderToolBlock("bash go test ./... (completed)", "", "ok\t./server\t0.1s"),
		},
	}

	final := finalOpenCodeMessageOrEmpty(state)

	if strings.Contains(final, "응답이 비어 있습니다.") {
		t.Fatalf("final message was treated as empty: %q", final)
	}
	if !strings.Contains(final, "계획 수립") {
		t.Fatalf("final message does not include thinking: %q", final)
	}
	if !strings.Contains(final, "go test ./...") || !strings.Contains(final, "ok\t./server") {
		t.Fatalf("final message does not include tool output: %q", final)
	}
}

func TestRenderToolPartIncludesTerminalOutput(t *testing.T) {
	rendered := renderToolPart(map[string]any{
		"id":   "part_tool",
		"type": "tool",
		"tool": "bash",
		"state": map[string]any{
			"status": "completed",
			"title":  "npm test",
			"output": "\x1b[31mFAIL\x1b[0m\n```inside output```",
		},
	})

	if !strings.Contains(rendered, "터미널 출력") {
		t.Fatalf("rendered tool part does not include output label: %q", rendered)
	}
	if strings.Contains(rendered, "\x1b") {
		t.Fatalf("rendered tool part still contains ANSI escapes: %q", rendered)
	}
	if !strings.Contains(rendered, "````console") {
		t.Fatalf("rendered tool part did not expand markdown fence: %q", rendered)
	}
	if !strings.Contains(rendered, "FAIL") || !strings.Contains(rendered, "inside output") {
		t.Fatalf("rendered tool part lost terminal text: %q", rendered)
	}
}

func mustOpenCodeEvent(t *testing.T, value map[string]any) openCodeSSEEvent {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return openCodeSSEEvent{Data: body}
}
