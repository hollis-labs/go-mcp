package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
)

const ProtocolVersion = "2024-11-05"

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

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initializeResult struct {
	ProtocolVersion string           `json:"protocolVersion"`
	ServerInfo      serverInfo       `json:"serverInfo"`
	Capabilities    serverCapability `json:"capabilities"`
}

type serverCapability struct {
	Tools *toolsCapability `json:"tools,omitempty"`
}

type toolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

type toolDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema"`
}

type ToolDefinition = toolDef

type toolsListResult struct {
	Tools []toolDef `json:"tools"`
}

type toolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

type cancelledParams struct {
	RequestID json.RawMessage `json:"requestId"`
	Reason    string          `json:"reason,omitempty"`
}

type toolCallResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ToolHandler func(ctx context.Context, args map[string]interface{}) (string, error)

type Tool struct {
	Name        string
	Description string
	InputSchema interface{}
	Handler     ToolHandler
}

var ErrUnknownTool = fmt.Errorf("unknown tool")

type Server struct {
	name    string
	version string

	mu    sync.RWMutex
	tools map[string]Tool

	in io.Reader

	out     io.Writer
	writeMu sync.Mutex

	inFlightMu sync.Mutex
	inFlight   map[string]context.CancelFunc
	wg         sync.WaitGroup
}

func NewServer(name, version string) *Server {
	return &Server{
		name:     name,
		version:  version,
		tools:    make(map[string]Tool),
		in:       os.Stdin,
		out:      os.Stdout,
		inFlight: make(map[string]context.CancelFunc),
	}
}

func (s *Server) RegisterTool(t Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools[t.Name] = t
}

func (s *Server) Info() (name, version string) {
	return s.name, s.version
}

func (s *Server) ToolDefinitions() []ToolDefinition {
	s.mu.RLock()
	defs := make([]ToolDefinition, 0, len(s.tools))
	for _, t := range s.tools {
		defs = append(defs, ToolDefinition{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	s.mu.RUnlock()
	sort.Slice(defs, func(i, j int) bool {
		return defs[i].Name < defs[j].Name
	})
	return defs
}

func (s *Server) CallTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	s.mu.RLock()
	tool, ok := s.tools[name]
	s.mu.RUnlock()
	if !ok {
		return "", ErrUnknownTool
	}
	return tool.Handler(ctx, args)
}

func (s *Server) Run() error {
	sessionCtx, sessionCancel := context.WithCancel(context.Background())
	defer sessionCancel()
	defer s.cancelAllInFlight()
	defer s.wg.Wait()

	scanner := bufio.NewScanner(s.in)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.writeError(nil, -32700, "Parse error", err.Error())
			continue
		}

		s.handleRequest(sessionCtx, &req)
	}

	return scanner.Err()
}

func (s *Server) handleRequest(sessionCtx context.Context, req *jsonRPCRequest) {
	isNotification := req.ID == nil || string(req.ID) == "null"

	switch req.Method {
	case "initialize":
		if isNotification {
			return
		}
		s.writeResult(req.ID, initializeResult{
			ProtocolVersion: ProtocolVersion,
			ServerInfo: serverInfo{
				Name:    s.name,
				Version: s.version,
			},
			Capabilities: serverCapability{
				Tools: &toolsCapability{ListChanged: false},
			},
		})

	case "notifications/initialized":
	case "notifications/cancelled":
		s.handleCancelled(req)

	case "tools/list":
		if isNotification {
			return
		}
		s.writeResult(req.ID, toolsListResult{Tools: s.ToolDefinitions()})

	case "tools/call":
		if isNotification {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleToolCall(sessionCtx, req)
		}()

	default:
		if !isNotification {
			s.writeError(req.ID, -32601, "Method not found", req.Method)
		}
	}
}

func (s *Server) handleCancelled(req *jsonRPCRequest) {
	var params cancelledParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return
	}
	if key := requestIDKey(params.RequestID); key != "" {
		s.cancelRequest(key)
	}
}

func (s *Server) handleToolCall(sessionCtx context.Context, req *jsonRPCRequest) {
	var params toolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.writeError(req.ID, -32602, "Invalid params", err.Error())
		return
	}

	ctx, cancel := context.WithCancel(sessionCtx)
	requestKey := requestIDKey(req.ID)
	if requestKey != "" {
		s.trackRequest(requestKey, cancel)
		defer s.finishRequest(requestKey)
	} else {
		defer cancel()
	}

	text, err := s.CallTool(ctx, params.Name, params.Arguments)
	if err == ErrUnknownTool {
		s.writeResult(req.ID, toolCallResult{
			Content: []contentBlock{{Type: "text", Text: fmt.Sprintf("unknown tool: %s", params.Name)}},
			IsError: true,
		})
		return
	}
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		s.writeResult(req.ID, toolCallResult{
			Content: []contentBlock{{Type: "text", Text: err.Error()}},
			IsError: true,
		})
		return
	}

	s.writeResult(req.ID, toolCallResult{
		Content: []contentBlock{{Type: "text", Text: text}},
	})
}

func requestIDKey(id json.RawMessage) string {
	if len(id) == 0 {
		return ""
	}
	return string(id)
}

func (s *Server) trackRequest(id string, cancel context.CancelFunc) {
	s.inFlightMu.Lock()
	defer s.inFlightMu.Unlock()
	s.inFlight[id] = cancel
}

func (s *Server) finishRequest(id string) {
	s.inFlightMu.Lock()
	cancel, ok := s.inFlight[id]
	if ok {
		delete(s.inFlight, id)
	}
	s.inFlightMu.Unlock()
	if ok {
		cancel()
	}
}

func (s *Server) cancelRequest(id string) {
	s.inFlightMu.Lock()
	cancel, ok := s.inFlight[id]
	s.inFlightMu.Unlock()
	if ok {
		cancel()
	}
}

func (s *Server) cancelAllInFlight() {
	s.inFlightMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.inFlight))
	for id, cancel := range s.inFlight {
		cancels = append(cancels, cancel)
		delete(s.inFlight, id)
	}
	s.inFlightMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (s *Server) writeResult(id json.RawMessage, result interface{}) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	s.writeJSON(resp)
}

func (s *Server) writeError(id json.RawMessage, code int, message, data string) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &rpcError{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}
	s.writeJSON(resp)
}

func (s *Server) writeJSON(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	data = append(data, '\n')
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, _ = s.out.Write(data)
}
