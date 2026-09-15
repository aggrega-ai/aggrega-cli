// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"encoding/json"
	"slices"
	"time"
)

// The reserved metadata names the per-request revision fixes for the two
// facts a request must state about itself. They are the foreign protocol's
// own tokens, spelled exactly as it spells them.
//
// They are held as constants rather than as struct tags because the refusal a
// missing one earns has to name the key it missed, and a tag cannot be read
// from a message. Requests are therefore decoded through a keyed lookup, so
// each name has one home. Results go the other way — nothing quotes a result
// key back to a caller — so the result side carries its names in tags.
const (
	metaKeyProtocolVersion    = "io.modelcontextprotocol/protocolVersion"
	metaKeyClientCapabilities = "io.modelcontextprotocol/clientCapabilities"
)

// resultTypeComplete marks a result that carries the whole of the answer,
// which is every result this server produces: no method here asks the caller
// for more input before it can finish.
const resultTypeComplete = "complete"

// The two scopes a cacheable result may declare: public for a result no
// caller's identity shaped, which any cache may serve to any caller, and
// private for one only a cache serving that same caller may reuse.
const (
	cacheScopePublic  = "public"
	cacheScopePrivate = "private"
)

// compiledInFreshness is how long a client may reuse a result composed from
// content that cannot change while the process lives.
const compiledInFreshness = time.Hour

// protocolEra is which of the two revisions a request is speaking. It is read
// off the request itself on every message: this server remembers nothing
// between requests, so nothing about a client's first message decides how its
// later ones are answered.
type protocolEra int

const (
	// eraHandshake is a request from a client that settled the protocol in an
	// initialize exchange. It carries none of the per-request metadata, and its
	// result carries nothing the handshake revision does not define.
	eraHandshake protocolEra = iota
	// eraPerRequest is a request that states its own revision and its sender's
	// capabilities, and is answered with the result envelope that revision
	// requires.
	eraPerRequest
)

// readRequestProtocol is the single home of era detection. It reads a
// request's params metadata and returns the era the request is speaking,
// along with the refusal the request has already earned where it named the
// per-request revision without conforming to it.
//
// It returns the era alone rather than the version string beside it, because
// nothing downstream has a use for that string: it has already been checked
// against what the server speaks, and every answer turns on the era. A caller
// that needs more of the metadata widens this return where it needs it, so
// nothing travels here that no one reads.
//
// The era turns on whether the request carries any of the reserved
// per-request names at all, rather than on the protocol-version name alone.
// Both are required of a request in that revision, so a request carrying one
// and not the other is a malformed request in that revision rather than a
// request in the handshake one — and answering it in the handshake revision
// would hide the mistake behind a reply that looks like it worked. Nothing is
// misread in the other direction: neither name exists in the handshake
// revision, whose metadata carries a progress token and nothing else.
//
// Params that are absent, null, or not an object at all leave the request in
// the handshake era, which is the reading that decides nothing a handler has
// not already decided: the handlers that need params parse them themselves and
// refuse what they cannot read.
func readRequestProtocol(params json.RawMessage) (protocolEra, *rpcError) {
	meta := readRequestMeta(params)
	_, hasVersion := meta[metaKeyProtocolVersion]
	_, hasCapabilities := meta[metaKeyClientCapabilities]
	if !hasVersion && !hasCapabilities {
		return eraHandshake, nil
	}

	revision, wellFormed := metaString(meta, metaKeyProtocolVersion)
	if !wellFormed {
		return eraPerRequest, missingRequestMetadata(metaKeyProtocolVersion)
	}
	if !hasCapabilities {
		return eraPerRequest, missingRequestMetadata(metaKeyClientCapabilities)
	}
	if !speaksRevision(revision) {
		return eraPerRequest, unsupportedRevision(revision)
	}
	return eraPerRequest, nil
}

// readRequestMeta decodes the metadata object a request carries in its
// params. A nil result means the request carried none that could be read,
// which the era reader treats as the handshake era.
func readRequestMeta(params json.RawMessage) map[string]json.RawMessage {
	if len(params) == 0 {
		return nil
	}
	var envelope struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if err := json.Unmarshal(params, &envelope); err != nil {
		return nil
	}
	return envelope.Meta
}

