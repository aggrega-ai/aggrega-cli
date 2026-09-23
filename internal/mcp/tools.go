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
// set a client meets first, or the tail behind it. Both are advertised in every
// tools/list and both are invocable by name through the same dispatch. The
// registry order is the advertised order, so tools/list is byte-stable across
// runs.
//
// name is set where the spec enters a registry, from the operation table, and is
// left unset on the definitions below. A definition that named itself would be a
// second place the name is spelled, and the tool the protocol advertises would
// then be able to drift from the one the operation table claims.
//
// kinds records the entity kinds a tool serves, so the capability tool can
// narrow its index to the kind an author is working on. An empty kinds is
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
// tool that reaches exactly what it declares whatever it is sent, which is the
// ordinary case. It is consulted before the tool runs and reads the same
// composition the tool itself reads, so the class a caller is admitted against
// is the class the call would have had: a second derivation of the same decision
// would agree on the day it was written and stop agreeing the first time one of
// the two learned about an input the other did not.
//

// summary is the one-line purpose the tool index carries, declared beside the
// description rather than extracted from it. Extraction was the alternative and
// is refused: a description is a paragraph written to be read once a caller has
// already chosen a tool, and cutting it at its first full stop produces a line
// that is merely usually right — a sentence opening with an abbreviation, a file
// suffix or a version number is cut mid-clause, and the result reads as a
// complete thought while saying something else. The index is the map every later
// call is routed by, so a line that is wrong there misroutes rather than merely
// reading badly, and the cost of declaring one line per tool is paid once.
//
// It is required of every tool a registry carries, and the registry entry point
// refuses a spec without one. A projection is only as good as its least
// complete row: one blank purpose is invisible in a result that is otherwise
// full, and an index with a hole in it is worse than no index, because a caller
// reads the hole as "this tool does nothing worth saying".
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
//
// signingKeyNote holds the part that is true only where a caller may select a
// signing key this machine holds and let the environment carry an identity, and
// is left empty by a tool that signs nothing. It is a second field rather than
// more text in the first because the two say different things about different
// arguments; what they share is the rule above, and that rule has one home in
// [toolSpec.notesWithheldFromAListener].
//
// commandSurfaceNote holds the part that is true only where this session derived
// the generated tools that reach a capability by running a command in a separate
// process, and is left empty by a tool that says nothing about that family.
//
// It is scoped on a different question from the two above — what this session
// holds, rather than how its caller reached it — and the two do not coincide. A
// session reached over a listener derives none of those tools, and so does a
// session over the process's own streams that was handed no command surface. A
// sentence scoped on the transport would be right about the first and wrong about
// the second, and the caller it would be wrong about is the one sent after a tool
// that is not there. The predicate has one home in [server.offersCommandTools],
// and the composition is [server.withSessionNotes] rather than
// [toolSpec.notesWithheldFromAListener], which enumerates the transport-scoped
// pair and only those.
//
// signingKeyArguments names the arguments through which a call selects an offline
// held signing key, and is empty for a tool that signs nothing. Such a key lives
// on the machine this server runs on, so a caller that did not launch this process
// cannot be the one it belongs to: the transport that refuses these arguments also
// withholds them from what it advertises, and both read this one list rather than
// two kept in step by eye.
type toolSpec struct {
	name                   string
	operation              string
	reach                  opsurface.Reach
	commandPath            string
	summary                string
	description            string
	workspaceSelectionNote string
	signingKeyNote         string
	commandSurfaceNote     string
	inputSchema            string
	signingKeyArguments    []string
	kinds                  []string
	reachEscalation        func(s *server, args json.RawMessage) opsurface.Reach
	invoke                 func(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError)
}

// writes reports whether a call to this tool can leave something behind, for
// the index a caller reads before it chooses a tool.
//
// It is derived from the reach class the operation set declares and from the
// widening a tool's own arguments can perform, and it is deliberately not a
// second field beside them. A boolean authored on a spec would agree with the
// class on the day it was written and would then be free to disagree with it,
// with nothing failing while the two happened to match.
//
// A tool whose arguments can widen its reach past the class its operation
// declares is reported as writing, because the index is read before a call is
// composed and the question it answers is whether this tool can write at all —
// not whether the cheapest way of calling it happens to avoid doing so. The
// tool's own description is where the condition is stated.
func (spec toolSpec) writes() bool {
	return spec.reach != opsurface.ReachNone || spec.reachEscalation != nil
}

