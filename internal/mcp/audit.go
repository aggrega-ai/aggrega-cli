// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// auditTimeFormat is how every line states when it was written: the date and
// time to the millisecond, with the offset from UTC.
//
// The millisecond is fixed width rather than trimmed, so every line is the same
// shape and two lines sort by their bytes as they sort by their times. The
// offset travels with the value because the stream is collected somewhere other
// than the host that wrote it, and a time with no offset is one the collector
// has to guess the meaning of.
const auditTimeFormat = "2006-01-02T15:04:05.000Z07:00"

// The transport a line was written on, as an operator reads it. They are the two
// values the transport field takes, and they are spelled here rather than
// derived from the mode's own name so the stream's vocabulary does not move when
// an internal one does.
const (
	transportNameStdio = "stdio"
	transportNameHTTP  = "http"
)

// The outcomes a request line reports, one per way a request can end.
//
// Each is what happened rather than the number that carried it, because the
// number is a transport's way of saying it and the stream is read by somebody
// asking what became of a call. They are a closed set: every request that
// reaches the endpoint ends in exactly one of them.
const (
	// outcomeOK is a call that ran and reported success.
	outcomeOK = "ok"
	// outcomeToolFailure is a call that ran and reported a failure the caller can
	// correct. The call was served; what it asked for did not succeed.
	outcomeToolFailure = "tool-failure"
	// outcomeError is a refusal carried as a protocol error envelope: a method
	// this server does not serve, a body it could not read, a cap the request
	// exceeded, or a fault of the server's own.
	outcomeError = "error"
	// outcomeUnauthenticated is a credential this deployment would not accept.
	outcomeUnauthenticated = "unauthenticated"
	// outcomeForbidden is an accepted credential that did not admit what was
	// asked for, or an origin this deployment does not serve.
	outcomeForbidden = "forbidden"
	// outcomeRateCapped is a caller that sent more than this deployment accepts
	// from one caller in a minute.
	outcomeRateCapped = "rate-capped"
	// outcomeCancelled is a caller that closed its connection before it was
	// answered, which this transport reads as cancelling the request it carried.
	outcomeCancelled = "cancelled"
)

// The lifecycle events a serving process reports about itself, as distinct from
// the requests it answers.
//
// A line carrying one of these carries no request keys: nothing about a caller
// is involved in any of them. They are the serving process's own life, which is
// the other half of what an operator supervising it needs to see.
const (
	// eventStartup is the process beginning to serve, before anything binds.
	eventStartup = "startup"
	// eventListening is the endpoint bound and accepting.
	eventListening = "listening"
	// eventStartupRefused is a launch that was refused before it served, carrying
	// the reason it was refused.
	eventStartupRefused = "startup-refused"
	// eventSigningKeyIgnored is a deployment whose workspace names a signing key
	// this listener will not use, carrying which setting names it.
	eventSigningKeyIgnored = "signing-key-ignored"
	// eventDocsBundle is the documentation bundle acquisition, carrying its
	// outcome.
	eventDocsBundle = "docs-bundle"
	// eventShutdown is the run ending and the listener released.
	eventShutdown = "shutdown"
)

// The outcomes the documentation bundle acquisition reports.
const (
	// bundleAcquired is a bundle fetched into the cache by this process.
	bundleAcquired = "acquired"
	// bundleNotAcquired is an acquisition that did not happen, carrying why. The
	// documentation resources answer not-found afterwards; the run is unaffected.
	bundleNotAcquired = "not-acquired"
	// bundleNotice is something the acquisition reported along the way, which is
	// neither of the two outcomes above.
	bundleNotice = "notice"
)

