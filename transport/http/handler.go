package httptransport

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"

	gmcp "github.com/hollis-labs/go-mcp/server"
)

const (
	defaultProtocolVersion = "2025-03-26"
	headerMismatchCode     = -32001
)

var supportedProtocolVersions = []string{
	defaultProtocolVersion,
	gmcp.ProtocolVersion,
}

type HandlerOptions struct {
	AllowedOrigins []string
}

type Handler struct {
	server         *gmcp.Server
	allowedOrigins map[string]struct{}
}

func NewHandler(server *gmcp.Server, opts HandlerOptions) *Handler {
	allowed := make(map[string]struct{}, len(opts.AllowedOrigins))
	for _, origin := range opts.AllowedOrigins {
		if origin == "" {
			continue
		}
		allowed[strings.ToLower(origin)] = struct{}{}
	}
	return &Handler{server: server, allowedOrigins: allowed}
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

type discoverParams struct {
	Meta map[string]interface{} `json:"_meta,omitempty"`
}

type toolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

type initializeResult struct {
	ProtocolVersion string           `json:"protocolVersion"`
	ServerInfo      serverInfo       `json:"serverInfo"`
	Capabilities    serverCapability `json:"capabilities"`
}

type discoverResult struct {
	ResultType        string           `json:"resultType"`
	SupportedVersions []string         `json:"supportedVersions"`
	Capabilities      serverCapability `json:"capabilities"`
	ServerInfo        serverInfo       `json:"serverInfo"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type serverCapability struct {
	Tools *toolsCapability `json:"tools,omitempty"`
}

type toolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

type toolsListResult struct {
	Tools []gmcp.ToolDefinition `json:"tools"`
}

type toolCallResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		h.handlePreflight(w, r)
		return
	}
	if !h.originAllowed(r) {
		writeHTTPError(w, http.StatusForbidden, nil, -32000, "forbidden origin")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", strings.Join([]string{http.MethodOptions, http.MethodPost}, ", "))
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if !acceptAllowsMCP(r.Header.Get("Accept")) {
		writeHTTPError(w, http.StatusNotAcceptable, nil, -32000, "Accept must include application/json")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024))
	if err != nil {
		writeHTTPError(w, http.StatusBadRequest, nil, -32700, "read body")
		return
	}
	defer r.Body.Close()

	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeHTTPError(w, http.StatusBadRequest, nil, -32700, "parse error")
		return
	}
	if err := validateHeaders(r, req); err != nil {
		writeHTTPError(w, http.StatusBadRequest, req.ID, headerMismatchCode, err.Error())
		return
	}

	isNotification := req.ID == nil || string(req.ID) == "null"
	switch req.Method {
	case "notifications/initialized", "notifications/cancelled":
		w.WriteHeader(http.StatusAccepted)
		return
	case "server/discover":
		if isNotification {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		h.handleDiscover(w, r, req)
	case "initialize":
		if isNotification {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		h.handleInitialize(w, r, req)
	case "tools/list":
		if isNotification {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		h.handleToolsList(w, r, req)
	case "tools/call":
		if isNotification {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		h.handleToolCall(r.Context(), w, r, req)
	default:
		writeHTTPError(w, http.StatusNotFound, req.ID, -32601, "method not found")
	}
}

func (h *Handler) originAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" || len(h.allowedOrigins) == 0 {
		return true
	}
	_, ok := h.allowedOrigins[strings.ToLower(origin)]
	return ok
}

func (h *Handler) handlePreflight(w http.ResponseWriter, r *http.Request) {
	if !h.originAllowed(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
	}
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, MCP-Protocol-Version, Mcp-Method, Mcp-Name, Authorization")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleDiscover(w http.ResponseWriter, r *http.Request, req jsonRPCRequest) {
	name, serverVersion := h.server.Info()
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: discoverResult{
			ResultType:        "complete",
			SupportedVersions: slices.Clone(supportedProtocolVersions),
			Capabilities: serverCapability{
				Tools: &toolsCapability{ListChanged: false},
			},
			ServerInfo: serverInfo{Name: name, Version: serverVersion},
		},
	}
	writeProtocolResponse(w, r, http.StatusOK, resp)
}

func (h *Handler) handleInitialize(w http.ResponseWriter, r *http.Request, req jsonRPCRequest) {
	var params initializeParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeHTTPError(w, http.StatusBadRequest, req.ID, -32602, "invalid params")
			return
		}
	}
	version := params.ProtocolVersion
	if version == "" {
		version = defaultProtocolVersion
	}
	if !slices.Contains(supportedProtocolVersions, version) {
		writeHTTPError(w, http.StatusBadRequest, req.ID, -32000, unsupportedProtocolMessage(version))
		return
	}
	name, serverVersion := h.server.Info()
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: initializeResult{
			ProtocolVersion: version,
			ServerInfo:      serverInfo{Name: name, Version: serverVersion},
			Capabilities: serverCapability{
				Tools: &toolsCapability{ListChanged: false},
			},
		},
	}
	writeProtocolResponse(w, r, http.StatusOK, resp)
}

func (h *Handler) handleToolsList(w http.ResponseWriter, r *http.Request, req jsonRPCRequest) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  toolsListResult{Tools: h.server.ToolDefinitions()},
	}
	writeProtocolResponse(w, r, http.StatusOK, resp)
}

func (h *Handler) handleToolCall(ctx context.Context, w http.ResponseWriter, r *http.Request, req jsonRPCRequest) {
	var params toolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeHTTPError(w, http.StatusBadRequest, req.ID, -32602, "invalid params")
		return
	}
	if prefersEventStream(r.Header.Get("Accept")) {
		h.handleToolCallSSE(ctx, w, req, params)
		return
	}

	text, err := h.server.CallTool(ctx, params.Name, params.Arguments)
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		result := toolCallResult{
			Content: []contentBlock{{Type: "text", Text: toolErrorText(params.Name, err)}},
			IsError: true,
		}
		writeProtocolResponse(w, r, http.StatusOK, jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
		return
	}

	result := toolCallResult{
		Content: []contentBlock{{Type: "text", Text: text}},
	}
	writeProtocolResponse(w, r, http.StatusOK, jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
}

func (h *Handler) handleToolCallSSE(ctx context.Context, w http.ResponseWriter, req jsonRPCRequest, params toolCallParams) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.handleToolCall(ctx, w, &http.Request{Header: http.Header{"Accept": []string{"application/json"}}}, req)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	bw := bufio.NewWriter(w)
	var writeMu sync.Mutex
	writeEvent := func(event string, data interface{}) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		payload, err := json.Marshal(data)
		if err != nil {
			return err
		}
		if _, err := bw.WriteString("event: " + event + "\n"); err != nil {
			return err
		}
		if _, err := bw.WriteString("data: "); err != nil {
			return err
		}
		if _, err := bw.Write(payload); err != nil {
			return err
		}
		if _, err := bw.WriteString("\n\n"); err != nil {
			return err
		}
		if err := bw.Flush(); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	ctx = gmcp.WithNotifier(ctx, func(n gmcp.Notification) {
		_ = writeEvent("message", n)
	})
	text, err := h.server.CallTool(ctx, params.Name, params.Arguments)
	if ctx.Err() != nil {
		return
	}
	resp := jsonRPCResponse{JSONRPC: "2.0", ID: req.ID}
	if err != nil {
		resp.Result = toolCallResult{
			Content: []contentBlock{{Type: "text", Text: toolErrorText(params.Name, err)}},
			IsError: true,
		}
	} else {
		resp.Result = toolCallResult{
			Content: []contentBlock{{Type: "text", Text: text}},
		}
	}
	_ = writeEvent("message", resp)
}

func toolErrorText(name string, err error) string {
	if errors.Is(err, gmcp.ErrUnknownTool) {
		return fmt.Sprintf("unknown tool: %s", name)
	}
	return err.Error()
}

func writeHTTPError(w http.ResponseWriter, status int, id json.RawMessage, code int, message string) {
	writeJSON(w, status, jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &rpcError{
			Code:    code,
			Message: message,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeProtocolResponse(w http.ResponseWriter, r *http.Request, status int, v interface{}) {
	if prefersEventStream(r.Header.Get("Accept")) {
		writeSSE(w, status, v)
		return
	}
	writeJSON(w, status, v)
}

func writeSSE(w http.ResponseWriter, status int, v interface{}) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, status, v)
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      nil,
			Error: &rpcError{
				Code:    -32603,
				Message: "internal error",
			},
		})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(status)

	bw := bufio.NewWriter(w)
	_, _ = bw.WriteString("event: message\n")
	_, _ = bw.WriteString("data: ")
	_, _ = bw.Write(data)
	_, _ = bw.WriteString("\n\n")
	_ = bw.Flush()
	flusher.Flush()
}

func acceptAllowsMCP(accept string) bool {
	if strings.TrimSpace(accept) == "" {
		return true
	}
	for _, part := range strings.Split(accept, ",") {
		mediaType := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if mediaType == "*/*" || mediaType == "application/json" || mediaType == "text/event-stream" {
			return true
		}
	}
	return false
}

func prefersEventStream(accept string) bool {
	for _, part := range strings.Split(accept, ",") {
		mediaType := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if mediaType == "text/event-stream" {
			return true
		}
	}
	return false
}

func validateHeaders(r *http.Request, req jsonRPCRequest) error {
	if err := validateProtocolVersionHeader(r, req); err != nil {
		return err
	}
	if err := validateMethodHeader(r, req); err != nil {
		return err
	}
	return validateNameHeader(r, req)
}

func validateProtocolVersionHeader(r *http.Request, req jsonRPCRequest) error {
	switch req.Method {
	case "server/discover":
		return nil
	}
	headerVersion := strings.TrimSpace(r.Header.Get("MCP-Protocol-Version"))
	if headerVersion == "" {
		return nil
	}
	if !slices.Contains(supportedProtocolVersions, headerVersion) {
		return fmt.Errorf("unsupported protocol version %q; supported versions: %s", headerVersion, strings.Join(supportedProtocolVersions, ", "))
	}
	if req.Method == "initialize" {
		var params initializeParams
		if len(req.Params) == 0 {
			return nil
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return fmt.Errorf("invalid initialize params")
		}
		if params.ProtocolVersion != "" && params.ProtocolVersion != headerVersion {
			return fmt.Errorf("header mismatch: MCP-Protocol-Version %q does not match body value %q", headerVersion, params.ProtocolVersion)
		}
	}
	return nil
}

func validateMethodHeader(r *http.Request, req jsonRPCRequest) error {
	headerMethod := strings.TrimSpace(r.Header.Get("Mcp-Method"))
	if headerMethod == "" {
		return nil
	}
	if headerMethod != req.Method {
		return fmt.Errorf("header mismatch: Mcp-Method %q does not match body value %q", headerMethod, req.Method)
	}
	return nil
}

func validateNameHeader(r *http.Request, req jsonRPCRequest) error {
	headerName := strings.TrimSpace(r.Header.Get("Mcp-Name"))
	if headerName == "" {
		return nil
	}
	if req.Method != "tools/call" {
		return fmt.Errorf("header mismatch: Mcp-Name is only valid for named requests this server supports")
	}
	var params toolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return fmt.Errorf("invalid params")
	}
	if params.Name != headerName {
		return fmt.Errorf("header mismatch: Mcp-Name %q does not match body value %q", headerName, params.Name)
	}
	return nil
}

func unsupportedProtocolMessage(version string) string {
	return fmt.Sprintf("unsupported protocol version %q; supported versions: %s", version, strings.Join(supportedProtocolVersions, ", "))
}
