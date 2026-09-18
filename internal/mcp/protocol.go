// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import "encoding/json"

// The two Model Context Protocol revisions this server speaks, each named
// for the shape of the exchange it defines rather than for its place in a
// sequence.
//
// perRequestRevision carries no connection-level negotiation at all: every
// request states the revision it speaks and the capabilities its sender
// offers, every result carries its type and the server's identity, and
// server/discover advertises what the server supports.
//
// handshakeRevision settles those same facts once, in an initialize
// exchange, and a client selects it by opening with that handshake. It is
// spoken for the deprecation window the protocol's own lifecycle policy
// grants, so a client that has not moved still connects.
const (
	perRequestRevision = "2026-07-28"
	handshakeRevision  = "2025-06-18"
)

// serverName is the implementation identity reported to the client in the
// initialize handshake's serverInfo block.
const serverName = "aggrega"

// JSON-RPC 2.0 and Model Context Protocol error codes. The first block is
// the standard JSON-RPC range.
//
// resourceNotFound is the code the read surface returns for an unknown
// resource URI to a client speaking the handshake revision. The 2026-07-28
// revision retired it and answers that condition with invalidParams instead,
// so it is kept both because one revision still emits it and because that
// revision reserves it against reuse; which of the two a client sees is
// settled in one place, where the read surface refuses.
//
// unsupportedProtocolVersion is allocated by the 2026-07-28 revision, which
// also fixes the payload it carries: the revisions the server does speak and
// the one the request asked for.
//
// headerMismatch is allocated by that same revision and reports a request whose
// transport headers do not mirror its body. Only the HTTP transport can raise
// it: the mirror headers exist so an intermediary can route a message without
// parsing it, and the stdio transport has no headers at all.
//
// The third code that revision allocates is deliberately not declared. It
// reports a client capability the server required and the request did not
// offer, and nothing here requires one; it is transport-independent, so no
// transport added later owns it either.
const (
	codeParseError                 = -32700
	codeInvalidRequest             = -32600
	codeMethodNotFound             = -32601
	codeInvalidParams              = -32602
	codeInternalError              = -32603
	codeResourceNotFound           = -32002
	codeHeaderMismatch             = -32020
	codeUnsupportedProtocolVersion = -32022
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

// cancelledParams is the params shape for the notifications/cancelled
// notification: the id of the request the client is cancelling. The protocol
// also allows an optional free-text reason, which is not declared here because
// nothing reads it — an undeclared field is simply ignored when the params are
// read, so a client that sends one is still understood.
//
// RequestID is captured as raw JSON — not decoded to a concrete number or
// string — so it reaches the cancel registry through the same canonicalization
// the in-flight request's own id took, with no decode-then-re-encode step that
// could perturb the bytes.
//
// It is deliberately a second wire shape for the concept the other
// request-serving frontend's cancel params also carry, and not a copy of a
// mechanic. The two protocols spell the field differently — requestId here, id
// there — and one struct answering to both wires would accept each protocol's
// field name on the other's surface, which is a wider contract than either
// states. What is single-sourced is the mechanic underneath: the registry the id
// resolves against, and the key rule that resolves it, live in
// internal/requestcancel and both frontends route into them.
type cancelledParams struct {
	RequestID json.RawMessage `json:"requestId"`
}

// rpcError carries the JSON-RPC 2.0 error envelope. Data is reserved for
// structured detail a future method may attach; omitempty drops it from
// the wire while it is nil.
//
// The two unexported fields are what a refusal asks the HTTP transport to carry
// beyond the envelope, for the refusals whose status and challenge are a
// property of the refusal rather than of its code. They travel no further than
// this process: encoding ignores an unexported field, so neither reaches a
// caller as anything but the status line and the header the transport writes
// from them. The stdio transport reads neither, which is correct rather than an
// omission — it has no status line and no headers, and no refusal it composes
// sets one.
//
// They are unexported for the same reason the authenticator seam is: there is no
// route by which a caller outside this package could compose a refusal that
// claims a status the transport did not decide on.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`

	// httpStatus is the status this refusal is carried under, where that is not
	// the one the code maps to. Zero leaves the mapping to decide.
	httpStatus int

	// insufficientScope is the one scope that would have admitted a call this
	// server refused for want of one, and is what the challenge accompanying
	// that refusal names. Empty on every other refusal, including one for a
	// class no scope admits.
	insufficientScope string
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
// negotiated protocol revision, the capabilities it advertises, its
// implementation identity, and the guidance a client may surface to the model.
//
// Instructions carries that guidance — the mental model an assistant needs
// before its first call, which it cannot recover from tool schemas alone. The
// protocol makes the field optional, so it is omitted from the wire when empty.
type initializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    serverCapabilities `json:"capabilities"`
	ServerInfo      implementation     `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
}

// implementation names a protocol participant — the client in
// initializeParams, the server in initializeResult.
type implementation struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// identity is this server's own implementation identity. Both revisions
// report it — the handshake in its serverInfo block, the per-request
// revision under the reserved metadata key on every result — so the two
// read it from here rather than each composing one.
func (s *server) identity() implementation {
	return implementation{Name: serverName, Version: s.serverVersion}
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

// serverCapabilitySet is the one statement of what this server advertises.
// The initialize handshake and server/discover both answer from here, so the
// two revisions cannot come to advertise different surfaces.
func serverCapabilitySet() serverCapabilities {
	return serverCapabilities{
		Tools:     &toolsCapability{ListChanged: false},
		Resources: &resourcesCapability{Subscribe: false, ListChanged: false},
		Prompts:   &promptsCapability{ListChanged: false},
	}
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

// listToolsResult is the tools/list result envelope. The per-request revision
// makes it a result a client may cache, so it carries that revision's envelope
// and its freshness hint; both encode to nothing for a request in the
// handshake revision, which defines neither.
type listToolsResult struct {
	perRequestEnvelope
	cacheHint
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

// listResourcesResult is the resources/list result envelope. It is cacheable
// in the per-request revision on the same terms as the other list results.
type listResourcesResult struct {
	perRequestEnvelope
	cacheHint
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
// envelope. It is cacheable in the per-request revision on the same terms as
// the other list results.
type listResourceTemplatesResult struct {
	perRequestEnvelope
	cacheHint
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

// listPromptsResult is the prompts/list result envelope. It is cacheable in
// the per-request revision on the same terms as the other list results.
type listPromptsResult struct {
	perRequestEnvelope
	cacheHint
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
//
// It carries the per-request revision's envelope and no freshness hint. That
// revision declares a rendered prompt an ordinary result rather than a
// cacheable one, so there is no window for it to state.
type getPromptResult struct {
	perRequestEnvelope
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
//
// It is cacheable in the per-request revision, and it is the one result whose
// freshness is not fixed by the method: the read surface serves several
// families out of different places, so the hint is chosen where the URI's
// family is resolved rather than here.
type readResourceResult struct {
	perRequestEnvelope
	cacheHint
	Contents []resourceContents `json:"contents"`
}