// toolRegistry is the curated, always-loaded tool set the authoring loop lives
// in. Every tool wraps an existing single-sourced surface — the shared
// compiler-operations layer, the embedded schema set, or the catalog resolver —
// and none reimplements it. The capability tool is the one meta entry: it
// narrows the tools this server announces to the ones serving one entity kind,
// and returns any of them in full by name. The order is fixed so the advertised
// list and its wire bytes are deterministic.
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
		// Beside the scaffold, because the two are the capabilities that PRODUCE
		// authoring files and a caller arriving cold can reach either without
		// having named anything first: one starts a document from a kind's
		// schema, the other converts something that already exists in a foreign
		// format. The tail behind this set is for a tool that answers once some
		// other tool has supplied a subject, which neither of these needs.
		curated("import", importToolSpec),
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
// meaningful choice between the two.
const (
	responseFormatDetailed = compilerops.VerbosityDetailed
	responseFormatConcise  = compilerops.VerbosityConcise
)

// The response format a call falls back to when it names none, one per class of
// tool. Each is declared once, so which shape a tool defaults to is a property of
// the class it belongs to rather than a token spelled again at every handler.
//
// The two classes differ on this and on nothing else. A DELIVERY tool is asked
// about a subject the caller already named — build this Release, describe this
// entity, validate this document — so the fuller answer is the one that was asked
// for, and the terse form is the opt-in. A DISCOVERY tool is asked what a
// workspace holds, by a caller that has nothing to name yet and no way to know
// what the answer will cost before it arrives; the summary is what that question
// wants, and the records themselves are the opt-in, because a caller that has read
// the summary knows the size of what it is asking for and a caller making its
// first call does not.
const (
	deliveryResponseFormatDefault  = responseFormatDetailed
	discoveryResponseFormatDefault = responseFormatConcise
)

// parseResponseFormat maps the response_format a call sent to one of the two
// declared tokens, and reports the correction to hand back when the call named a
// value the tool does not declare.
//
// The match is EXACT. Every tool carrying this argument declares its values as an
// enumerated set, and an enumerated set is an exact-match constraint: a value the
// declared schema rejects is refused rather than answered in a shape the caller did
// not ask for. Matching loosely would leave the declared set false for every
// spelling the loosening admitted, and silently — an argument quietly substituted
// reads to its sender exactly like one that was honoured, and here the two outcomes
// are different answers to different questions. A caller that mistyped is told what
// it sent and what the argument accepts, which is the correction it needs.
//
// The value is a pointer so that a value a caller sent is distinguishable from one
// it left out. Absent is the tool's own default and is not a failure; every value a
// caller wrote and the declared set does not carry is refused, the blank string
// among them. A plain string would make a refused value and an absent one the same
// bytes at exactly the point the two have to be told apart.
//
// The fallback is a parameter rather than a constant in the body because the two
// classes of tool above disagree about it. A second parser would be a second place
// the recognized tokens are spelled, and the two would agree until one of them
// learned about a spelling the other did not; passing the fallback in keeps one
// reading of what a caller wrote and leaves each caller to name the default its own
// class declares.
func parseResponseFormat(tool string, v *string, fallback string) (format, refusal string) {
	if v == nil {
		return fallback, ""
	}
	switch *v {
	case responseFormatConcise:
		return responseFormatConcise, ""
	case responseFormatDetailed:
		return responseFormatDetailed, ""
	}
	return fallback, fmt.Sprintf("%s: `response_format` selects the shape of the answer and accepts %s, "+
		"spelled exactly; %q was sent. Send one of those, or leave `response_format` out for %s.",
		tool, declaredResponseFormats(), *v, fallback)
}

