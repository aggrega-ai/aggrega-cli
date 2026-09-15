// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/requestcancel"
)

// endpointPath is the one path this transport serves. Every other path is
// answered not-found, and that answer is settled before authentication, so a
// caller probing for paths learns nothing from a challenge on a path that does
// not exist.
const endpointPath = "/mcp"

// The headers the per-request revision asks a request to mirror its body in, so
// an intermediary can route a message without parsing its body.
//
// They are spelled here as the foreign protocol spells them. Header names match
// case-insensitively on the wire and net/http canonicalises them on the way in,
// so the casing below is documentation rather than a matching rule.
const (
	headerProtocolVersion = "MCP-Protocol-Version"
	headerMethod          = "Mcp-Method"
	headerName            = "Mcp-Name"
	headerAuthenticate    = "WWW-Authenticate"
	headerContentType     = "Content-Type"
	headerOrigin          = "Origin"
	headerAllow           = "Allow"
)

// contentTypeJSON is the media type every answer this transport composes
// carries. An event stream is a different transport shape and this one never
// negotiates into it: a request is answered with one JSON object or with
// nothing at all.
const contentTypeJSON = "application/json"

// The wrapper the per-request revision defines for a mirrored name whose bytes
// do not travel safely in a header field: the prefix, the Base64 of the name,
// then the suffix. A value in that form is decoded before it is compared to the
// body; any other value is the name itself.
const (
	nameSentinelPrefix = "=?base64?"
	nameSentinelSuffix = "?="
)

// errRunEnded is the cause recorded on an HTTP request's context when the run
// the listener belongs to ends while that request is still being answered.
//
// Recording a cause is what lets a reader of that context tell the run ending
// from the caller going away, and the two are answered differently: a run that
// ends still answers the request it was answering, while a caller whose
// connection has closed will not read an answer and is written none.
var errRunEnded = errors.New("the serving process is shutting down")

// httpAuthenticator decides whether a request may reach the protocol surface.
//
// A refusal composes its own response — the status and whatever challenge its
// scheme asks a caller to answer — because the scheme is the authenticator's
// own and no other layer here can compose one for it.
//
// The interface is unexported and so is the field holding it, which is what
// makes the refusing value below the only one reachable from outside this
// package: there is no exported route by which a caller could install an
// authenticator that admits anything.
type httpAuthenticator interface {
	// authenticate reports whether r carries the credentials this deployment
	// requires. It writes the refusal itself and returns false when r does not.
	authenticate(w http.ResponseWriter, r *http.Request) bool
}

// bearerChallenge refuses every request with the challenge a bearer-token
// deployment answers.
//
// It is the value this transport ships with, and the refusal is unconditional:
// nothing is served to anyone until a validator that can tell one token from
// another replaces it. A transport that shipped serving requests while its
// credential check was still being built would be reachable without a token for
// as long as that took, which is the window this value closes.
type bearerChallenge struct{}

func (bearerChallenge) authenticate(w http.ResponseWriter, _ *http.Request) bool {
	w.Header().Set(headerAuthenticate, "Bearer")
	w.WriteHeader(http.StatusUnauthorized)
	return false
}

// HTTPConfig carries what the HTTP transport applies to the traffic it accepts,
// on top of the server configuration both transports share.
//
// The values are the deployment's resolved configuration rather than defaults
// to be filled in here: each key's default is stated in the configuration
// schema's description for that key and nowhere else, so a second statement of
// one in this package would be a copy that drifts. A non-positive cap is
// therefore a caller that resolved nothing, and the constructor refuses it.
type HTTPConfig struct {
	// AllowedOrigins are the origins accepted on a request's Origin header. A
	// request naming an origin absent from this list is refused; one naming a
	// listed origin, or carrying no Origin header, is served. Empty accepts only
	// callers that send no Origin header, which is the shape a client that is not
	// a browser takes.
	AllowedOrigins []string

	// MaxBodyBytes is the largest request body accepted. A larger body is
	// refused, and is not read past the cap.
	MaxBodyBytes int64

	// MaxConcurrentRequests is how many requests are answered at once. One
	// arriving while that many are in flight is refused rather than queued.
	MaxConcurrentRequests int
}

