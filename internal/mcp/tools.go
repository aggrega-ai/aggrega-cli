// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"strings"
)

// toolSpec is one curated tool the server always advertises: its name, the
// rich natural-language description the client shows an assistant, the JSON
// Schema for its arguments, and the handler that runs it. The registry order is
// the advertised order, so tools/list is byte-stable across runs.
type toolSpec struct {
	name        string
	description string
	inputSchema string
	invoke      func(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError)
}

// toolRegistry is the curated, always-loaded tool set the authoring loop lives
// in. Every tool wraps an existing single-sourced surface — the shared
// compiler-operations layer, the embedded schema set, the catalog resolver, or
// the search socket — and none reimplements it. The order is fixed so the
// advertised list and its wire bytes are deterministic.
var toolRegistry = []toolSpec{
	validateToolSpec,
	buildToolSpec,
	describeToolSpec,
	getSchemaToolSpec,
	searchToolSpec,
	explainErrorToolSpec,
	scaffoldToolSpec,
	genCatalogToolSpec,
}

// toolTextContent is one text block in a tool result's content array. The
// protocol lets a result carry several typed content blocks; every tool here
// emits a single human-readable text block alongside the machine-readable
// structuredContent, so an assistant can read either.
type toolTextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// callToolResult is the tools/call result envelope. Content carries the
// human-readable rendering; StructuredContent carries the same result as a
// machine-parseable object when the tool has one; IsError marks a tool-level
// failure — the call was well-formed but the operation did not succeed — as
// distinct from a protocol-level error, which travels as a JSON-RPC error
// envelope instead. A tool-level failure still carries corrective guidance in
// its text block so the assistant can act on it rather than on an opaque code.
type callToolResult struct {
	Content           []toolTextContent `json:"content"`
	StructuredContent json.RawMessage   `json:"structuredContent,omitempty"`
	IsError           bool              `json:"isError,omitempty"`
}

// response format tokens select a verbose or terse rendering where a tool has a
// meaningful choice between the two. The detailed form is the default: an
// assistant new to the workspace benefits from the fuller result, and a terse
// form is an explicit opt-in.
const (
	responseFormatDetailed = "detailed"
	responseFormatConcise  = "concise"
)

// parseResponseFormat maps a client-supplied response_format value to one of the
// two supported tokens, defaulting to the detailed form for any absent or
// unrecognized value.
func parseResponseFormat(v string) string {
	if strings.EqualFold(strings.TrimSpace(v), responseFormatConcise) {
		return responseFormatConcise
	}
	return responseFormatDetailed
}

// textResult builds a successful tool result carrying a human-readable text
// block and the machine-readable structured payload. A nil structured payload
// omits structuredContent from the wire.
func textResult(text string, structured any) (*callToolResult, *rpcError) {
	res := &callToolResult{Content: []toolTextContent{{Type: "text", Text: text}}}
	if structured != nil {
		b, err := json.Marshal(structured)
		if err != nil {
			return nil, &rpcError{Code: codeInternalError, Message: "marshal tool result: " + err.Error()}
		}
		res.StructuredContent = b
	}
	return res, nil
}

// errorResult builds a tool-level failure result. The guidance text is the
// actionable message an assistant reads to correct the call; the result is
// marked IsError so the client renders it as a failure rather than a success.
func errorResult(guidance string) (*callToolResult, *rpcError) {
	return &callToolResult{
		Content: []toolTextContent{{Type: "text", Text: guidance}},
		IsError: true,
	}, nil
}

// handleToolsList answers tools/list with the curated set: each tool's name,
// its rich description, and the JSON Schema for its arguments, in registry
// order.
func (s *server) handleToolsList() (any, *rpcError) {
	tools := make([]tool, 0, len(toolRegistry))
	for i := range toolRegistry {
		tools = append(tools, tool{
			Name:        toolRegistry[i].name,
			Description: toolRegistry[i].description,
			InputSchema: json.RawMessage(toolRegistry[i].inputSchema),
		})
	}
	return listToolsResult{Tools: tools}, nil
}

// handleToolsCall dispatches a tools/call request to the named tool. A missing
// or unknown tool name is an invalid-params protocol error naming the curated
// set so the caller can correct it; a well-formed call to a known tool runs the
// tool, whose own outcome — success or a guidance-carrying failure — is
// returned as the result.
func (s *server) handleToolsCall(params json.RawMessage) (any, *rpcError) {
	var p callToolParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid tools/call params: " + err.Error()}
	}
	if p.Name == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "tools/call requires a tool name; available tools: " + toolNames()}
	}
	for i := range toolRegistry {
		if toolRegistry[i].name == p.Name {
			return toolRegistry[i].invoke(s, context.Background(), p.Arguments)
		}
	}
	return nil, &rpcError{Code: codeInvalidParams, Message: "unknown tool: " + p.Name + "; available tools: " + toolNames()}
}

// toolNames renders the curated tool names as a comma-separated list for an
// error that must point the caller at the valid set.
func toolNames() string {
	names := make([]string, 0, len(toolRegistry))
	for i := range toolRegistry {
		names = append(names, toolRegistry[i].name)
	}
	return strings.Join(names, ", ")
}