// requestAudit is the record of one request answered over a listener: who asked,
// what they asked for, how far it reached, what became of it, and how long it
// took.
//
// The field order below is the order the keys appear in the encoded line, which
// is what makes one line the same shape as the next. Every field that is a
// property of the request rather than of the answer is set before the request is
// routed, so a request that ends at any gate still carries what was known by the
// time it got there.
//
// A field is omitted when it has nothing to say rather than written empty, and
// the two are different statements: an absent client identifier is a token that
// named no client, and an absent escalation is a call that reached exactly what
// its operation declares. The four that are never omitted — the time, the
// identifier, the transport and the outcome, plus the duration, which is
// meaningful at zero — are the ones every line has an answer for.
type requestAudit struct {
	Time             string `json:"time"`
	RequestID        string `json:"request_id"`
	Transport        string `json:"transport"`
	WorkspaceRoot    string `json:"workspace_root,omitempty"`
	Subject          string `json:"subject,omitempty"`
	Issuer           string `json:"issuer,omitempty"`
	ClientID         string `json:"client_id,omitempty"`
	Method           string `json:"method,omitempty"`
	Target           string `json:"target,omitempty"`
	Reach            string `json:"reach,omitempty"`
	ReachEscalatedTo string `json:"reach_escalated_to,omitempty"`
	Outcome          string `json:"outcome"`
	Refusal          string `json:"refusal,omitempty"`
	DurationMs       int64  `json:"duration_ms"`
	TraceParent      string `json:"traceparent,omitempty"`
	TraceState       string `json:"tracestate,omitempty"`
	Baggage          string `json:"baggage,omitempty"`
}

// recordPrincipal records who the request is on behalf of, as the credential it
// carried asserted.
//
// Nothing is interpreted and nothing is compared: what a principal is called is
// the authorization server's business, and the record says who asked in the
// words that server used. A credential that named no client, and a proxy
// assertion that knows of no issuer, each leave their field out rather than
// writing it empty.
func (a *requestAudit) recordPrincipal(p *principal) {
	if p == nil {
		return
	}
	a.Subject = p.Subject
	a.Issuer = p.Issuer
	a.ClientID = p.ClientID
}

// recordRequest records what the request asked for: the method, whatever that
// method addresses by name, and the trace context the caller sent.
//
// The target is read through the one reader that already answers "what does this
// request address" for the header the per-request revision asks a client to
// mirror. A second reader here would be a second answer to one question, and the
// two would agree until a method that addresses something by name was added to
// one of them.
func (a *requestAudit) recordRequest(req *rpcRequest) {
	a.Method = req.Method
	if target, addresses := addressedName(req); addresses {
		a.Target = target
	}
	a.TraceParent, a.TraceState, a.Baggage = readTraceContext(req.Params)
}

// recordReach records the classes a call reached, as the admission check decided
// them. A call that never reached a tool recorded none, and leaves both fields
// out.
func (a *requestAudit) recordReach(rec *reachRecorder) {
	a.Reach = string(rec.declared)
	a.ReachEscalatedTo = string(rec.escalated)
}