// httpHandler answers the protocol over HTTP: one endpoint, one JSON-RPC
// message per request, no session of any kind.
//
// It holds the same server the stdio transport drives and reaches the same
// route from a parsed request to an answer. What it does not share is the
// framing: the stdio writer puts newline-delimited objects on one stream under
// a mutex, while an answer here is one object in a response body with a status
// code, written by the goroutine net/http gave the request. Framing is the
// transport's own; routing is not.
type httpHandler struct {
	srv *server

	// runCtx is the serving process's own context. Every request's context is
	// watched against it, so ending the run ends the work a request is doing
	// rather than only declining to accept the next one.
	runCtx context.Context

	allowedOrigins []string
	maxBodyBytes   int64

	// slots bounds how many requests are answered at once. A send that cannot
	// proceed is a refusal rather than a wait: a caller told to come back is
	// better served than one held on a connection until something frees up.
	slots chan struct{}

	// auth decides whether a request reaches the protocol surface. It is never
	// nil — newHTTPHandler installs the refusing value — and a test in this
	// package replaces it to reach the transport behaviour behind it.
	auth httpAuthenticator
}

// NewHTTPHandler builds the handler that serves the protocol over HTTP, and
// acquires the documentation bundle once before it answers anything.
//
// ctx is the serving process's own: it reaches the acquisition and every
// request answered afterwards, so ending it ends the work in flight.
//
// cfg is the configuration both transports share. Stdin and Stdout are ignored
// here — no message is framed on a stream in this mode — while Stderr carries
// the log lines it carries on stdio.
//
// The handler it returns refuses every request with a bearer challenge. An
// authenticator that can tell one token from another replaces that value inside
// this package; there is no exported route by which a caller could install one
// that admits anything, which is what makes this transport unreachable without
// a credential until such a validator exists.
func NewHTTPHandler(ctx context.Context, cfg Config, transport HTTPConfig) (http.Handler, error) {
	logw := cfg.Stderr
	if logw == nil {
		logw = os.Stderr
	}
	s := &server{
		// Nothing frames a message on a stream in this mode. The field is set
		// rather than left nil so a write that reached it would be discarded
		// rather than crash the serving process.
		out:           io.Discard,
		logw:          logw,
		serverVersion: cfg.ServerVersion,
		toolchain:     cfg.Toolchain,
		commandTools:  deriveCommandTools(cfg.Commands),
		renderHelp:    cfg.RenderHelp,
		startDir:      cfg.WorkspaceRoot,
	}
	s.ensureDocsBundle(ctx)
	return newHTTPHandler(ctx, s, transport)
}

// newHTTPHandler wires a handler onto an already-built server. It is the single
// construction point, so the caps are checked once and the refusing
// authenticator is installed once however the handler was reached.
func newHTTPHandler(runCtx context.Context, s *server, transport HTTPConfig) (*httpHandler, error) {
	if transport.MaxBodyBytes <= 0 {
		return nil, fmt.Errorf("mcp.limits.max_body_bytes must be positive, got %d", transport.MaxBodyBytes)
	}
	if transport.MaxConcurrentRequests <= 0 {
		return nil, fmt.Errorf("mcp.limits.max_concurrent_requests must be positive, got %d",
			transport.MaxConcurrentRequests)
	}
	// Nothing on this transport records an entry in the registry — the connection
	// is what cancels here — but a cancellation notification may still arrive over
	// it, and the notification path routes the id it names to the registry's
	// pickup. Leaving the field nil would turn that permitted no-op into a crash.
	if s.cancels == nil {
		s.cancels = requestcancel.New()
	}
	return &httpHandler{
		srv:            s,
		runCtx:         runCtx,
		allowedOrigins: slices.Clone(transport.AllowedOrigins),
		maxBodyBytes:   transport.MaxBodyBytes,
		slots:          make(chan struct{}, transport.MaxConcurrentRequests),
		auth:           bearerChallenge{},
	}, nil
}

