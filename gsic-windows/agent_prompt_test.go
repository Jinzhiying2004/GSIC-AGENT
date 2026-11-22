package main

import (
	"strings"
	"testing"
)

func TestFillAgentPrompt(t *testing.T) {
	cfg := &Config{Port: 28789, Token: "testtoken123"}
	out := FillAgentPrompt(cfg, "100.99.88.77")
	if strings.Contains(out, "{{") {
		t.Errorf("unfilled placeholder in prompt:\n%s", out)
	}
	for _, want := range []string{
		"http://100.99.88.77:28789",
		"testtoken123",
		"/agent-prompt",
		"POST /task",
		"X-Token",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}
