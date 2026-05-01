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

func mustOpenCodeEvent(t *testing.T, value map[string]any) openCodeSSEEvent {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return openCodeSSEEvent{Data: body}
}