// ServeHTTP answers one JSON-RPC message.
//
// The order the gates run in is the contract, not an implementation detail:
//
//   - The path and the method are settled first, so a caller probing for paths
//     is told a path does not exist rather than being challenged for a
//     credential it would then discover is useless there.
//   - Authentication runs next, ahead of everything a request can influence.
//     Every refusal after this point therefore reaches an authenticated caller
//     only, which is what makes the unauthenticated answer to this endpoint a
//     single one however the request was shaped.
//   - The origin, the concurrency slot and the body cap follow, in that order:
//     an unlisted origin costs a header read, a slot is held only for work that
//     is going to be done, and the body is the first thing a caller can make
//     large.
//   - The body is then read and parsed, its era established, and its headers
//     checked against it, before anything is routed.
func (h *httpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != endpointPath {
		// No body: an answer here would only describe a path that does not exist.
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set(headerAllow, http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !h.auth.authenticate(w, r) {
		return
	}
	if origin := r.Header.Get(headerOrigin); !h.originAllowed(origin) {
		h.refuse(w, http.StatusForbidden, codeInvalidRequest, "origin not allowed: "+origin)
		return
	}
	release, taken := h.takeSlot()
	if !taken {
		h.refuse(w, http.StatusServiceUnavailable, codeInternalError,
			"the server is already answering as many requests as it accepts at once")
		return
	}
	defer release()

	body, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, h.maxBodyBytes))
	if readErr != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(readErr, &tooLarge) {
			h.refuse(w, http.StatusRequestEntityTooLarge, codeInvalidRequest,
				fmt.Sprintf("request body exceeds the %d bytes this server accepts", h.maxBodyBytes))
			return
		}
		h.refuse(w, http.StatusBadRequest, codeParseError, "read request body: "+readErr.Error())
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.refuse(w, http.StatusBadRequest, codeParseError, "parse error: "+err.Error())
		return
	}
	if req.Method == "" {
		// A JSON-RPC response carries a result or an error and no method, and the
		// protocol asks a client never to send one. Anything else carrying no
		// method is not a message this server can act on either, so one answer
		// covers both rather than two that a caller could not tell apart.
		h.refuse(w, http.StatusBadRequest, codeInvalidRequest,
			"this server answers requests and notifications; the body names no method")
		return
	}

	era, revision, protocolErr := readRequestProtocol(req.Params)
	if protocolErr != nil {
		h.respondRefusal(w, statusForRefusal(protocolErr.Code), req.ID, protocolErr)
		return
	}
	if headerErr := mirrorHeadersRefusal(r.Header, era, revision, &req); headerErr != nil {
		h.respondRefusal(w, statusForRefusal(headerErr.Code), req.ID, headerErr)
		return
	}

	if len(req.ID) == 0 {
		h.srv.handleNotification(&req)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	h.answer(w, r, era, &req)
}

// answer routes one id-bearing request and writes what it produced, unless the
// caller stopped waiting for it.
//
// The request's context is the connection's — the one net/http ends when that
// connection closes — with the run's end composed onto it, so a stop reaches the
// work a request is doing rather than only declining to accept the next request.
// Those two are the whole of the cancellation here, and the connection is the
// channel the protocol itself gives this transport: closing the response stream
// is the cancellation signal, a server treats a caller's disconnect as
// cancellation of that request, and no cancellation notification is expected for
// it. So nothing is registered by id on this path. The id-keyed registry belongs
// to stdio, where there is no per-request stream to close and a notification
// naming the id is the only way a caller ends one request and leaves the rest
// running; a cancellation that does arrive here still reaches that registry's
// pickup and finds nothing, which is the no-op the protocol permits for an id
// nothing is working on.
//
// The run ending does not suppress the answer: the caller is still connected and
// still reading, so the request that was being answered is answered. A caller
// that has gone is written nothing at all, which leaves the exchange to be
// terminated with no protocol message — this transport's form of the rule the
// stdio transport keeps by dropping the line it would have framed.
func (h *httpHandler) answer(w http.ResponseWriter, r *http.Request, era protocolEra, req *rpcRequest) {
	ctx, cancel := context.WithCancelCause(r.Context())
	defer cancel(nil)
	stopWatchingRun := context.AfterFunc(h.runCtx, func() { cancel(errRunEnded) })
	defer stopWatchingRun()

	result, rpcErr := h.srv.dispatchInEra(ctx, era, req)

	if r.Context().Err() != nil {
		return
	}
	if rpcErr != nil {
		h.respondRefusal(w, statusForRefusal(rpcErr.Code), req.ID, rpcErr)
		return
	}
	h.respondResult(w, req.ID, result)
}

