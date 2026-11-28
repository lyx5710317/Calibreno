package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"dalibre/internal/calibre"
)

// readOnly is the Host of a library opened by the stdio bridge while
// the app isn't running.
type readOnly struct{ lib *calibre.Library }

func (h readOnly) Library() *calibre.Library    { return h.lib }
func (readOnly) Changed(...int64)               {}
func (readOnly) Trash() func(path string) error { return nil }

// bearer adds the token to each request.
type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

// Bridge serves MCP on in and out, for clients that only start stdio
// servers. While the app serves endpoint it passes everything on to the
// app with token. An empty endpoint, or a port with no listener, lets
// the bridge serve the library in root read-only.
func Bridge(ctx context.Context, endpoint, token, root, version string, in io.ReadCloser, out io.WriteCloser) error {
	t := &sdk.IOTransport{Reader: in, Writer: out}
	if endpoint != "" {
		s, closeSession, err := proxy(ctx, endpoint, token, version)
		switch {
		case err == nil:
			defer closeSession()
			return s.Run(ctx, t)
		case !errors.Is(err, ErrNotRunning):
			return fmt.Errorf("connect to Calibreno’s MCP server: %w", err)
		}
	}
	var h readOnly
	if root != "" {
		lib, err := calibre.OpenReadOnly(root)
		if err != nil {
			return fmt.Errorf("open the saved Calibreno library read-only: %w", err)
		}
		defer lib.Close()
		h.lib = lib
	}
	return New(h, version, false).MCP().Run(ctx, t)
}

// proxy connects to the app's server and makes a server with its tools
// that calls them there, naming the agent it serves. The returned function
// ends the connection.
func proxy(ctx context.Context, endpoint, token, version string) (*sdk.Server, func(), error) {
	var (
		mu    sync.Mutex
		names []string
		srv   *sdk.Server
	)
	refresh := func(ctx context.Context, cs *sdk.ClientSession) error {
		mu.Lock()
		defer mu.Unlock()
		if srv == nil {
			return nil
		}
		var tools []*sdk.Tool
		for t, err := range cs.Tools(ctx, nil) {
			if err != nil {
				return err
			}
			tools = append(tools, t)
		}
		call := func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			p := &sdk.CallToolParams{Name: req.Params.Name, Arguments: req.Params.Arguments}
			if req.Session != nil {
				if ip := req.Session.InitializeParams(); ip != nil && ip.ClientInfo != nil {
					p.Meta = sdk.Meta{clientMeta: forward(ip.ClientInfo)}
				}
			}
			return cs.CallTool(ctx, p)
		}
		srv.RemoveTools(names...)
		names = names[:0]
		for _, t := range tools {
			srv.AddTool(t, call)
			names = append(names, t.Name)
		}
		return nil
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "dalibre-bridge", Version: version}, &sdk.ClientOptions{
		ToolListChangedHandler: func(_ context.Context, req *sdk.ToolListChangedRequest) {
			go func() {
				refreshCtx, cancel := context.WithTimeout(ctx, connectTimeout)
				defer cancel()
				_ = refresh(refreshCtx, req.Session)
			}()
		},
	})
	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	cs, err := connectApp(connectCtx, client, endpoint, token)
	if err != nil {
		return nil, nil, err
	}
	opts := &sdk.ServerOptions{Capabilities: &sdk.ServerCapabilities{Tools: &sdk.ToolCapabilities{ListChanged: true}}}
	if r := cs.InitializeResult(); r != nil {
		opts.Instructions = r.Instructions
	}
	mu.Lock()
	srv = sdk.NewServer(&sdk.Implementation{Name: implementationName, Title: "Calibreno", Version: version}, opts)
	mu.Unlock()
	if err := refresh(connectCtx, cs); err != nil {
		cs.Close()
		return nil, nil, fmt.Errorf("load MCP tools: %w", err)
	}
	return srv, func() { cs.Close() }, nil
}

// forward is the agent c as the bridge names it to the app: its name,
// title and the one icon the app would keep.
func forward(c *sdk.Implementation) clientInfo {
	ci := clientInfo{Name: c.Name, Title: c.Title}
	if _, _, i, ok := pickIcon(c.Icons); ok {
		ci.Icons = c.Icons[i : i+1]
	}
	return ci
}