// lifecycleAudit is the record of one thing the serving process did on its own
// account: starting, binding, being refused, acquiring its documentation, and
// stopping.
//
// It carries an event where a request line carries a method and a caller, and
// that is the whole difference between the two shapes. A reader separating the
// two streams reads for the presence of the event key.
type lifecycleAudit struct {
	Time          string `json:"time"`
	Event         string `json:"event"`
	Transport     string `json:"transport"`
	Address       string `json:"address,omitempty"`
	WorkspaceRoot string `json:"workspace_root,omitempty"`
	// Reference is what the documentation bundle acquisition was for, as the
	// reference and version it resolved to. It belongs to that one event, which
	// is the only one of them that is about something named.
	Reference string `json:"reference,omitempty"`
	Outcome   string `json:"outcome,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// writeAuditLine encodes one record and emits it as a single write.
//
// One write per line is the property that matters rather than a detail of the
// implementation: several goroutines answer requests at once and the listener's
// own diagnostics arrive on the same stream, so a line assembled across more
// than one write could be split by another writer and neither half would parse.
// The whole line, newline included, is composed in memory first and handed to
// the stream in one call.
//
// HTML escaping is turned off. The encoder escapes three characters by default
// for the benefit of a document embedded in a web page, and this is not one: a
// workspace path or a refusal sentence carrying one of them would otherwise
// reach the stream in a form nobody typed.
func writeAuditLine(w io.Writer, line any) {
	if w == nil {
		return
	}
	var composed strings.Builder
	encoder := json.NewEncoder(&composed)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(line); err != nil {
		// Every field is a string or an integer, so there is no value here
		// encoding can refuse. Dropping the line rather than reporting the
		// failure keeps this from being a second way for the log route to fail.
		return
	}
	_, _ = io.WriteString(w, composed.String())
}

// auditNow is the time a line states, to the resolution the format carries.
func auditNow() string {
	return time.Now().Format(auditTimeFormat)
}

// mintRequestID returns the identifier one request is recorded under.
//
// It is minted here and never returned to the caller: it exists so a request
// line and any lifecycle line that request caused can be read together, and a
// caller has no use for a name this process gave to its own record. It is drawn
// from the same source a credential would be, so two requests cannot be given
// one identifier by anything a caller does.
func mintRequestID() string {
	return rand.Text()
}

// transportName is how the transport a session speaks is spelled on a line.
func transportName(mode transportMode) string {
	if mode == transportHTTP {
		return transportNameHTTP
	}
	return transportNameStdio
}

// outcomeForStatus reports what became of a request, from the status its answer
// carried and whether the tool that ran reported a failure.
//
// It is the single derivation of the outcome, which is what keeps the three
// statuses the protocol's authorization design gives meanings to distinguishable
// from the general refusal. Reading the status first is what separates a call
// refused for its reach from a call refused for anything else: both travel as an
// error envelope, and only the status says which happened.
//
// A tool failure is read only on an answered call. A tool that reported a
// failure was served — the call reached it, it ran, and what it produced is a
// result — so it is a different outcome from a request the server refused, and
// an operator counting refusals should not find it among them.
func outcomeForStatus(status int, toolReportedFailure bool) string {
	switch status {
	case http.StatusUnauthorized:
		return outcomeUnauthenticated
	case http.StatusForbidden:
		return outcomeForbidden
	case http.StatusTooManyRequests:
		return outcomeRateCapped
	case http.StatusOK, http.StatusAccepted:
		if toolReportedFailure {
			return outcomeToolFailure
		}
		return outcomeOK
	default:
		return outcomeError
	}
}

// reachRecorder is how the classes a call reached travel from where they are
// decided to where the request is recorded.
//
// The two are decided deep in the call: the declared class once a tool is
// resolved, and the escalation once that tool has read the arguments it was
// sent. Neither is knowable at the request boundary, which is where the line is
// written — and recomputing either there would be a second derivation of a
// decision the admission check already took, which would agree on the day it was
// written and stop agreeing the first time one of the two learned about an input
// the other did not.
//
// It is written by the goroutine answering the request and read by that same
// goroutine after the dispatch returns, so the write is ordered before the read
// by the call itself and no lock is involved. A session speaking the process's
// own streams places none on its requests, so the recording is a no-op there.
type reachRecorder struct {
	declared  opsurface.Reach
	escalated opsurface.Reach
}

// reachRecorderKey is the request-context key the recorder travels under. It is
// an unexported type so nothing outside this package can read or write the
// value, and so it cannot collide with another package's key.
type reachRecorderKey struct{}

// withReachRecorder returns ctx carrying somewhere for the admission check to
// record the classes a call reached.
func withReachRecorder(ctx context.Context, rec *reachRecorder) context.Context {
	return context.WithValue(ctx, reachRecorderKey{}, rec)
}

// noteReach records the class an operation declares and the class the call
// escalated to, where this request is being recorded at all.
//
// The escalation is recorded only where it differs from the declared class: a
// call that reached exactly what its operation declares did not escalate, and a
// line saying it escalated to what it already was would report an event that did
// not happen.
//
// A request carrying no recorder is a session speaking the process's own
// streams, where no line is written for anything. Nothing about that is a
// failure, so it is a silent no-op rather than a reported one.
func noteReach(ctx context.Context, declared, escalated opsurface.Reach) {
	rec, _ := ctx.Value(reachRecorderKey{}).(*reachRecorder)
	if rec == nil {
		return
	}
	rec.declared = declared
	if escalated != declared {
		rec.escalated = escalated
	}
}

// The reserved metadata names carrying a request's trace context. They are the
// W3C names, spelled as that specification spells them, and they are read from
// the same metadata object every other reserved name is read from.
const (
	metaKeyTraceParent = "traceparent"
	metaKeyTraceState  = "tracestate"
	metaKeyBaggage     = "baggage"
)

// The bounds the trace-context specifications place on the two list-valued
// members. A value past either is not propagated, which is the specifications'
// own instruction rather than a policy of this server's.
const (
	traceStateMaxBytes = 512
	baggageMaxBytes    = 8192
)

// traceParentLength is how long a trace parent is: two characters of version, a
// thirty-two character trace identifier, a sixteen character parent identifier,
// two characters of flags, and the three separators between them.
const traceParentLength = 55

// readTraceContext reads the trace context a request carries, and returns
// nothing for a request that carries none or carries one that does not parse.
//
// Copying verbatim is the whole of what this server does with a trace context:
// it neither starts a trace, nor continues one, nor reads anything out of the
// identifiers. A collector joining this stream to a caller's own is what the
// values are for, so a value altered on the way through would join the two
// records to nothing.
//
// A value that does not parse is omitted rather than copied. That direction is
// deliberate: a malformed identifier copied into the line would be recorded as
// this request's trace context by whatever reads the stream, and joining on it
// would attribute this request to a trace it was never part of — which is worse
// than having no trace context at all. The two list-valued members are read only
// where the parent parses, because neither means anything without one.
func readTraceContext(params json.RawMessage) (parent, state, baggage string) {
	meta := readRequestMeta(params)
	if len(meta) == 0 {
		return "", "", ""
	}
	raw, present := metaString(meta, metaKeyTraceParent)
	if !present || !wellFormedTraceParent(raw) {
		return "", "", ""
	}
	if value, ok := metaString(meta, metaKeyTraceState); ok && wellFormedList(value, traceStateMaxBytes) {
		state = value
	}
	if value, ok := metaString(meta, metaKeyBaggage); ok && wellFormedList(value, baggageMaxBytes) {
		baggage = value
	}
	return raw, state, baggage
}

// wellFormedTraceParent reports whether a value is a trace parent this server
// will copy.
//
// The checks are the specification's own: the four fields in order, each of its
// fixed width and all hexadecimal; a version that is not the reserved
// all-ones value; and neither identifier all zeros, which that specification
// defines as the absence of an identifier rather than as one.
func wellFormedTraceParent(raw string) bool {
	if len(raw) != traceParentLength {
		return false
	}
	fields := strings.Split(raw, "-")
	if len(fields) != 4 {
		return false
	}
	version, traceID, parentID, flags := fields[0], fields[1], fields[2], fields[3]
	if len(version) != 2 || len(traceID) != 32 || len(parentID) != 16 || len(flags) != 2 {
		return false
	}
	for _, field := range fields {
		if !lowerHex(field) {
			return false
		}
	}
	if version == "ff" {
		return false
	}
	return !allZeros(traceID) && !allZeros(parentID)
}

// wellFormedList reports whether a value is one of the two comma-separated
// member lists this server will copy: within the bound its own specification
// places on it, and every member carrying the separator between a name and a
// value.
//
// It stops at the shape rather than validating each name and value against its
// own grammar. What the members mean is the caller's and the collector's
// business; what this checks is that the value is a list of members at all,
// which is what separates a trace context from something else that happened to
// be sent under the name.
func wellFormedList(raw string, maxBytes int) bool {
	if raw == "" || len(raw) > maxBytes {
		return false
	}
	for _, member := range strings.Split(raw, ",") {
		trimmed := strings.TrimSpace(member)
		name, value, separated := strings.Cut(trimmed, "=")
		if !separated || name == "" || value == "" {
			return false
		}
	}
	return true
}

// lowerHex reports whether every character is a lower-case hexadecimal digit.
// The trace-context identifiers are defined in lower case, and a value in upper
// case is a value that does not conform rather than one to be folded.
func lowerHex(s string) bool {
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// allZeros reports whether every character is the digit zero.
func allZeros(s string) bool {
	return strings.Count(s, "0") == len(s)
}
