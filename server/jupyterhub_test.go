package main

import (
	"errors"
	"testing"
)

func TestIsJupyterHubAlreadyStartingOrRunning(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "pending spawn bad request",
			err:  &jupyterHubHTTPError{StatusCode: 400, Body: `{"message":"server pending spawn"}`},
			want: true,
		},
		{
			name: "already running conflict",
			err:  &jupyterHubHTTPError{StatusCode: 409, Body: `server already running`},
			want: true,
		},
		{
			name: "unrelated bad request",
			err:  &jupyterHubHTTPError{StatusCode: 400, Body: `bad user`},
			want: false,
		},
		{
			name: "plain error",
			err:  errors.New("network error"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isJupyterHubAlreadyStartingOrRunning(tt.err); got != tt.want {
				t.Fatalf("isJupyterHubAlreadyStartingOrRunning() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestJupyterHubUserServerReady(t *testing.T) {
	if !jupyterHubUserServerReady(map[string]any{"server": "/user/hkjang/"}) {
		t.Fatal("legacy server field should be ready")
	}
	if !jupyterHubUserServerReady(map[string]any{
		"servers": map[string]any{
			"": map[string]any{"ready": true},
		},
	}) {
		t.Fatal("default named server ready field should be ready")
	}
	if jupyterHubUserServerReady(map[string]any{
		"servers": map[string]any{
			"": map[string]any{"ready": false},
		},
	}) {
		t.Fatal("not-ready default server should not be ready")
	}
}
