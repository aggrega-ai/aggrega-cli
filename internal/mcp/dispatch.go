// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import "encoding/json"

// dispatch routes a request to its per-method handler and returns either
// a result payload or a JSON-RPC error envelope. The routing table is the
// load-bearing surface for the server's wire protocol: adding a method
// means adding a case here.
//
// tools/list returns the curated tool set and tools/call runs a named
// tool; the resource list and read surfaces return well-formed,
// presently-empty results while their bodies are wired in. An
// unrecognized method returns the standard method-not-found envelope.
func (s *server) dispatch(req *rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req.Params)
	case "ping":
		return struct{}{}, nil
	case "tools/list":
		return s.handleToolsList()
	case "resources/list":
		return listResourcesResult{Resources: []resource{}}, nil
	case "resources/templates/list":
		return listResourceTemplatesResult{ResourceTemplates: []resourceTemplate{}}, nil
	case "prompts/list":
		return listPromptsResult{Prompts: []prompt{}}, nil
	case "tools/call":
		return s.handleToolsCall(req.Params)
	case "resources/read":
		return s.handleResourcesRead(req.Params)
	default:
		return nil, &rpcError{Code: codeMethodNotFound, Message: "method not found: " + req.Method}
	}
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
// tools, resources, and prompts capabilities and reports the server
// identity. The negotiated protocol revision echoes the client's when the
// server supports it, and otherwise reports the revision the client
// should adopt. Malformed params are logged and the handshake proceeds
// with the server's revision, since the negotiation can complete without
// them.
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
		Capabilities: serverCapabilities{
			Tools:     &toolsCapability{ListChanged: false},
			Resources: &resourcesCapability{Subscribe: false, ListChanged: false},
			Prompts:   &promptsCapability{ListChanged: false},
		},
		ServerInfo: implementation{Name: serverName, Version: s.serverVersion},
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

// handleResourcesRead dispatches a resources/read request. It validates
// the params and reports an unknown resource URI as a well-formed
// resource-not-found error while the resource bodies are wired in. A
// missing URI is an invalid-params error.
func (s *server) handleResourcesRead(params json.RawMessage) (any, *rpcError) {
	var p readResourceParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid resources/read params: " + err.Error()}
	}
	if p.URI == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "resources/read requires a resource uri"}
	}
	return nil, &rpcError{Code: codeResourceNotFound, Message: "resource not found: " + p.URI}
}