// originAllowed reports whether a request naming this origin is served. A
// request carrying no Origin header is served: the header is what a browser
// attaches, and its absence is the shape of every client that is not one.
//
// The comparison is exact. An origin is a scheme, a host and an optional port
// with no path, so there is nothing here to normalise that a caller has not
// already normalised — and a looser comparison would admit an origin the
// deployment did not list.
func (h *httpHandler) originAllowed(origin string) bool {
	return origin == "" || slices.Contains(h.allowedOrigins, origin)
}

// takeSlot claims one of the concurrency slots and returns the release for it.
// A claim that cannot proceed reports false rather than waiting, so the cap
// bounds what is being worked on rather than what is queued behind it.
func (h *httpHandler) takeSlot() (release func(), taken bool) {
	select {
	case h.slots <- struct{}{}:
		return func() { <-h.slots }, true
	default:
		return nil, false
	}
}

// statusForRefusal is the status that carries a protocol refusal.
//
// It maps the protocol's own answer to the transport's, so a caller that reads
// only the status still learns what a caller that parses the envelope learns: a
// method this server does not serve is not-found, a failure of the server's own
// is a server error, and everything else is something about the request the
// caller can correct.
//
// The refusals this transport composes for itself do not come through here.
// Those are conditions of the transport rather than of the protocol — an
// unlisted origin, a body past the cap, no free slot — and each names its own
// status where it is raised.
func statusForRefusal(code int) int {
	switch code {
	case codeMethodNotFound:
		return http.StatusNotFound
	case codeInternalError:
		return http.StatusInternalServerError
	default:
		return http.StatusBadRequest
	}
}

// mirrorHeadersRefusal checks a request's headers against its body and returns
// the refusal it earned, or nil.
//
// The requirements are era-scoped, because the two revisions ask for different
// things. A request in the per-request revision must carry the revision it
// states in its body, the method it names, and — for the methods that address
// something by name — that name. A handshake-revision request states its
// revision nowhere, so the header is optional there and is only held to naming
// the revision that handshake defines; the other two headers belong to a
// revision that request is not speaking and are left alone, exactly as a
// session or stream-resumption header is.
func mirrorHeadersRefusal(header http.Header, era protocolEra, revision string, req *rpcRequest) *rpcError {
	if era != eraPerRequest {
		return handshakeVersionHeaderRefusal(header.Get(headerProtocolVersion))
	}
	if got := header.Get(headerProtocolVersion); got != revision {
		return headerMismatch(fmt.Sprintf(
			"%s header is %q and the request body states %q", headerProtocolVersion, got, revision))
	}
	if got := header.Get(headerMethod); got != req.Method {
		return headerMismatch(fmt.Sprintf(
			"%s header is %q and the request body names method %q", headerMethod, got, req.Method))
	}
	return mirroredNameRefusal(header.Get(headerName), req)
}

// handshakeVersionHeaderRefusal checks the one header a handshake-revision
// request may carry.
//
// An absent header is the ordinary case and earns nothing. A header naming a
// revision this server does not speak earns the refusal that names what it does
// speak, composed where every such refusal is composed. A header naming the
// other revision this server does speak is a different fault — the body is not
// in that revision — and is reported as the mismatch it is.
func handshakeVersionHeaderRefusal(got string) *rpcError {
	switch {
	case got == "":
		return nil
	case !speaksRevision(got):
		return unsupportedRevision(got)
	case got != handshakeRevision:
		return headerMismatch(fmt.Sprintf(
			"%s header is %q and the request body carries no per-request metadata, so it is a %s request",
			headerProtocolVersion, got, handshakeRevision))
	default:
		return nil
	}
}

// mirroredNameRefusal checks the name header against whatever the request
// addresses, for the methods that address something by name.
//
// A body whose params cannot be read is not refused here. The header's presence
// is still required, because that is readable without the body, but there is
// nothing to compare it against — and the method's own handler refuses params it
// cannot read with the answer that names them, which is the more useful one.
func mirroredNameRefusal(got string, req *rpcRequest) *rpcError {
	target, addresses := addressedName(req)
	if !addresses {
		return nil
	}
	decoded, wellFormed := decodeMirroredName(got)
	if !wellFormed {
		return headerMismatch(fmt.Sprintf(
			"%s header is absent or malformed and %s addresses a target by name", headerName, req.Method))
	}
	if target != "" && decoded != target {
		return headerMismatch(fmt.Sprintf(
			"%s header is %q and the request body addresses %q", headerName, decoded, target))
	}
	return nil
}

