package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrNotRunning is Dial's error when nothing serves the endpoint: Dalibre
// isn't running, or its MCP server is off.
var ErrNotRunning = errors.New("Calibreno’s MCP server isn’t running")

const connectTimeout = 5 * time.Second

// Dial connects to the app's MCP server at endpoint with token, as the
// client name, for tools such as dalibre-plugin that drive the running
// app.
func Dial(ctx context.Context, endpoint, token, name, version string) (*sdk.ClientSession, error) {
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	client := sdk.NewClient(&sdk.Implementation{Name: name, Version: version}, nil)
	return connectApp(ctx, client, endpoint, token)
}

func connectApp(ctx context.Context, client *sdk.Client, endpoint, token string) (*sdk.ClientSession, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || !localHost(u.Host, "") || u.Path != Path || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid local Calibreno MCP endpoint")
	}
	probe := net.Dialer{Timeout: time.Second}
	conn, err := probe.DialContext(ctx, "tcp", u.Host)
	if isConnectionRefused(err) {
		return nil, ErrNotRunning
	}
	if err != nil {
		return nil, fmt.Errorf("reach Calibreno’s MCP endpoint: %w", err)
	}
	conn.Close()
	if token == "" {
		return nil, errors.New("Calibreno’s MCP token is missing. Open Settings › MCP to create one")
	}
	cs, err := client.Connect(ctx, &sdk.StreamableClientTransport{
		Endpoint: endpoint,
		HTTPClient: &http.Client{
			Transport: bearer{token, http.DefaultTransport},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil)
	if err != nil {
		return nil, err
	}
	if info := cs.InitializeResult().ServerInfo; info == nil || info.Name != implementationName {
		cs.Close()
		return nil, errors.New("the port is serving a different MCP server")
	}
	return cs, nil
}

// CallTool calls the tool name with args and decodes its structured result
// into out (when not nil). A tool that fails returns its text as the
// error.
func CallTool(ctx context.Context, cs *sdk.ClientSession, name string, args, out any) error {
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return err
	}
	if res.IsError {
		for _, c := range res.Content {
			if t, ok := c.(*sdk.TextContent); ok {
				return errors.New(t.Text)
			}
		}
		return errors.New(name + " failed")
	}
	if out == nil || res.StructuredContent == nil {
		return nil
	}
	data, err := json.Marshal(res.StructuredContent)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}
