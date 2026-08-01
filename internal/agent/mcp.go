package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Server exposes the agent scope as an MCP server with 4 synchronous tools over
// streamable HTTP on a single /mcp endpoint.
type Server struct {
	scope *Scope
	hist  HistoryStore
	fwd   *Forwarder
	mcp   *server.MCPServer
}

// NewServer wires the tools. The caller mounts Handler() at exactly /mcp.
func NewServer(scope *Scope, hist HistoryStore, fwd *Forwarder) *Server {
	s := &Server{scope: scope, hist: hist, fwd: fwd}
	ms := server.NewMCPServer("gospy-agent", "1.0.0", server.WithToolCapabilities(true), server.WithInputSchemaValidation())

	ms.AddTool(mcp.NewTool("list_entries",
		mcp.WithDescription("Lists the entries currently visible to the agent: the agent gate must be enabled and the agent filter profile applies. Pagination is mandatory (max 200 entries per page)."),
		mcp.WithNumber("offset", mcp.DefaultNumber(0), mcp.Min(0)),
		mcp.WithNumber("limit", mcp.DefaultNumber(50), mcp.Min(1), mcp.Max(200)),
	), s.handleListEntries)

	ms.AddTool(mcp.NewTool("get_entry",
		mcp.WithDescription("Returns a single captured entry in full: headers are sanitized, bodies are included (hex dump for binary). Only entries in the agent-visible set can be read."),
		mcp.WithString("id", mcp.Required()),
	), s.handleGetEntry)

	ms.AddTool(mcp.NewTool("send_request",
		mcp.WithDescription("Sends a request through the gospy proxy using a captured entry as template. The template is REQUIRED and must be in the agent-visible set: the host (scheme+host+port) is fixed to the template's, and sensitive headers (Authorization, Cookie, ...) come exclusively from the captured request and are never exposed. Every omitted field is inherited from the template: method, path, query, headers and body. 'headers' only adds or replaces NON-sensitive headers (overriding a sensitive one is rejected). A 'body' string replaces the body with UTF-8 text (Content-Encoding is dropped); when omitted, the template's raw captured body is replayed byte-for-byte (binary and compressed included). The request is captured as agent-origin traffic and the active rules apply. Response headers are sanitized."),
		mcp.WithString("template", mcp.Required(), mcp.Description("ID of a captured entry in the agent-visible set to use as the request template.")),
		mcp.WithString("method", mcp.Description("HTTP method (uppercased). Omitted: the template's method.")),
		mcp.WithString("path", mcp.Description("Request path, e.g. /api/issues/search. Omitted: the template's path.")),
		mcp.WithString("query", mcp.Description("Query parameters as a URL query string, e.g. `a=1&b=2`. Omitted or empty: the template's query is inherited.")),
		mcp.WithString("headers", mcp.Description("Headers to add or replace as a JSON object, e.g. {\"X-Trace\": \"abc\"}. Sensitive headers cannot be overridden and always come from the template. Omitted: no changes.")),
		mcp.WithString("body", mcp.Description("Request body as UTF-8 text. Omitted: the template's raw captured body is replayed byte-for-byte.")),
	), s.handleSendRequest)

	ms.AddTool(mcp.NewTool("list_visible_hosts",
		mcp.WithDescription("Lists the distinct hosts currently in the agent-visible set, sorted."),
	), s.handleListVisibleHosts)

	s.mcp = ms
	return s
}

// Handler returns the streamable HTTP handler; mount it at exactly /mcp.
//
// The default session manager is used: it stays stateless (IDs are generated
// but never validated for existence) while still returning a Mcp-Session-Id on
// initialize, which session-aware clients like MCP Inspector require.
func (s *Server) Handler() http.Handler {
	return server.NewStreamableHTTPServer(s.mcp, server.WithEndpointPath("/mcp"))
}

func (s *Server) handleListEntries(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	page := s.scope.ListEntries(req.GetInt("offset", 0), req.GetInt("limit", 50))
	return mcp.NewToolResultJSON(page)
}

func (s *Server) handleGetEntry(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := req.GetString("id", "")
	if id == "" {
		return mcp.NewToolResultError("id is required"), nil
	}
	if !s.scope.IsVisible(id) {
		return mcp.NewToolResultErrorf("entry %s is not in the agent-visible set", id), nil
	}
	entry, err := s.hist.Get(id)
	if err != nil {
		return mcp.NewToolResultErrorf("entry %s not found", id), nil
	}
	return mcp.NewToolResultJSON(EntryDetail(s.hist.Dir(), entry))
}

func (s *Server) handleSendRequest(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !s.scope.GateEnabled() {
		return mcp.NewToolResultError("the agent gate is disabled"), nil
	}
	spec, err := parseRequestSpec(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	built, err := s.scope.buildTemplateRequest(spec)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp, callID, err := s.fwd.Do(ctx, built.method, built.url, built.headers, built.body)
	if err != nil {
		return mcp.NewToolResultErrorf("forward failed: %v", err), nil
	}
	resp.Request = ResolvedRequest{Method: built.method, URL: built.url, BodySource: built.bodySource}
	if le, err := s.hist.GetByAgentCallID(callID); err == nil {
		resp.EntryID = le.ID
	}
	return mcp.NewToolResultJSON(resp)
}

func (s *Server) handleListVisibleHosts(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	hosts := s.scope.VisibleHosts()
	return mcp.NewToolResultJSON(map[string]any{"hosts": hosts, "count": len(hosts)})
}

func parseRequestSpec(req mcp.CallToolRequest) (RequestSpec, error) {
	spec := RequestSpec{
		Template: req.GetString("template", ""),
		Method:   strings.ToUpper(req.GetString("method", "")),
		Path:     req.GetString("path", ""),
		Body:     req.GetString("body", ""),
	}
	if spec.Template == "" {
		return spec, fmt.Errorf("template is required: reference a captured entry in the agent-visible set")
	}
	var err error
	if spec.Headers, err = parseHeadersArg(req.GetArguments()); err != nil {
		return spec, fmt.Errorf("headers: %w", err)
	}
	if spec.Query, err = parseQueryArg(req.GetArguments()); err != nil {
		return spec, fmt.Errorf("query: %w", err)
	}
	return spec, nil
}

// parseQueryArg decodes the send_request query argument: a URL query string
// such as "a=1&b=2". An absent or empty value yields nil so the template's
// query is inherited unchanged.
func parseQueryArg(args map[string]any) (map[string][]string, error) {
	raw, ok := args["query"]
	if !ok {
		return nil, nil
	}
	s, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("must be a query string")
	}
	if s == "" {
		return nil, nil
	}
	return url.ParseQuery(s)
}

// parseHeadersArg decodes the send_request headers argument: a JSON object with
// string or string-array values. An absent or empty value yields nil so no
// headers are overridden.
func parseHeadersArg(args map[string]any) (map[string][]string, error) {
	raw, ok := args["headers"]
	if !ok {
		return nil, nil
	}
	s, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("must be a JSON string")
	}
	if s == "" {
		return nil, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	out := make(map[string][]string, len(m))
	for k, v := range m {
		var sval string
		if err := json.Unmarshal(v, &sval); err == nil {
			out[k] = []string{sval}
			continue
		}
		var aval []string
		if err := json.Unmarshal(v, &aval); err != nil {
			return nil, fmt.Errorf("value for %q must be a string or string array", k)
		}
		out[k] = aval
	}
	return out, nil
}
