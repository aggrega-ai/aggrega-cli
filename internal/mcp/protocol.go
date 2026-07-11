// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import "encoding/json"

// protocolVersion is the Model Context Protocol revision this server
// implements. The initialize handshake echoes it when the client
// requests a supported revision, and otherwise reports it as the
// revision the client should adopt.
const protocolVersion = "2025-06-18"

// serverName is the implementation identity reported to the client in the
// initialize handshake's serverInfo block.
const serverName = "aggrega"

// JSON-RPC 2.0 and Model Context Protocol error codes. The first block is
// the standard JSON-RPC range; resourceNotFound is the protocol-specific
// code the read surface returns for an unknown resource URI.
const (
	codeParseError       = -32700
	codeInvalidRequest   = -32600
	codeMethodNotFound   = -32601
	codeInvalidParams    = -32602
	codeInternalError    = -32603
	codeResourceNotFound = -32002
)

// rpcRequest is the wire shape for an incoming JSON-RPC 2.0 message. An
// absent id field (nil ID) marks a notification, which yields no
// response; a present id marks a request the server must answer.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcResponse is the wire shape for an outgoing JSON-RPC 2.0 response.
// Exactly one of Result or Error is populated; the json.RawMessage
// result encoding supports every method's payload without distinct
// response types.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError carries the JSON-RPC 2.0 error envelope. Data is reserved for
// structured detail a future method may attach; omitempty drops it from
// the wire while it is nil.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// initializeParams is the client's half of the initialize handshake: the
// protocol revision it speaks, the capabilities it offers, and its
// implementation identity.
type initializeParams struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    json.RawMessage `json:"capabilities,omitempty"`
	ClientInfo      implementation  `json:"clientInfo"`
}

// initializeResult is the server's half of the initialize handshake: the
// negotiated protocol revision, the capabilities it advertises, and its
// implementation identity.
type initializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    serverCapabilities `json:"capabilities"`
	ServerInfo      implementation     `json:"serverInfo"`
}

// implementation names a protocol participant — the client in
// initializeParams, the server in initializeResult.
type implementation struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// serverCapabilities advertises the feature groups the server supports.
// Each group is present when the server serves it; the list and dispatch
// surfaces for tools, resources, and prompts are all present even while
// their bodies return empty results.
type serverCapabilities struct {
	Tools     *toolsCapability     `json:"tools,omitempty"`
	Resources *resourcesCapability `json:"resources,omitempty"`
	Prompts   *promptsCapability   `json:"prompts,omitempty"`
}

// toolsCapability advertises the tools surface. ListChanged reports
// whether the server emits a list-changed notification when its tool set
// changes; the server serves a fixed set, so it is false.
type toolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

// resourcesCapability advertises the resources surface. Subscribe reports
// whether the server supports per-resource change subscriptions;
// ListChanged reports whether it emits a list-changed notification. The
// server serves a fixed set without subscriptions, so both are false.
type resourcesCapability struct {
	Subscribe   bool `json:"subscribe"`
	ListChanged bool `json:"listChanged"`
}

// promptsCapability advertises the prompts surface. ListChanged reports
// whether the server emits a list-changed notification when its prompt
// set changes; the server serves a fixed set, so it is false.
type promptsCapability struct {
	ListChanged bool `json:"listChanged"`
}

// tool is a single entry in the tools/list result — the invocable
// operation an assistant can call, named with a JSON Schema describing
// its arguments.
type tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// listToolsResult is the tools/list result envelope.
type listToolsResult struct {
	Tools []tool `json:"tools"`
}

// resource is a single entry in the resources/list result — a concrete,
// readable item addressed by URI.
type resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// listResourcesResult is the resources/list result envelope.
type listResourcesResult struct {
	Resources []resource `json:"resources"`
}

// resourceTemplate is a single entry in the resources/templates/list
// result — a URI-template pattern that expands into a family of readable
// resources.
type resourceTemplate struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// listResourceTemplatesResult is the resources/templates/list result
// envelope.
type listResourceTemplatesResult struct {
	ResourceTemplates []resourceTemplate `json:"resourceTemplates"`
}

// prompt is a single entry in the prompts/list result — a named,
// parameterized prompt template an assistant can request.
type prompt struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []promptArgument `json:"arguments,omitempty"`
}

// promptArgument describes one argument a prompt template accepts.
type promptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// listPromptsResult is the prompts/list result envelope.
type listPromptsResult struct {
	Prompts []prompt `json:"prompts"`
}

// getPromptParams is the prompts/get request params: the prompt to render and
// the argument values that parameterize it.
type getPromptParams struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

// promptContent is one content block in a rendered prompt message. Every prompt
// here emits text blocks whose bodies are pointers into the resource and
// tool surfaces, so no schema or documentation prose is restated inline.
type promptContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// promptMessage is one message in a rendered prompt: the role it is spoken in
// and its content block.
type promptMessage struct {
	Role    string        `json:"role"`
	Content promptContent `json:"content"`
}

// getPromptResult is the prompts/get result envelope: a short description of the
// rendered prompt and the ordered messages that seed the authoring conversation.
type getPromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []promptMessage `json:"messages"`
}

// callToolParams is the tools/call request params: the tool to invoke and
// its arguments.
type callToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// readResourceParams is the resources/read request params: the URI of the
// resource to read.
type readResourceParams struct {
	URI string `json:"uri"`
}

// resourceContents is one item of a resources/read result — the content of a
// read resource. Text carries a textual resource's body inline; MimeType names
// its media type so the client renders it correctly.
type resourceContents struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
}

// readResourceResult is the resources/read result envelope: the contents of
// the requested resource, served inline.
type readResourceResult struct {
	Contents []resourceContents `json:"contents"`
}
