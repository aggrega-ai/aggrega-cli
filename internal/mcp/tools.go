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
	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// toolSpec is one tool the server can run: its name, the rich natural-language
// description the client shows an assistant, the JSON Schema for its arguments,
// and the handler that runs it. A spec lives in one of two registries — the
// curated set advertised in every tools/list, or the deferred set surfaced only
// on demand through the capability tool — but both are invocable by name through
// the same dispatch. The registry order is the advertised order, so tools/list
// is byte-stable across runs.
//
// name is set where the spec enters a registry, from the operation table, and is
// left unset on the definitions below. A definition that named itself would be a
// second place the name is spelled, and the tool the protocol advertises would
// then be able to drift from the one the operation table claims.
//
// kinds records the entity kinds a tool serves, so the capability tool can
// narrow the long tail to the kind an author is working on. An empty kinds is
// kind-agnostic: the tool serves every entity kind (schema, scaffold, and
// diagnostic helpers are the natural agnostic tools). The field is exposure
// metadata only; it never changes what a tool does or returns.
//
// operation names the capability the tool answers, set alongside the name and
// from the same source, so two tools reaching one capability under two names
// can be recognised as reaching one capability. It is the join key: matching
// tools by a spelling would tie the pairing to names that are chosen
// independently on each side and exist precisely so they can differ.
//
// reach is how far that operation's own effects travel, read from the operation
// set where the operation is, so a spec carries the class rather than sending
// every listing back to the set for it. It is set from the same lookup the
// operation is and never authored on a definition below: a class written beside
// a tool would be a second declaration of a fact the operation set owns, and the
// two would agree until one of them was edited.
//
// commandPath is the invocation path of the command a generated tool runs,
// empty for a tool that runs its operation inside this server.
//
// reachEscalation reports how far a call would actually reach, where this tool's
// own arguments can widen it past the class its operation declares. Nil is a
// tool that reaches exactly what it declares whatever it is sent, which is every
// tool here but one. It is consulted before the tool runs and reads the same
// composition the tool itself reads, so the class a caller is admitted against
// is the class the call would have had: a second derivation of the same decision
// would agree on the day it was written and stop agreeing the first time one of
// the two learned about an input the other did not.
//

// description holds what is true of a tool however a caller reached it.
// workspaceSelectionNote holds the part that is true only where a caller may
// name a workspace of its own, and is left empty by a tool that says nothing
// about one. The two are separate because a session that withholds those
// arguments from what it advertises must withhold the sentence offering them in
// the same breath: a description and the schema beside it travel in one payload,
// so a caller reading the sentence and then being refused by the schema was told
// two different things at once. Keeping the sentence in its own field gives it
// one home rather than a second copy to be removed by pattern.
type toolSpec struct {
	name                   string
	operation              string
	reach                  opsurface.Reach
	commandPath            string
	description            string
	workspaceSelectionNote string
	inputSchema            string
	kinds                  []string
	reachEscalation        func(s *server, args json.RawMessage) opsurface.Reach
	invoke                 func(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError)
}

// toolRegistry is the curated, always-loaded tool set the authoring loop lives
// in. Every tool wraps an existing single-sourced surface — the shared
// compiler-operations layer, the embedded schema set, or the catalog resolver —
// and none reimplements it. The capability tool is the one
// meta entry: it surfaces the deferred long tail on demand so the default set
// stays small. The order is fixed so the advertised list and its wire bytes are
// deterministic.
//
// Every entry names the operation it answers, and takes its advertised name from
// that operation's row in the availability table. A tool for an operation the
// table says nothing about does not reach this registry, and no entry can be
// advertised under a name the table does not give.
//
// It is assembled in init rather than a package-level literal because the
// capability tool's handler reads this very registry to surface the working set
// for a kind; a literal would close a self-reference the initializer analysis
// rejects, so the assignment is deferred to init where that reference is a
// runtime read.
var toolRegistry []toolSpec