// declaredResponseFormats renders the values response_format accepts, for the
// refusal above to name them in.
//
// It reads the same constants the parser matches against, so what a caller is told
// it may send and what the parser admits are one statement rather than two that
// agree today.
func declaredResponseFormats() string {
	return fmt.Sprintf("%q and %q", responseFormatDetailed, responseFormatConcise)
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

// servedTools returns every tool this session serves this request, in the order
// they are announced: the curated set, then the statically declared long tail,
// then the tools generated from this machine's commands.
//
// It is a concatenation of the two readers that already compose those halves
// rather than a third walk over the registries. Each of them applies this
// session's own composition — the disclosure a tool carries about its generated
// twin — and each narrows what it composed by what this request admits, so a
// walk written here would be a second derivation of both and would agree with
// them only until one of the three was edited.
//
// The order is fixed twice over: each registry is in its own declared order and
// the halves are joined in one, so the answer and its wire bytes are stable
// across runs.
func (s *server) servedTools(ctx context.Context) []toolSpec {
	curated := s.curatedTools(ctx)
	deferred := s.deferredTools(ctx)
	out := make([]toolSpec, 0, len(curated)+len(deferred))
	out = append(out, curated...)
	out = append(out, deferred...)
	return out
}

// handleToolsList answers tools/list with every tool this session serves: each
// tool's name, its rich description, and the JSON Schema for its arguments.
//
// It is the whole surface rather than a chosen part of it, because the protocol
// puts the decision about how much of a tool list to put in front of a model
// with the client — it fetches the definitions and defers injecting them — and a
// definition a client never received is an operation a model can never call,
// however reachable the name is on the wire. A server that answered with less
// would be making that decision on the client's behalf and taking away the only
// half of it the client can perform.
//
// A caller that cannot carry the whole list says so in configuration, and the
// narrowing is read through the same admission every listing here passes
// through, so a tool it leaves out is absent from this answer and refused at the
// call rather than absent from one and live on the other.
//
// The descriptions are this session's rather than the registry's bare ones,
// because a tool discloses the generated tool that reaches the same capability
// by running its command, and which commands a session was handed is a property
// of that session. The schemas are this session's for the same reason: which
// arguments a caller may send depends on how it reached the server, and the
// listing is where it reads them.
//
// ctx is the request's own, and it is what the listing is narrowed by: a tool
// the credential this request carried does not admit is absent from the answer,
// so a client learns the bound from the list rather than from a failed call.
// That is why the listing is composed per request rather than once per session —
// this server holds nothing between requests, and two requests on one connection
// may present different credentials.
func (s *server) handleToolsList(ctx context.Context) (any, *rpcError) {
	served := s.servedTools(ctx)
	tools := make([]tool, 0, len(served))
	for i := range served {
		tools = append(tools, tool{
			Name:        served[i].name,
			Description: s.advertisedDescription(served[i]),
			InputSchema: s.advertisedSchema(served[i]),
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
// Three refusals are composed here rather than by the tool, all after the tool is
// resolved and before it runs, and every tool crosses this line — so each holds
// for the tools that resolve a workspace, the ones that answer without resolving
// one, and the generated command tools alike, with one home for its wording
// rather than one per tool.
//
// The first is a call that names a workspace this session does not let it name.
// The second is a call that selects a signing key held on this machine where the
// caller is not the one this machine belongs to; it is read from the tool's own
// spec, so a tool that signs nothing declares none and is never asked. Both travel
// as that tool's own failing result, and both are decided from the arguments alone
// with nothing resolved and nothing run.
//
// The third is a call reaching further than the credential it presented admits,
// which does not: it is a refusal of the request rather than an outcome of the
// tool, the tool never ran, and the transport carries it under its own status with
// the challenge naming the scope that would admit it. A tool the credential does
// not admit is also absent from that request's tool list, so this answer is what a
// caller reaches by naming a tool directly rather than by reading what it was
// offered.
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
		// Read beside the refusal above, and for the same reason: both are about an
		// argument this transport does not let a call send, so a caller that sent one
		// is answered about the argument rather than about anything downstream of it.
		if refusal := s.signingKeyRefusal(*spec, p.Arguments); refusal != "" {
			return errorResult(refusal)
		}
		// Read before the tool runs and after the refusal above, so nothing a
		// call the credential does not admit asked for has happened by the time
		// it is refused, and so a call naming a workspace this session does not
		// let it name is answered about that rather than about a scope.
		if refusal := s.admissionRefusal(ctx, spec, p.Arguments); refusal != nil {
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
