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
	// Issue #3: the filled prompt must never contain the master token.
	if strings.Contains(out, "testtoken123") {
		t.Errorf("prompt leaks the master token")
	}
	if strings.Contains(out, "Token：") {
		t.Errorf("prompt still carries a token field")
	}
	for _, want := range []string{
		"http://100.99.88.77:28789",
		"/agent-prompt",
		"POST /task",
		"X-Token",
		"POST /pair",
		"配对码",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

// A template that still carries the old {{TOKEN}} placeholder must not
// leak the token either: the placeholder is stripped, never filled.
func TestFillAgentPromptStripsLegacyTokenPlaceholder(t *testing.T) {
	saved := agentPromptTemplate
	agentPromptTemplate = "# test\n- Token：{{TOKEN}}\n- addr http://{{TAILNET_IP}}:{{PORT}}\n"
	defer func() { agentPromptTemplate = saved }()
	cfg := &Config{Port: 28789, Token: "testtoken123"}
	out := FillAgentPrompt(cfg, "100.99.88.77")
	if strings.Contains(out, "testtoken123") || strings.Contains(out, "{{TOKEN}}") {
		t.Errorf("legacy template leaked token or placeholder: %q", out)
	}
}
