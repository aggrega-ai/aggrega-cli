// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
)

// toolSpec is one tool the server can run: its name, the rich natural-language
// description the client shows an assistant, the JSON Schema for its arguments,
// and the handler that runs it. A spec lives in one of two registries — the
// curated set advertised in every tools/list, or the deferred set surfaced only
// on demand through the capability tool — but both are invocable by name through
// the same dispatch. The registry order is the advertised order, so tools/list
// is byte-stable across runs.
//
// kinds records the entity kinds a tool serves, so the capability tool can
// narrow the long tail to the kind an author is working on. An empty kinds is
// kind-agnostic: the tool serves every entity kind (schema, scaffold, and
// diagnostic helpers are the natural agnostic tools). The field is exposure
// metadata only; it never changes what a tool does or returns.
type toolSpec struct {
	name        string
	description string
	inputSchema string
	kinds       []string
	invoke      func(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError)
}

// toolRegistry is the curated, always-loaded tool set the authoring loop lives
// in. Every tool wraps an existing single-sourced surface — the shared
// compiler-operations layer, the embedded schema set, or the catalog resolver —
// and none reimplements it. The capability tool is the one
// meta entry: it surfaces the deferred long tail on demand so the default set
// stays small. The order is fixed so the advertised list and its wire bytes are
// deterministic.
//
// It is assembled in init rather than a package-level literal because the
// capability tool's handler reads this very registry to surface the working set
// for a kind; a literal would close a self-reference the initializer analysis
// rejects, so the assignment is deferred to init where that reference is a
// runtime read.
var toolRegistry []toolSpec

func init() {
	toolRegistry = []toolSpec{
		validateToolSpec,
		buildToolSpec,
		describeToolSpec,
		getSchemaToolSpec,
		explainErrorToolSpec,
		scaffoldToolSpec,
		genCatalogToolSpec,
		describeCapabilitiesToolSpec,
	}
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
	responseFormatDetailed = compilerops.VerbosityDetailed
	responseFormatConcise  = compilerops.VerbosityConcise
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

// payload names in the corrective text a decode failure produces: what the
// surface that rejected the call calls the object it could not read. A tool
// reads an arguments object; a protocol method reads a params object.
const (
	payloadArguments = "arguments"
	payloadParams    = "params"
)

// invalidToolArguments builds the protocol error a tool returns when it cannot
// read its arguments object. The call never reached the operation, so there is
// no operation outcome to report as a tool-level failure; what the caller needs
// is the corrective text, which is why this shares one route with the
// request-params failure below.
func invalidToolArguments(tool string, err error) *rpcError {
	return &rpcError{Code: codeInvalidParams, Message: decodeFailureGuidance(tool, payloadArguments, err)}
}

// invalidRequestParams builds the protocol error a method returns when it cannot
// read its params object.
func invalidRequestParams(method string, err error) *rpcError {
	return &rpcError{Code: codeInvalidParams, Message: decodeFailureGuidance(method, payloadParams, err)}
}

// decodeFailureGuidance renders a failure to read a request payload into the
// corrective text a caller acts on: what the payload got wrong, and what to send
// instead. subject names the surface the payload was sent to and payload names
// what that surface calls it, so one route serves every decode failure on this
// server and the phrasing cannot drift between them.
//
// It reports the failure against the shape the caller authored — the payload's
// own field names and JSON types — and deliberately never against the internal
// shape the payload decodes into: an internal shape is not something a caller
// can see, correct, or look up, so naming it costs the caller the correction and
// costs the message its only purpose.
func decodeFailureGuidance(subject, payload string, err error) string {
	resend := fmt.Sprintf("Send the %s as a JSON object whose keys are the names %s declares, then call it again.",
		payload, subject)

	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return fmt.Sprintf("%s: the %s are not valid JSON (byte offset %d). %s",
			subject, payload, syntaxErr.Offset, resend)
	}

	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		if typeErr.Field != "" {
			if want := jsonTypeWord(typeErr.Type); want != "" {
				return fmt.Sprintf("%s: the %q field must be %s, but %s was sent. Correct that field and call %s again.",
					subject, typeErr.Field, want, articled(typeErr.Value), subject)
			}
			return fmt.Sprintf("%s: the %q field was sent as %s, which it does not accept. Correct that field and call %s again.",
				subject, typeErr.Field, articled(typeErr.Value), subject)
		}
		return fmt.Sprintf("%s: the %s must be a JSON object, but %s was sent. %s",
			subject, payload, articled(typeErr.Value), resend)
	}

	return fmt.Sprintf("%s: the %s could not be read. %s", subject, payload, resend)
}

// jsonTypeWord names the JSON type a decoded field accepts, in the vocabulary of
// the document the caller wrote rather than of the value it decodes into. An
// unnamed type yields the empty string, which the caller renders as a shapeless
// rejection instead of guessing.
func jsonTypeWord(t reflect.Type) string {
	if t == nil {
		return ""
	}
	switch t.Kind() {
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "a boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.Slice, reflect.Array:
		return "an array"
	case reflect.Map, reflect.Struct:
		return "an object"
	default:
		return ""
	}
}

// articled prefixes a JSON type name from the decoder with its article, so the
// rendered sentence reads as prose. An empty name yields a neutral noun phrase
// rather than a dangling article.
func articled(jsonType string) string {
	switch jsonType {
	case "":
		return "another value"
	case "array", "object":
		return "an " + jsonType
	default:
		return "a " + jsonType
	}
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
		return nil, invalidRequestParams("tools/call", err)
	}
	if p.Name == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "tools/call requires a tool name; available tools: " + toolNames()}
	}
	if spec, ok := lookupTool(p.Name); ok {
		return spec.invoke(s, context.Background(), p.Arguments)
	}
	return nil, &rpcError{Code: codeInvalidParams, Message: "unknown tool: " + p.Name +
		"; available tools: " + toolNames() +
		"; call " + describeCapabilitiesName + " to discover additional capabilities"}
}

// lookupTool resolves a tool by name across the curated set and the deferred
// set, so a deferred tool a client discovered through the capability tool is
// invocable by the same tools/call path as a curated one.
func lookupTool(name string) (*toolSpec, bool) {
	for i := range toolRegistry {
		if toolRegistry[i].name == name {
			return &toolRegistry[i], true
		}
	}
	for i := range deferredToolRegistry {
		if deferredToolRegistry[i].name == name {
			return &deferredToolRegistry[i], true
		}
	}
	return nil, false
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