// metaString reads one metadata value as a string. A name that is absent, or
// present carrying something that is not a string, is reported the same way:
// the request did not supply the field the revision requires of it, and the
// refusal is the same either way.
func metaString(meta map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := meta[key]
	if !ok {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

// supportedRevisions is the one statement of which revisions this server
// speaks, most preferred first. Both the discovery answer and the refusal a
// request naming some other revision earns are composed from it, so the two
// cannot come to disagree.
func supportedRevisions() []string {
	return []string{perRequestRevision, handshakeRevision}
}

// speaksRevision reports whether a request may name this revision.
func speaksRevision(revision string) bool {
	return slices.Contains(supportedRevisions(), revision)
}

// unsupportedRevisionData is the payload the per-request revision fixes for a
// refusal on version grounds: what the server does speak, and what was asked
// for.
type unsupportedRevisionData struct {
	Supported []string `json:"supported"`
	Requested string   `json:"requested"`
}

// unsupportedRevision refuses a request naming a revision this server does not
// speak, telling the caller both what it asked for and what it could ask for
// instead, so one round trip is enough to recover.
func unsupportedRevision(requested string) *rpcError {
	// A string slice and a string; there is no value here encoding can refuse.
	data, _ := json.Marshal(unsupportedRevisionData{
		Supported: supportedRevisions(),
		Requested: requested,
	})
	return &rpcError{
		Code:    codeUnsupportedProtocolVersion,
		Message: "unsupported protocol version: " + requested,
		Data:    data,
	}
}

// missingRequestMetadata refuses a request that claimed the per-request
// revision without carrying one of the two fields that revision requires of
// every request. It names the missing field, because the caller cannot see
// which of them it dropped.
func missingRequestMetadata(key string) *rpcError {
	return &rpcError{
		Code:    codeInvalidParams,
		Message: "request _meta is missing the required field " + key,
	}
}

// resultMeta is the metadata a result carries back. The per-request revision
// asks a server to identify itself on every answer, so a client holding a
// reply can say which server produced it without having asked anything
// earlier.
type resultMeta struct {
	ServerInfo implementation `json:"io.modelcontextprotocol/serverInfo"`
}

// perRequestEnvelope carries what the per-request revision requires of every
// result: the kind of result it is, and the server's identity.
//
// It is embedded at the head of a result type, so its fields lead the encoded
// object and the rest of that result follows in its own declared order. Both
// fields drop out of the encoding when they are unset, which is what lets one
// result type serve both revisions: a result composed for the handshake
// revision leaves the embedded value zero and encodes with neither field,
// carrying nothing that revision does not define.
type perRequestEnvelope struct {
	Meta       *resultMeta `json:"_meta,omitempty"`
	ResultType string      `json:"resultType,omitempty"`
}

// completeEnvelope is the envelope for a result that carries the whole answer.
func (s *server) completeEnvelope() perRequestEnvelope {
	identity := s.identity()
	return perRequestEnvelope{
		Meta:       &resultMeta{ServerInfo: identity},
		ResultType: resultTypeComplete,
	}
}

// cacheHint is how long, and to whom, a result may be served from a cache.
// The per-request revision asks for it on the results a client is likely to
// ask for repeatedly.
//
// It embeds beside perRequestEnvelope on a result type that carries one. The
// window is a pointer so that a genuinely zero window — "stale on arrival" —
// is stated rather than dropped, which is the one value an omit-on-empty
// field could not express.
type cacheHint struct {
	TTLMs      *int64 `json:"ttlMs,omitempty"`
	CacheScope string `json:"cacheScope,omitempty"`
}

// The freshness a result advertises is chosen by WHERE ITS CONTENT CAME FROM,
// never by which method asked for it. That is why these two are the whole
// table, and why a method picks one of them rather than declaring a window of
// its own: two methods reading the same content would otherwise be free to
// tell a client different things about how long it keeps.
//
// Content compiled into the binary, and content read from the documentation
// bundle pinned at the start of the run, cannot change while the process lives
// and is shaped by nothing about the caller: public, fresh for a long window.
// Content read out of the caller's own workspace changes under their editor
// between one request and the next, and belongs to them alone: private, and
// stale the moment it arrives.
//
// compiledInCacheHint is the first of the two.
func compiledInCacheHint() cacheHint {
	window := compiledInFreshness.Milliseconds()
	return cacheHint{TTLMs: &window, CacheScope: cacheScopePublic}
}

// workspaceCacheHint is the second: a window of zero, stated rather than left
// out, so a client is told to read again rather than left to guess how long
// the answer is good for.
func workspaceCacheHint() cacheHint {
	var window int64
	return cacheHint{TTLMs: &window, CacheScope: cacheScopePrivate}
}

// cacheHintFor is the freshness hint a result carries back to a request in
// this era. A handshake-revision result carries none, because that revision
// defines no such field and a result carries nothing its own revision does not
// define.
func cacheHintFor(era protocolEra, hint cacheHint) cacheHint {
	if era != eraPerRequest {
		return cacheHint{}
	}
	return hint
}

// stampResult attaches what the per-request revision asks of a result: the
// kind of result it is and the server's identity on every one of them, and the
// freshness hint on the ones that revision makes cacheable.
//
// It is the single site that attaches them, so no handler decides for itself
// whether its answer is decorated. A handler composes what it was asked for;
// the revision the request spoke decides what travels with it.
//
// Every result reached here except one is composed entirely from content the
// binary carries or from the bundle pinned for the run, so each takes the
// compiled-in hint. resources/read is that one: its content comes from
// whichever family the URI named, so its hint is chosen where the family is
// resolved and this site leaves it as it found it.
//
// Two answers deliberately reach the end untouched. The per-request revision
// defines neither ping nor initialize — its client-request set names ten
// methods and neither is among them — so those answers belong to the handshake
// revision and carry nothing this one adds, whatever metadata the request that
// asked for them happened to carry.
func (s *server) stampResult(era protocolEra, result any) any {
	if era != eraPerRequest {
		return result
	}
	envelope := s.completeEnvelope()
	switch r := result.(type) {
	case discoverResult:
		r.perRequestEnvelope, r.cacheHint = envelope, compiledInCacheHint()
		return r
	case listToolsResult:
		r.perRequestEnvelope, r.cacheHint = envelope, compiledInCacheHint()
		return r
	case listResourcesResult:
		r.perRequestEnvelope, r.cacheHint = envelope, compiledInCacheHint()
		return r
	case listResourceTemplatesResult:
		r.perRequestEnvelope, r.cacheHint = envelope, compiledInCacheHint()
		return r
	case listPromptsResult:
		r.perRequestEnvelope, r.cacheHint = envelope, compiledInCacheHint()
		return r
	case readResourceResult:
		r.perRequestEnvelope = envelope
		return r
	case getPromptResult:
		r.perRequestEnvelope = envelope
		return r
	case *callToolResult:
		// Stamped on a copy rather than through the pointer: the value belongs
		// to whatever composed the tool's result, and writing into it would put
		// this request's envelope on something a later request could be handed.
		stamped := *r
		stamped.perRequestEnvelope = envelope
		return &stamped
	}
	return result
}
