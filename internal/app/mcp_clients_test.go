package app

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"dalibre/internal/settings"
)

func TestMCPClientPresets(t *testing.T) {
	const endpoint = "http://127.0.0.1:9876/mcp"
	const bearer = "Bearer test-token"
	a := &App{settings: &settings.Settings{MCP: settings.MCP{Port: 9876, Token: "test-token"}}}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	type object = map[string]any
	headers := object{"Authorization": bearer}
	http := object{"url": endpoint, "headers": headers}
	qoder := object{"type": "streamable-http", "url": endpoint, "headers": headers}
	presets := map[string]struct {
		name   string
		fields object
		cmd    string
	}{
		"Claude Code":    {cmd: `claude mcp add --transport http dalibre http://127.0.0.1:9876/mcp --header "Authorization: Bearer test-token"`},
		"Claude Desktop": {name: "dalibre", fields: object{"command": exe, "args": []any{"mcp"}}},
		"Cursor":         {name: "dalibre", fields: http},
		"TRAE":           {name: "calibreno", fields: http},
		"TraeWork":       {name: "calibreno", fields: http},
		"Qoder":          {name: "calibreno", fields: qoder},
		"QoderWork":      {name: "calibreno", fields: qoder},
		"Kimi Code CLI":  {name: "calibreno", fields: http},
		"Qwen Code":      {name: "calibreno", fields: object{"httpUrl": endpoint, "headers": headers}},
		"CodeBuddy IDE":  {name: "calibreno", fields: object{"transportType": "streamable-http", "url": endpoint, "headers": headers, "disabled": false}},
		"CodeBuddy CLI":  {cmd: `codebuddy mcp add --scope user --transport http --header "Authorization: Bearer test-token" -- calibreno http://127.0.0.1:9876/mcp`},
		"Cherry Studio":  {name: "calibreno", fields: object{"type": "streamableHttp", "url": endpoint, "headers": headers}},
	}
	for _, snippet := range a.mcpSnippets() {
		want, ok := presets[snippet.Client]
		if !ok {
			t.Errorf("unexpected or repeated client %q", snippet.Client)
			continue
		}
		delete(presets, snippet.Client)
		t.Run(snippet.Client, func(t *testing.T) {
			if snippet.Hint == nil || snippet.Hint() == "" {
				t.Error("missing setup instructions")
			}
			if want.cmd != "" {
				if snippet.Text != want.cmd {
					t.Errorf("command = %q, want %q", snippet.Text, want.cmd)
				}
				return
			}
			var got object
			if err := json.Unmarshal([]byte(snippet.Text), &got); err != nil {
				t.Fatal(err)
			}
			expected := object{"mcpServers": object{want.name: want.fields}}
			if !reflect.DeepEqual(got, expected) {
				t.Errorf("configuration = %v, want %v", got, expected)
			}
		})
	}
	if len(presets) > 0 {
		t.Errorf("missing presets: %v", presets)
	}
}