func init() {
	toolRegistry = []toolSpec{
		curated("workspace-entity-index", listEntitiesToolSpec),
		curated("release-candidates", listReleasesToolSpec),
		curated("validate", validateToolSpec),
		curated("build", buildToolSpec),
		curated("pull", pullToolSpec),
		curated("inspect", describeToolSpec),
		curated("get-schema", getSchemaToolSpec),
		curated("explain-error", explainErrorToolSpec),
		curated("scaffold-entity", scaffoldToolSpec),
		curated("plugin-gen-catalog", genCatalogToolSpec),
		curated("describe-capabilities", describeCapabilitiesToolSpec),
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
// machine-parseable object when the tool has one; IsError marks a failure the
// caller can correct by changing the arguments — a refused argument
// combination, a missing or wrong-typed argument, or an operation that ran and
// reported a problem. It is distinct from a protocol-level error, which travels
// as a JSON-RPC error envelope and is reserved for a fault with no tool result
// to live in. A failure carried here always carries corrective guidance in its
// text block so the assistant can act on it rather than on an opaque code; the
// rule that decides the channel is in the package documentation.
//
// It carries the per-request revision's envelope and no freshness hint. That
// revision declares a tool result an ordinary result rather than a cacheable
// one, so there is no window for it to state.
type callToolResult struct {
	perRequestEnvelope
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

// errorResult builds a tool-level failure result, and it is the route every
// failure a caller can correct travels on: a refused argument combination, a
// missing or wrong-typed argument, an unresolvable path, a diagnostic from the
// compiler. The guidance text is the actionable message an assistant reads to
// correct the call; the result is marked IsError so the client renders it as a
// failure rather than a success.
//
// The rule that decides between this route and an error envelope is stated once,
// in the package documentation, and is not restated here — two statements of one
// rule drift, and nothing fails when they do.
func errorResult(guidance string) (*callToolResult, *rpcError) {
	return structuredErrorResult(guidance, nil)
}

// structuredErrorResult is errorResult for a failure that still has a
// machine-readable result to hand back: an operation that ran, produced findings,
// and then refused. The guidance text is what an assistant reads to correct the
// call; the structured payload is the same findings in the shape the other
// surfaces render them in.
//
// A nil structured payload omits structuredContent, which is why errorResult
// delegates here rather than the other way round: one construction site for the
// failure envelope, and the two routes cannot drift into disagreeing about what a
// failing result looks like.
//
// It exists because a refusal is not a reason to drop to a lower fidelity. An
// operation whose findings are readable on success and only prose on failure
// reports the same findings two ways, and the failing case is the one a caller
// most needs to act on programmatically.
func structuredErrorResult(guidance string, structured any) (*callToolResult, *rpcError) {
	res := &callToolResult{
		Content: []toolTextContent{{Type: "text", Text: guidance}},
		IsError: true,
	}
	if structured != nil {
		b, err := json.Marshal(structured)
		if err != nil {
			return nil, &rpcError{Code: codeInternalError, Message: "marshal tool result: " + err.Error()}
		}
		res.StructuredContent = b
	}
	return res, nil
}

// payload names in the corrective text a decode failure produces: what the
// surface that rejected the call calls the object it could not read. A tool
// reads an arguments object; a protocol method reads a params object.
const (
	payloadArguments = "arguments"
	payloadParams    = "params"
)

// invalidToolArguments builds the failure a tool returns when it cannot read its
// arguments object. The tool is already resolved and the caller corrects this by
// re-sending the arguments, so it is a tool-level failure carrying the corrective
// text rather than an error envelope.
//
// It returns the tool-invoke result pair so a handler returns it directly.
func invalidToolArguments(tool string, err error) (*callToolResult, *rpcError) {
	return errorResult(decodeFailureGuidance(tool, payloadArguments, err))
}

// invalidRequestParams builds the protocol error a method returns when it cannot
// read its params object. This one stays an error envelope: the params are what
// name the tool, so a params the server cannot read leaves no tool result to
// answer through. It shares the corrective-text route with the arguments failure
// above so the two cannot phrase the same rejection differently.
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
//
// The descriptions are this session's rather than the registry's bare ones,
// because a curated tool discloses the generated tool that reaches the same
// capability by running its command, and which commands a session was handed is
// a property of that session. The schemas are this session's for the same
// reason: which arguments a caller may send depends on how it reached the
// server, and the listing is where it reads them.
//
// ctx is the request's own, and it is what the listing is narrowed by: a tool
// the credential this request carried does not admit is absent from the answer,
// so a client learns the bound from the list rather than from a failed call.
// That is why the listing is composed per request rather than once per session —
// this server holds nothing between requests, and two requests on one connection
// may present different credentials.
func (s *server) handleToolsList(ctx context.Context) (any, *rpcError) {
	curatedTools := s.curatedTools(ctx)
	tools := make([]tool, 0, len(curatedTools))
	for i := range curatedTools {
		tools = append(tools, tool{
			Name:        curatedTools[i].name,
			Description: s.advertisedDescription(curatedTools[i]),
			InputSchema: s.advertisedSchema(curatedTools[i]),
		})
	}
	return listToolsResult{Tools: tools}, nil
}

// handleToolsCall dispatches a tools/call request to the named tool. Params it
// cannot read, an absent tool name, and a name no tool answers to are the three
// failures that reach this surface before a tool is resolved: none has a tool
// result to live in, so each is an invalid-params error envelope, and the
// unknown-name envelope names the curated set and the capability tool so the
// caller can correct it. Once a tool is resolved, its own outcome — success or a
// guidance-carrying failure — is returned as the result, including a refusal of
// the arguments it was given.
//
// Two refusals are composed here rather than by the tool, both after the tool is
// resolved and before it runs, and every tool crosses this line — so each holds
// for the tools that resolve a workspace, the ones that answer without resolving
// one, and the generated command tools alike, with one home for its wording
// rather than one per tool.
//
// The first is a call that names a workspace this session does not let it name,
// which travels as that tool's own failing result. The second is a call reaching
// further than the credential it presented admits, which does not: it is a
// refusal of the request rather than an outcome of the tool, the tool never ran,
// and the transport carries it under its own status with the challenge naming
// the scope that would admit it. A tool the credential does not admit is also
// absent from that request's tool list, so this answer is what a caller reaches
// by naming a tool directly rather than by reading what it was offered.
//
// ctx is the run's own, handed to the resolved tool. Every tool that publishes,
// pulls, builds, lints or searches carries it into the work it drives, so ending
// the run ends that work rather than only its report.
func (s *server) handleToolsCall(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var p callToolParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidRequestParams("tools/call", err)
	}
	if p.Name == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "tools/call requires a tool name; available tools: " + toolNames()}
	}
	if spec, ok := s.lookupTool(p.Name); ok {
		// Read before the tool reads its own arguments, and here rather than where
		// a workspace is resolved, because not every tool resolves one: the
		// generated command tools carry the same argument and run a command with
		// it, and one curated tool answers from the machine-wide caches when no
		// workspace resolves, so a refusal placed at resolution would be dropped
		// there without a word.
		if refusal := s.workspaceSelectionRefusal(p.Arguments); refusal != "" {
			return errorResult(refusal)
		}
		// Read before the tool runs and after the refusal above, so nothing a
		// call the credential does not admit asked for has happened by the time
		// it is refused, and so a call naming a workspace this session does not
		// let it name is answered about that rather than about a scope.
		if refusal := s.scopeRefusal(ctx, spec, p.Arguments); refusal != nil {
			return nil, refusal
		}
		return spec.invoke(s, ctx, p.Arguments)
	}
	// One class of name is answered with more than "no such tool", and it is the
	// class a caller is most likely to have read about somewhere else: a tool
	// that runs a command in a separate process exists on the transport whose
	// caller is at this machine's keyboard and on no other. Saying only that it
	// is unknown would leave a caller correcting a name that was never wrong.
	if s.transport == transportHTTP {
		if refusal := commandToolTransportRefusal(p.Name); refusal != "" {
			return nil, &rpcError{Code: codeInvalidParams, Message: refusal}
		}
	}
	return nil, &rpcError{Code: codeInvalidParams, Message: "unknown tool: " + p.Name +
		"; available tools: " + toolNames() +
		"; call " + describeCapabilitiesName + " to discover additional capabilities"}
}

// lookupTool resolves a tool by name across the two statically declared
// registries — the curated set and the deferred set — so a deferred tool a
// client discovered through the capability tool is invocable by the same
// tools/call path as a curated one.
//
// It spans the static registries alone, which is what a caller holding no
// session wants. A tools/call goes through the server's own lookup below, which
// also spans the command tools derived for that session.
func lookupTool(name string) (*toolSpec, bool) {
	return lookupToolIn(name, toolRegistry, deferredToolRegistry)
}

// lookupTool resolves a tool by name across every registry this session serves:
// the curated set, the deferred set, and the command tools derived from the
// command surface the caller supplied.
func (s *server) lookupTool(name string) (*toolSpec, bool) {
	return lookupToolIn(name, toolRegistry, deferredToolRegistry, s.commandTools)
}

// lookupToolIn resolves a tool by name across the given registries in order. It
// is the one scan the two lookups above share, so which registries a lookup
// spans is the only thing that distinguishes them.
func lookupToolIn(name string, registries ...[]toolSpec) (*toolSpec, bool) {
	for _, reg := range registries {
		for i := range reg {
			if reg[i].name == name {
				return &reg[i], true
			}
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
