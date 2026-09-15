// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// discoverResult is the server/discover result: the revisions the server
// speaks, what it offers, and the guidance a client may put in front of the
// model before its first call.
//
// The per-request revision has no handshake, so this is where a client learns
// everything the initialize exchange used to tell it. It is a result a client
// may cache, so it carries the freshness hint as well as the envelope every
// result carries.
type discoverResult struct {
	perRequestEnvelope
	cacheHint
	SupportedVersions []string           `json:"supportedVersions"`
	Capabilities      serverCapabilities `json:"capabilities"`
	Instructions      string             `json:"instructions,omitempty"`
}

// handleDiscover answers server/discover.
//
// It answers from the same three places the initialize handshake answers
// from — the capability set, the server identity, and the connect-time
// guidance — so a client reaches the same server whichever revision it
// speaks, and neither answer can drift from the other.
//
// The envelope and the freshness hint are attached where every result's are,
// so this handler states what discovery reports and nothing about how the
// revision dresses it.
func (s *server) handleDiscover() (any, *rpcError) {
	return discoverResult{
		SupportedVersions: supportedRevisions(),
		Capabilities:      serverCapabilitySet(),
		Instructions:      serverInstructions,
	}, nil
}
