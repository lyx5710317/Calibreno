package app

import (
	"encoding/json"
	"os"

	"dalibre/cmd/dalibre/locales"
)

// mcpSnippet is the setup that can be copied for one client.
type mcpSnippet struct {
	Client string
	Text   string
	Hint   func() string
}

func (a *App) mcpSnippets() []mcpSnippet {
	endpoint, bearer := a.mcpEndpoint(), "Bearer "+a.mcpToken()
	exe, err := os.Executable()
	if err != nil {
		exe = Name
	}
	type object = map[string]any
	config := func(name string, server object) string {
		data, _ := json.MarshalIndent(object{"mcpServers": object{name: server}}, "", "  ")
		return string(data)
	}
	headers := map[string]string{"Authorization": bearer}
	httpConfig := config("calibreno", object{"url": endpoint, "headers": headers})
	qoderConfig := config("calibreno", object{"type": "streamable-http", "url": endpoint, "headers": headers})
	return []mcpSnippet{
		{"Claude Code", `claude mcp add --transport http dalibre ` + endpoint + ` --header "Authorization: ` + bearer + `"`, locales.McpHintClaudeCode},
		{"Claude Desktop", config("dalibre", object{"command": exe, "args": []string{"mcp"}}), locales.McpHintClaudeDesktop},
		{"Cursor", config("dalibre", object{"url": endpoint, "headers": headers}), locales.McpHintCursor},
		{"TRAE", httpConfig, locales.McpHintTrae},
		{"TraeWork", httpConfig, locales.McpHintTraeWork},
		{"Qoder", qoderConfig, locales.McpHintQoder},
		{"QoderWork", qoderConfig, locales.McpHintQoderWork},
		{"Kimi Code CLI", httpConfig, locales.McpHintKimiCode},
		{"Qwen Code", config("calibreno", object{"httpUrl": endpoint, "headers": headers}), locales.McpHintQwenCode},
		{"CodeBuddy IDE", config("calibreno", object{"transportType": "streamable-http", "url": endpoint, "headers": headers, "disabled": false}), locales.McpHintCodeBuddyIDE},
		{"CodeBuddy CLI", `codebuddy mcp add --scope user --transport http --header "Authorization: ` + bearer + `" -- calibreno ` + endpoint, locales.McpHintCodeBuddy},
		{"Cherry Studio", config("calibreno", object{"type": "streamableHttp", "url": endpoint, "headers": headers}), locales.McpHintCherryStudio},
	}
}
