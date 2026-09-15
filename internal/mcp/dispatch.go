// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
)

// dispatch routes a request to its per-method handler and returns either
// a result payload or a JSON-RPC error envelope. The routing table is the
// load-bearing surface for the server's wire protocol: adding a method
// means adding a case here.
//
// tools/list returns the curated tool set and tools/call runs a named tool.
// The resource surfaces enumerate the fixed schema family and the CLI entry
// point, advertise the full set of URI templates, and read a resource by URI;
// the prompt surfaces list and render the authoring personas and the workflow
// calling sequences. An unrecognized method returns the standard method-not-found
// envelope.
//
// The revision a request speaks is read off the request before anything is
// routed, and a request that claims the per-request revision without
// conforming to it is refused here rather than reaching a handler. Two methods
// belong to one revision only: initialize is the handshake revision's own, and
// server/discover carries the same facts in the per-request revision, so a
// request reaching for either from the wrong revision is told the server does
// not serve that method.
//
// The result a handler composes is then stamped with whatever the request's own
// revision asks a result to carry, in one place, so a handler never decides
// that for itself.
//
// ctx is the run's own. It reaches every tool invocation and the resource reads
// that leave the process, so ending the run ends what a call is doing rather
// than only refusing the next message.
func (s *server) dispatch(ctx context.Context, req *rpcRequest) (any, *rpcError) {
	era, _, protocolErr := readRequestProtocol(req.Params)
	if protocolErr != nil {
		return nil, protocolErr
	}
	return s.dispatchInEra(ctx, era, req)
}

// dispatchInEra is dispatch with the era already read: it routes the request
// and stamps what the handler composed. It is the whole of the route from a
// parsed request to an answer, and both transports arrive here.
//
// The split exists because one transport reads the era before it can finish
// validating the request. The HTTP transport's header requirements are
// era-scoped — a request in the per-request revision must mirror its body's
// revision, method and target name in headers, and a handshake-revision request
// must not be held to any of that — so it needs the era in hand before routing.
// Without this entry point it would either read the same metadata a second time,
// leaving two reads to drift apart, or grow a method table of its own, which is
// the one thing the table above exists to prevent.
func (s *server) dispatchInEra(ctx context.Context, era protocolEra, req *rpcRequest) (any, *rpcError) {
	result, rpcErr := s.route(ctx, era, req)
	if rpcErr != nil {
		return nil, rpcErr
	}
	return s.stampResult(era, result), nil
}

// route is the method table itself: it resolves a request to the handler that
// answers it and returns what that handler composed, undressed.
func (s *server) route(ctx context.Context, era protocolEra, req *rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req.Params)
	case "server/discover":
		if era != eraPerRequest {
			return nil, methodNotFound(req.Method)
		}
		return s.handleDiscover()
	case "ping":
		return struct{}{}, nil
	case "tools/list":
		return s.handleToolsList()
	case "resources/list":
		return s.handleResourcesList()
	case "resources/templates/list":
		return listResourceTemplatesResult{ResourceTemplates: allResourceTemplates()}, nil
	case "prompts/list":
		return handlePromptsList()
	case "prompts/get":
		return handlePromptsGet(req.Params)
	case "tools/call":
		return s.handleToolsCall(ctx, req.Params)
	case "resources/read":
		return s.handleResourcesRead(ctx, era, req.Params)
	default:
		return nil, methodNotFound(req.Method)
	}
}

// methodNotFound builds the standard envelope for a method this server does
// not serve. It is the one home for that answer, so a method the server serves
// in one revision and not the other is indistinguishable from a method it has
// never heard of — which is what a client in the wrong revision should see.
func methodNotFound(method string) *rpcError {
	return &rpcError{Code: codeMethodNotFound, Message: "method not found: " + method}
}

// allResourceTemplates advertises every URI-template family the read surface
// resolves: the documentation families (language-reference and authoring
// guide), the entity-schema family, the catalog-entity family, and the CLI-help
// family. The order is fixed so the advertised list is byte-stable across runs.
func allResourceTemplates() []resourceTemplate {
	out := docsResourceTemplates()
	out = append(out, schemaResourceTemplate(), catalogResourceTemplate(), cliResourceTemplate())
	return out
}

// handleResourcesList enumerates the concrete resources the server serves
// without a parameter: one entry per addressable entity-schema kind, plus the
// CLI-help entry point. The documentation and catalog families are templated
// rather than enumerated — their instance sets are parameterized by page path
// or resolved workspace — so they appear only in resources/templates/list. The
// order is deterministic: the schema entries in the schema set's own sorted
// order, then the CLI entry point.
func (s *server) handleResourcesList() (any, *rpcError) {
	out := schemaResourceList()
	out = append(out, cliRootResource())
	return listResourcesResult{Resources: out}, nil
}

// resourceNotFound builds the resource-not-found envelope for a URI whose
// scheme is unrecognized or whose target is absent. It is the one place that
// code is built, which is what lets refusalInEra re-code it for the revision
// that retired it — so a reader here should not take the code as the one a
// client necessarily sees.
func resourceNotFound(uri string) *rpcError {
	return &rpcError{Code: codeResourceNotFound, Message: "resource not found: " + uri}
}

// handleNotification handles a notification for its side effect. A
// notification carries no id and yields no response. The initialized
// notification confirms the handshake completed; unrecognized
// notifications are logged to stderr and otherwise ignored, per the
// protocol's forward-compatibility rule.
//
// It runs on the message loop's own goroutine rather than on a goroutine of its
// own, so a cancellation takes effect at the moment it is read rather than
// queueing behind the work it is cancelling.
func (s *server) handleNotification(req *rpcRequest) {
	switch req.Method {
	case "notifications/initialized":
		// Handshake complete; the client may now issue requests.
	case "notifications/cancelled":
		s.handleCancelled(req.Params)
	default:
		s.logf("mcp: ignoring unrecognized notification %q", req.Method)
	}
}

