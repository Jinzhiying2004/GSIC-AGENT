package main

import (
	_ "embed"
	"fmt"
	"os"
	"strings"
)

//go:embed assets/agent_prompt.md
var agentPromptTemplate string

// windowsPlatformNotes fills the {{PLATFORM_NOTES}} section of the
// agent prompt for the Windows build.
const windowsPlatformNotes = `## 平台说明（Windows）

- 鼠标键盘坐标是屏幕物理像素，左上角是 (0, 0)。
- shell 默认是 cmd；用 PowerShell 时 shell 填 "powershell"。注意 PowerShell 5.1 默认 TLS 1.0，有些网站连不上，优先用 cmd + curl.exe。
- 路径用 Windows 风格，如 C:\Users\xxx\Downloads\文件.txt。

## 例子：打开记事本打一行字

1. GET /status 确认在线
2. POST /task：[{"action": "launch", "path": "notepad.exe"}, {"action": "sleep", "ms": 1000}, {"action": "type", "text": "Hello from GSIC"}]
3. GET /screenshot 确认字打上去了`

// FillAgentPrompt renders the agent prompt template with this
// device's live connection info.
func FillAgentPrompt(cfg *Config, tailIP string) string {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "-"
	}
	r := strings.NewReplacer(
		"{{TAILNET_IP}}", tailIP,
		"{{PORT}}", fmt.Sprintf("%d", cfg.Port),
		"{{TOKEN}}", cfg.Token,
		"{{HOSTNAME}}", hostname,
		"{{PLATFORM}}", "Windows",
		"{{PLATFORM_NOTES}}", windowsPlatformNotes,
	)
	return r.Replace(agentPromptTemplate)
}