// addressedName reports what a request addresses by name, and whether the
// method addresses anything that way at all.
//
// The three methods below each carry their target in their own params field,
// which is why this reads the body rather than taking a name from the caller: a
// tool and a prompt are addressed by name and a resource by URI, and the header
// mirrors whichever of them the method uses. A target that cannot be read comes
// back empty, leaving the comparison to be skipped rather than failed.
func addressedName(req *rpcRequest) (target string, addresses bool) {
	switch req.Method {
	case "tools/call":
		var p callToolParams
		_ = json.Unmarshal(req.Params, &p)
		return p.Name, true
	case "prompts/get":
		var p getPromptParams
		_ = json.Unmarshal(req.Params, &p)
		return p.Name, true
	case "resources/read":
		var p readResourceParams
		_ = json.Unmarshal(req.Params, &p)
		return p.URI, true
	default:
		return "", false
	}
}

// decodeMirroredName reads a name header, decoding the wrapper the revision
// defines for a name whose bytes do not travel safely in a header field.
//
// A value wrapped in that form and carrying something that is not Base64 is
// reported as ill-formed, because the caller stated an encoding and then did
// not use it. A value that merely resembles the wrapper without being one — too
// short to hold both halves — is a name like any other: only a well-formed
// wrapper is treated as one.
func decodeMirroredName(raw string) (name string, wellFormed bool) {
	if raw == "" {
		return "", false
	}
	if len(raw) < len(nameSentinelPrefix)+len(nameSentinelSuffix) ||
		!strings.HasPrefix(raw, nameSentinelPrefix) ||
		!strings.HasSuffix(raw, nameSentinelSuffix) {
		return raw, true
	}
	encoded := raw[len(nameSentinelPrefix) : len(raw)-len(nameSentinelSuffix)]
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", false
	}
	return string(decoded), true
}

// headerMismatch builds the refusal the per-request revision allocates for a
// request whose transport headers do not mirror its body. It is the one place
// that code is built, so every mismatch this transport finds is reported the
// same way whichever header found it.
func headerMismatch(detail string) *rpcError {
	return &rpcError{Code: codeHeaderMismatch, Message: "request headers do not match the request body: " + detail}
}

// refuse answers with a refusal this transport composed for itself, carrying no
// id: the conditions that reach here are met before a request's id is known, or
// where the body it would have come from was never read.
func (h *httpHandler) refuse(w http.ResponseWriter, status, code int, message string) {
	h.respondRefusal(w, status, nil, &rpcError{Code: code, Message: message})
}

// respondRefusal answers with a pre-built error envelope under the given status,
// preserving whatever structured data the envelope carries.
func (h *httpHandler) respondRefusal(w http.ResponseWriter, status int, id json.RawMessage, env *rpcError) {
	h.respond(w, status, &rpcResponse{JSONRPC: "2.0", ID: id, Error: env})
}

// respondResult answers with a successful response carrying result.
func (h *httpHandler) respondResult(w http.ResponseWriter, id json.RawMessage, result any) {
	resultBytes, err := json.Marshal(result)
	if err != nil {
		h.respondRefusal(w, http.StatusInternalServerError, id,
			&rpcError{Code: codeInternalError, Message: "marshal result: " + err.Error()})
		return
	}
	h.respond(w, http.StatusOK, &rpcResponse{JSONRPC: "2.0", ID: id, Result: resultBytes})
}

// respond is the single route from every answer this transport composes to the
// response body, so every one of them carries the same media type and one JSON
// object with no framing around it.
//
// A marshal failure here has nowhere left to go: the status line is not yet
// written, but composing a second envelope would risk the same failure, so the
// failure is logged and the exchange is left to terminate with a server error.
func (h *httpHandler) respond(w http.ResponseWriter, status int, resp *rpcResponse) {
	b, err := json.Marshal(resp)
	if err != nil {
		h.srv.logf("mcp: marshal response: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set(headerContentType, contentTypeJSON)
	w.WriteHeader(status)
	if _, err := w.Write(b); err != nil {
		h.srv.logf("mcp: write response: %v", err)
	}
}
