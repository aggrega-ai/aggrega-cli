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
// ctx is the run's own. It reaches the handshake's bundle acquisition, every
// tool invocation, and the resource reads that leave the process, so ending the
// run ends what a call is doing rather than only refusing the next message.
func (s *server) dispatch(ctx context.Context, req *rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(ctx, req.Params)
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
		return s.handleResourcesRead(ctx, req.Params)
	default:
		return nil, &rpcError{Code: codeMethodNotFound, Message: "method not found: " + req.Method}
	}
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

// resourceNotFound builds the protocol's resource-not-found envelope for a URI
// whose scheme is unrecognized or whose target is absent.
func resourceNotFound(uri string) *rpcError {
	return &rpcError{Code: codeResourceNotFound, Message: "resource not found: " + uri}
}

// handleNotification handles a notification for its side effect. A
// notification carries no id and yields no response. The initialized
// notification confirms the handshake completed; unrecognized
// notifications are logged to stderr and otherwise ignored, per the
// protocol's forward-compatibility rule.
func (s *server) handleNotification(req *rpcRequest) {
	switch req.Method {
	case "notifications/initialized":
		// Handshake complete; the client may now issue requests.
	case "notifications/cancelled":
		// No long-running request is cancellable in this surface yet.
	default:
		s.logf("mcp: ignoring unrecognized notification %q", req.Method)
	}
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
// It is also where the documentation bundle is acquired, once per session and
// ahead of any resource read, so that the read path stays offline: see
// ensureDocsBundle, which reports its own outcome to the log stream and cannot
// fail the handshake.
func (s *server) handleInitialize(ctx context.Context, params json.RawMessage) (any, *rpcError) {
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
	s.ensureDocsBundle(ctx)
	return initializeResult{
		ProtocolVersion: negotiateProtocolVersion(p.ProtocolVersion),
		Capabilities: serverCapabilities{
			Tools:     &toolsCapability{ListChanged: false},
			Resources: &resourcesCapability{Subscribe: false, ListChanged: false},
			Prompts:   &promptsCapability{ListChanged: false},
		},
		ServerInfo:   implementation{Name: serverName, Version: s.serverVersion},
		Instructions: serverInstructions,
	}, nil
}

// negotiateProtocolVersion returns the revision the handshake settles on:
// the client's requested revision when the server supports it, otherwise
// the server's own revision, which signals the client to adopt it or
// disconnect.
func negotiateProtocolVersion(requested string) string {
	if requested == protocolVersion {
		return requested
	}
	return protocolVersion
}

// handleResourcesRead dispatches a resources/read request. It validates the
// params and routes by URI scheme: langref:// and authoring:// serve
// documentation pages from the local bundle cache; schema:// serves an entity's
// canonical JSON Schema; catalog:// serves a catalog entity's authored YAML from
// the resolved workspace; cli:// serves a command's help. A missing URI is an
// invalid-params error; an unrecognised scheme or an absent target is a
// well-formed resource-not-found error.
func (s *server) handleResourcesRead(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var p readResourceParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidRequestParams("resources/read", err)
	}
	if p.URI == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "resources/read requires a resource uri"}
	}
	scheme, _, ok := splitScheme(p.URI)
	if !ok {
		return nil, resourceNotFound(p.URI)
	}
	switch scheme {
	case schemeLangref, schemeAuthoring:
		return s.readDocsResource(p.URI)
	case schemeSchema:
		return readSchemaResource(p.URI)
	case schemeCatalog:
		return s.readCatalogResource(p.URI)
	case schemeCli:
		return s.readCliResource(ctx, p.URI)
	default:
		return nil, resourceNotFound(p.URI)
	}
}