// handleCancelled ends the request the notification names, by routing its id to
// the cancel registry. The named request's context is cancelled, its handler
// observes that at its next interruption point, and nothing further is written
// for it.
//
// Three cases come to nothing, and all three are what the protocol asks for.
// Params that will not parse are dropped silently — a notification has no
// response channel an error could be reported on. An id nothing is working on,
// and an id whose request has already been answered, are both a lookup that
// misses; the server keeps serving either way.
func (s *server) handleCancelled(params json.RawMessage) {
	var p cancelledParams
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	s.cancels.Cancel(p.RequestID)
}

// handleInitialize answers the initialize handshake. It advertises the
// tools, resources, and prompts capabilities, reports the server
// identity, and hands back the connect-time guidance a client may surface
// to the model. The negotiated protocol revision echoes the client's when
// the server supports it, and otherwise reports the revision the client
// should adopt. Malformed params are logged and the handshake proceeds
// with the server's revision, since the negotiation can complete without
// them.
//
// It reaches nothing outside the process: the documentation bundle is already
// acquired by the time any message is answered, so this handler composes a
// reply out of what the server was built with and nothing else.
func (s *server) handleInitialize(params json.RawMessage) (any, *rpcError) {
	var p initializeParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			s.logf("mcp: initialize params parse error: %v", err)
		}
	}
	if p.ClientInfo.Name != "" {
		s.logf("mcp: initialize from client %q version %q (protocol %q)",
			p.ClientInfo.Name, p.ClientInfo.Version, p.ProtocolVersion)
	}
	return initializeResult{
		ProtocolVersion: negotiateProtocolVersion(p.ProtocolVersion),
		Capabilities:    serverCapabilitySet(),
		ServerInfo:      s.identity(),
		Instructions:    serverInstructions,
	}, nil
}

// negotiateProtocolVersion returns the revision the handshake settles on:
// the client's requested revision when the server supports it, otherwise
// the revision the handshake is defined in, which signals the client to
// adopt it or disconnect. A client reaching this exchange at all has chosen
// the handshake, so the other revision this server speaks is not on offer
// here — a client that wants it states it on each request instead.
func negotiateProtocolVersion(requested string) string {
	if requested == handshakeRevision {
		return requested
	}
	return handshakeRevision
}

// handleResourcesRead dispatches a resources/read request. It validates the
// params and routes by URI scheme: langref:// and authoring:// serve
// documentation pages from the local bundle cache; schema:// serves an entity's
// canonical JSON Schema; catalog:// serves a catalog entity's authored YAML from
// the resolved workspace; cli:// serves a command's help. A missing URI is an
// invalid-params error; an unrecognised scheme or an absent target is a
// well-formed not-found error in whichever code the request's revision uses.
//
// This is the one method whose result is not fresh for the same span whatever
// it was asked for: most families serve content the process cannot see change,
// and the catalog family serves the caller's own workspace. So the freshness
// hint is chosen here, beside the branch that resolves the family, rather than
// by the method.
//
// It takes the run's context so every routed method carries the same shape, and
// consults it nowhere: no family's read reaches work that a context could end.
// Each of them answers from bytes this process already holds — a cached page, a
// compiled-in schema, a file in the resolved workspace, or the command tree.
func (s *server) handleResourcesRead(_ context.Context, era protocolEra, params json.RawMessage) (any, *rpcError) {
	var p readResourceParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidRequestParams("resources/read", err)
	}
	if p.URI == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "resources/read requires a resource uri"}
	}
	scheme, _, ok := splitScheme(p.URI)
	if !ok {
		return nil, refusalInEra(era, resourceNotFound(p.URI))
	}
	var (
		res  readResourceResult
		rerr *rpcError
		hint cacheHint
	)
	switch scheme {
	case schemeLangref, schemeAuthoring:
		// The bundle is pinned at the start of the run and never re-acquired
		// while it lasts, so a page served from it is as fixed as one the
		// binary carries.
		res, rerr = s.readDocsResource(p.URI)
		hint = compiledInCacheHint()
	case schemeSchema:
		res, rerr = readSchemaResource(p.URI)
		hint = compiledInCacheHint()
	case schemeCatalog:
		// The only family read out of the caller's workspace, where their own
		// editor can change the answer between one request and the next.
		res, rerr = s.readCatalogResource(p.URI)
		hint = workspaceCacheHint()
	case schemeCli:
		// Rendered from the command tree this process holds, which is the tree
		// compiled into the binary.
		res, rerr = s.readCliResource(p.URI)
		hint = compiledInCacheHint()
	default:
		return nil, refusalInEra(era, resourceNotFound(p.URI))
	}
	if rerr != nil {
		return nil, refusalInEra(era, rerr)
	}
	res.cacheHint = cacheHintFor(era, hint)
	return res, nil
}

// refusalInEra re-codes a refusal whose code differs between the two
// revisions, and is the only place in this server where one does.
//
// The per-request revision retired the resource-not-found code its
// predecessors used and answers a URI no family resolves with the
// invalid-params code instead, so the retired code never reaches a client
// speaking that revision. Every other refusal travels unchanged, and the
// refusal it is handed is never written through: the caller's value is left as
// it was.
//
// It pairs with resourceNotFound, which is the one place that code is built.
func refusalInEra(era protocolEra, err *rpcError) *rpcError {
	if era != eraPerRequest || err.Code != codeResourceNotFound {
		return err
	}
	recoded := *err
	recoded.Code = codeInvalidParams
	return &recoded
}
