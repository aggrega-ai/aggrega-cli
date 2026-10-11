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
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle"
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
	headerAuthorization   = "Authorization"
	headerContentType     = "Content-Type"
	headerCacheControl    = "Cache-Control"
	headerOrigin          = "Origin"
	headerAllow           = "Allow"
)

// contentTypeJSON is the media type an answer composed as one object carries,
// which is every answer bar the ones that reported progress on their way to it.
// A request is answered with one JSON object, with a stream of events ending in
// that object, or with nothing at all.
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

// httpAuthenticator decides whether a request may reach the protocol surface,
// and says who it is on behalf of where it may.
//
// It composes the challenge a refusal carries — the scheme is the
// authenticator's own and no other layer here can compose one for it — but it
// writes nothing: the answer goes out through the one route every answer this
// transport composes goes out through, so a refused caller receives a body
// framed exactly as every other answer is.
//
// The interface is unexported and so is the field holding it, which is what
// keeps an authenticator that admits anything out of reach from outside this
// package: there is no exported route by which a caller could install one.
type httpAuthenticator interface {
	// authenticate reports whether r carries the credentials this deployment
	// requires. It returns r carrying who the request is on behalf of where it
	// does, and the refusal to answer with where it does not.
	authenticate(r *http.Request) (*http.Request, *authRefusal)
}

// authRefusal is what a caller whose credential was not accepted receives: the
// status, the challenge it is invited to answer, and the sentence that says so.
//
// An empty challenge is a refusal that asks for nothing, which is the shape of
// the one condition here that is not about what the caller presented — this
// deployment being momentarily unable to verify anything at all.
//
// Nothing about the credential itself ever reaches this type. The message is
// composed from fixed text and the challenge from what the deployment declared
// about itself, so a token cannot travel back to the caller that sent it, into
// a log, or into anything an intermediary records.
//
// reason names, for whoever runs this deployment, which check turned the
// credential away. It travels here rather than being written where it is decided
// so that a refused request leaves ONE line rather than two: the record of the
// request carries the reason as a field of itself, and the check that found it
// writes nothing of its own. It is deliberately a distinct type from the message
// beside it, so putting one into the other does not compile.
//
// An empty reason is the refusal of a caller that presented NOTHING, and it is
// what keeps that case silent. Being challenged is how a client discovers where
// to obtain a token, so it is the first thing every well-behaved client does: a
// line for each of those would bury the refusals that mean something under the
// ones that mean a client is starting up, and would hand anything that can reach
// the endpoint an unbounded write to whatever collects this stream.
type authRefusal struct {
	status    int
	challenge string
	message   string
	reason    refusalReason
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

	// RequestsPerMinute is how many requests one caller may send in a minute.
	// It is counted against the caller the credential named rather than
	// against the deployment, so one caller cannot spend another's allowance,
	// and a request arriving with none left is refused rather than delayed.
	RequestsPerMinute int

	// Authorization is what this deployment declares about the bearer tokens its
	// callers present. Unlike the caps above it is not a bound on traffic: it is
	// what every answer given to a caller carrying no credential is composed
	// from, and what every token presented is held to, which is why a handler is
	// not built without it.
	Authorization AuthorizationConfig

	// DenyReach names the kinds of reach this deployment does not carry,
	// whatever a caller's credential asserts. A tool whose operation reaches
	// one of them is absent from every listing this handler answers and
	// refused at every call, naming the setting. Empty carries whatever the
	// callers' own scopes admit; a value outside the operation set's reach
	// vocabulary is refused rather than ignored, because an operator who
	// misspells a kind has asked for a bound and would otherwise be served
	// without one.
	DenyReach []string

	// Egress yields the client the authorization server's keys are read with.
	// It is consulted once, while the handler is built, and only where this
	// deployment reads a token at all and Authorization names no file to read
	// those keys from: a deployment that names a file, and one that reads its
	// caller from a proxy in front, never reach the network. A nil gate, or
	// one that refuses, is therefore a token-validating deployment with
	// nowhere to read its keys from, and the handler is refused rather than
	// built.
	Egress NetworkEgress
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
	// nil — newHTTPHandler installs the token validator, or the reader of a
	// trusted proxy's assertion where the deployment declared one — and a test
	// in this package replaces it to reach the transport behaviour behind it.
	auth httpAuthenticator

	// limiter bounds how fast one caller may send. It is claimed after the
	// credential check, because there is nobody to count against until a
	// credential has said who is asking.
	limiter *principalRateLimiter

	// resource is what this deployment publishes about itself: the document, the
	// paths it is answered at, and the challenge that points a refused caller at
	// it. Composed once at construction, because a caller that fetches the
	// document and a caller that is challenged must be told the same thing.
	resource protectedResource

	// progressKeepAlive is how long a stream carrying progress may sit idle
	// before a comment line is written on it. A test shortens it so a stall is a
	// property of the proof rather than of the clock; the production value, and
	// what it is chosen against, is stated where the constant is declared.
	progressKeepAlive time.Duration

	// workspaceRoot is the workspace this process serves, recorded on every
	// request answered here.
	//
	// It is resolved once, at construction, rather than read from the root the
	// process was launched with: a listener may have been started inside the
	// workspace it serves instead of being pointed at it, and the directory it was
	// launched in is not the root a reader of the record needs. Resolving it per
	// request would answer the same question repeatedly about a workspace fixed
	// when the process started, and would let two requests to one process report
	// two different roots.
	workspaceRoot string
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
// The handler it returns answers a request for what this deployment publishes
// about itself with no credential asked for, and every request to the protocol
// endpoint only where that request carries a bearer token the configured
// authorization server issued for this deployment. There is no exported route
// by which a caller could install an authenticator that admits anything.
func NewHTTPHandler(ctx context.Context, cfg Config, transport HTTPConfig) (http.Handler, error) {
	logw := cfg.Stderr
	if logw == nil {
		logw = os.Stderr
	}
	// The command surface is derived here and thrown away, which is the one
	// place this constructor reads it at all. What it yields is the vocabulary a
	// declared tool name is held to, never a tool this session serves: one
	// configuration file serves both transports, so a name that would have named
	// a generated tool has to be accepted here — it announces nothing on a
	// transport that carries none, exactly as a withdrawn kind no credential
	// reaches withdraws an already-empty set.
	announced, err := announcedToolNames(cfg.AnnounceTools, deriveCommandTools(cfg.Commands))
	if err != nil {
		return nil, err
	}
	s := &server{
		// Nothing frames a message on a stream in this mode. The field is set
		// rather than left nil so a write that reached it would be discarded
		// rather than crash the serving process.
		out:           io.Discard,
		logw:          logw,
		serverVersion: cfg.ServerVersion,
		toolchain:     cfg.Toolchain,
		// cfg.Commands is deliberately not derived from here. A session reached
		// over HTTP is composed with no command surface, and the composition is
		// applied at newHTTPHandler below rather than here, so that it covers
		// every server this handler is mounted on rather than only the ones this
		// constructor built.
		announcedTools: announced,
		renderHelp:     cfg.RenderHelp,
		launchRoot:     cfg.WorkspaceRoot,
		// Handed to this transport exactly as to the other: what the operator
		// typed when starting the process is in force for every call it answers,
		// whoever is calling.
		flags: cfg.Flags,
		// Handed to this transport exactly as to the other: which plugin a
		// reference resolves to is a property of the workspace and the process
		// rather than of how a caller reached it, so an importer that runs for a
		// client at this machine's keyboard runs for a caller reaching a listener
		// too, over the one workspace this process serves.
		resolveImportPlugin: cfg.ResolveImportPlugin,
		// Handed over on the same terms, and the withholding this reading applies
		// is the answer to the difference rather than a reason to withhold the
		// enumeration itself: which plugins a workspace resolves is a property of
		// the workspace, and what a caller reaching a listener is not told about
		// them is decided where the answer is composed.
		listPlugins: cfg.ListPlugins,
		// Marked here as well as at the single construction point below, and the
		// reason is the acquisition on the next line: it reports its outcome in
		// the form this session's transport fixes, and it runs before that point
		// is reached, so a server still carrying the zero value would report the
		// one thing it does before serving in the form of the other transport.
		// The mark below is unchanged and is still what covers every server this
		// handler is mounted on, however it was built.
		transport: transportHTTP,
	}
	s.docsRoot, s.docsRootErr = docsbundle.CacheRoot(ctx)
	s.ensureDocsBundle(ctx)
	return newHTTPHandler(ctx, s, transport)
}

// newHTTPHandler wires a handler onto an already-built server. It is the single
// construction point, so the caps are checked once, the token validator is
// installed once, and the session is marked as one reached over HTTP once,
// however the handler was reached.
//
// The mark is set here rather than beside the server the exported constructor
// builds because this is the point no HTTP path bypasses: a caller that builds a
// server itself and mounts a handler on it is serving that server over HTTP just
// as much as one that went through the constructor, and a session that answered
// requests while still claiming to speak the process's own streams would grant
// those callers what only a caller at this machine's keyboard may have.
//
// The command surface is dropped here for exactly that reason, and it is dropped
// rather than filtered. A generated tool builds a command line out of the values
// a call sent and runs it in a separate process, so every value it carries
// reaches the host as the command reads it — a directory to search, a file to
// write — and nothing between this server and that process can hold one inside
// the workspace this process serves. A caller at this machine's keyboard names
// such a location freely, because it is naming its own machine; a caller
// reaching a listener is naming somebody else's. Dropping the surface is what
// makes that structural: the session derives no generated tool, so there is
// none to advertise, none to call, and no argument of one to police. A rule
// written as a list of refused argument names would instead have to grow an
// entry for every flag a command ever gains that reaches the filesystem.
//
// The deployment's own withdrawal is read here for the third time on the same
// ground: it is a setting on the block that configures a listener, so installing
// it at this point is what makes a session speaking the process's own streams
// carry none of it without anything having to ask which transport it is on.
func newHTTPHandler(runCtx context.Context, s *server, transport HTTPConfig) (*httpHandler, error) {
	if transport.MaxBodyBytes <= 0 {
		return nil, fmt.Errorf("mcp.limits.max_body_bytes must be positive, got %d", transport.MaxBodyBytes)
	}
	if transport.MaxConcurrentRequests <= 0 {
		return nil, fmt.Errorf("mcp.limits.max_concurrent_requests must be positive, got %d",
			transport.MaxConcurrentRequests)
	}
	if transport.RequestsPerMinute <= 0 {
		return nil, fmt.Errorf("mcp.limits.requests_per_minute must be positive, got %d",
			transport.RequestsPerMinute)
	}
	// Nothing on this transport records an entry in the registry — the connection
	// is what cancels here — but a cancellation notification may still arrive over
	// it, and the notification path routes the id it names to the registry's
	// pickup. Leaving the field nil would turn that permitted no-op into a crash.
	if s.cancels == nil {
		s.cancels = requestcancel.New()
	}
	denied, err := deniedClasses(transport.DenyReach)
	if err != nil {
		return nil, err
	}
	resource, err := resolveProtectedResource(transport.Authorization)
	if err != nil {
		return nil, err
	}
	auth, err := resolveAuthenticator(runCtx, transport, resource)
	if err != nil {
		return nil, err
	}
	s.transport = transportHTTP
	s.commandTools = nil
	s.deniedReach = denied
	return &httpHandler{
		srv:               s,
		runCtx:            runCtx,
		allowedOrigins:    slices.Clone(transport.AllowedOrigins),
		maxBodyBytes:      transport.MaxBodyBytes,
		slots:             make(chan struct{}, transport.MaxConcurrentRequests),
		auth:              auth,
		limiter:           newPrincipalRateLimiter(transport.RequestsPerMinute),
		resource:          resource,
		progressKeepAlive: progressKeepAlive,
		workspaceRoot:     servedWorkspaceRoot(s),
	}, nil
}

// servedWorkspaceRoot is the workspace this process serves, for the record it
// writes about each request.
func servedWorkspaceRoot(s *server) string {
	return s.resolveServedWorkspace().root
}

// resolveServedWorkspace resolves the workspace this process serves, exactly as
// a tool call naming no workspace resolves it.
//
// A resolution that fails falls back to the root the process was launched with,
// which is the honest answer rather than an empty one: the launch already
// refuses a listener with no workspace to serve, so a failure here is a
// workspace that has stopped resolving since, and the directory it was pointed
// at is what a reader can act on. The workspace returned then holds that
// directory as its root, as the process was given it, and nothing else.
//
// A process given no directory began its resolution in its working directory,
// so that directory is the one held. Left empty, the fallback would hold nothing,
// and an answer read against it would pass on any mention of the working
// directory exactly as it was composed.
func (s *server) resolveServedWorkspace() *toolWorkspace {
	if tw, err := s.resolveToolWorkspace("", nil); err == nil {
		return tw
	}
	root := s.launchRoot
	if root == "" {
		if wd, err := os.Getwd(); err == nil {
			root = wd
		}
	}
	return &toolWorkspace{root: root, transport: s.transport}
}

// resolveAuthenticator settles which credential this deployment reads, and
// reaches for the verification keys only where it reads a token.
//
// The two are one decision rather than two, which is why they are settled here
// together: a deployment reading its caller from a proxy in front validates no
// token, so fetching a key set for it would reach an authorization server whose
// answer nothing would ever consult — and would refuse a workspace that permits
// no outbound request over keys it was never going to read.
//
// ctx is whoever asked for this deployment, and it is carried through to where
// the verification keys are settled, because reaching for a key set is the one
// step here that reads from outside this process.
//
// Nothing built here writes to the log stream. The reason a credential was
// turned away travels back on the refusal itself and is recorded as a field of
// that request's own record, which is what keeps a refused request to one line.
func resolveAuthenticator(ctx context.Context, transport HTTPConfig,
	resource protectedResource) (httpAuthenticator, error) {
	proxy, err := resolveProxyAssertion(transport.Authorization, resource)
	if err != nil {
		return nil, err
	}
	if proxy != nil {
		return proxy, nil
	}
	keys, err := resolveVerificationKeys(ctx, transport.Authorization, transport.Egress)
	if err != nil {
		return nil, err
	}
	return &bearerValidator{
		resource: resource,
		issuer:   transport.Authorization.Issuer,
		audience: transport.Authorization.Resource,
		keys:     keys,
	}, nil
}

// ServeHTTP answers one JSON-RPC message, or hands out what this deployment
// publishes about itself.
//
// The order the gates run in is the contract, not an implementation detail:
//
//   - A request for the document this deployment publishes about itself is
//     answered first, and it is the one answer given with no credential asked
//     for: a client reads it to find out where to obtain a token, so a
//     challenge there would close the only door into the deployment.
//   - The path and the method are settled next, so a caller probing for paths
//     is told a path does not exist rather than being challenged for a
//     credential it would then discover is useless there.
//   - Authentication runs after that, ahead of everything a request can
//     influence. Every refusal after this point therefore reaches an
//     authenticated caller only, which is what makes the unauthenticated answer
//     to this endpoint a single one however the request was shaped. What the
//     credential asserted travels on the request from here, so everything
//     answered afterwards is answered on behalf of someone.
//   - The origin, the per-caller rate cap, the concurrency slot and the body cap
//     follow, in that order: an unlisted origin costs a header read, a slot is
//     held only for work that is going to be done, and the body is the first
//     thing a caller can make large. The rate cap sits where it does for two
//     reasons of its own. It cannot run before authentication, because it counts
//     against a caller and there is nobody to count against until a credential
//     has said who is asking. And it runs before the slot rather than after,
//     because a request that is going to be refused for sending too fast is not
//     work that is going to be done, so taking a slot for it first would let one
//     capped caller displace a caller whose request was going to be answered.
//   - The body is then read and parsed, its era established, and its headers
//     checked against it, before anything is routed.
func (h *httpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.resource.servesMetadataAt(r.URL.Path) {
		h.serveMetadata(w, r)
		return
	}
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
	h.serveEndpoint(w, r)
}

// serveEndpoint answers one message on the protocol endpoint and records what
// became of it.
//
// It is split from the routing above so that the record covers exactly the
// requests it is a record OF. The two answers above it are settled before
// anything about a caller is read — a path this deployment does not serve, and a
// method this endpoint does not take — so neither is a request to this server's
// protocol surface, and recording them would turn a port scan into an unbounded
// write to whatever collects the stream.
//
// Every gate below writes into the same record, and the record is written once,
// when this returns, whatever gate the request ended at. That is what makes
// "one line per request" a property of the function rather than of whoever adds
// the next gate: a gate that returns without writing a line would have to remove
// the deferred write to do it.
func (h *httpHandler) serveEndpoint(w http.ResponseWriter, r *http.Request) {
	rec := requestAudit{
		Time:          auditNow(),
		RequestID:     mintRequestID(),
		Transport:     transportName(transportHTTP),
		WorkspaceRoot: h.workspaceRoot,
		Outcome:       outcomeError,
	}
	started := time.Now()
	// written is read at the deferred write and set by the one gate that has a
	// reason to suppress the line. It starts true so a gate added later is
	// recorded rather than silently absent.
	written := true
	defer func() {
		if !written {
			return
		}
		rec.DurationMs = time.Since(started).Milliseconds()
		h.srv.logRecord(rec)
	}()

	authenticated, refusal := h.auth.authenticate(r)
	if refusal != nil {
		// A caller that presented nothing named no check and leaves no line. It
		// is being told where to obtain a credential rather than being turned
		// away for something it did, and that answer is every well-behaved
		// client's first exchange with this deployment.
		written = refusal.reason != ""
		rec.Refusal = string(refusal.reason)
		rec.Outcome = outcomeForStatus(refusal.status, false)
		if refusal.challenge != "" {
			w.Header().Set(headerAuthenticate, refusal.challenge)
		}
		h.refuse(w, refusal.status, codeInvalidRequest, refusal.message)
		return
	}
	r = authenticated
	rec.recordPrincipal(principalFrom(r.Context()))

	if origin := r.Header.Get(headerOrigin); !h.originAllowed(origin) {
		rec.Outcome = outcomeForStatus(http.StatusForbidden, false)
		h.refuse(w, http.StatusForbidden, codeInvalidRequest, "origin not allowed: "+origin)
		return
	}
	if !h.limiter.allow(principalFrom(r.Context())) {
		rec.Outcome = outcomeForStatus(http.StatusTooManyRequests, false)
		h.refuse(w, http.StatusTooManyRequests, codeInternalError,
			"this caller has sent more requests than this server accepts from one caller in a minute; "+
				"the allowance refills steadily, so the next request will be answered shortly")
		return
	}
	release, taken := h.takeSlot()
	if !taken {
		rec.Outcome = outcomeForStatus(http.StatusServiceUnavailable, false)
		h.refuse(w, http.StatusServiceUnavailable, codeInternalError,
			"the server is already answering as many requests as it accepts at once")
		return
	}
	defer release()

	body, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, h.maxBodyBytes))
	if readErr != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(readErr, &tooLarge) {
			rec.Outcome = outcomeForStatus(http.StatusRequestEntityTooLarge, false)
			h.refuse(w, http.StatusRequestEntityTooLarge, codeInvalidRequest,
				fmt.Sprintf("request body exceeds the %d bytes this server accepts", h.maxBodyBytes))
			return
		}
		rec.Outcome = outcomeForStatus(http.StatusBadRequest, false)
		h.refuse(w, http.StatusBadRequest, codeParseError, "read request body: "+readErr.Error())
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		rec.Outcome = outcomeForStatus(http.StatusBadRequest, false)
		h.refuse(w, http.StatusBadRequest, codeParseError, "parse error: "+err.Error())
		return
	}
	if req.Method == "" {
		// A JSON-RPC response carries a result or an error and no method, and the
		// protocol asks a client never to send one. Anything else carrying no
		// method is not a message this server can act on either, so one answer
		// covers both rather than two that a caller could not tell apart.
		rec.Outcome = outcomeForStatus(http.StatusBadRequest, false)
		h.refuse(w, http.StatusBadRequest, codeInvalidRequest,
			"this server answers requests and notifications; the body names no method")
		return
	}
	rec.recordRequest(&req)

	era, revision, protocolErr := readRequestProtocol(req.Params)
	if protocolErr != nil {
		status := statusForRefusal(protocolErr)
		rec.Outcome = outcomeForStatus(status, false)
		h.respondRefusal(w, status, req.ID, protocolErr)
		return
	}
	if headerErr := mirrorHeadersRefusal(r.Header, era, revision, &req); headerErr != nil {
		status := statusForRefusal(headerErr)
		rec.Outcome = outcomeForStatus(status, false)
		h.respondRefusal(w, status, req.ID, headerErr)
		return
	}

	if len(req.ID) == 0 {
		h.srv.handleNotification(&req)
		rec.Outcome = outcomeForStatus(http.StatusAccepted, false)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	h.answer(w, r, era, &req, &rec)
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
//
// rec is this request's own record, and this is where the three facts only the
// routing knows reach it: how far the call was admitted to reach, whether the
// caller was still there to be answered, and what the answer turned out to be.
// The reach classes are carried on the context rather than returned, because
// they are decided several layers down and nothing between here and there has
// any use for them; the recorder is written by the dispatch this call makes and
// read after it returns, so the call itself orders the two.
//
// # What the record spans when the answer is a stream
//
// This returns once the answer is written, which for a call that reported
// progress is once the stream's last event is written rather than once the
// first one is. The record of the request is written by the caller above, after
// this returns, so the duration it reports spans the whole exchange either way
// and the outcome it reports is the one the exchange ended at. That is the
// property to hold on to: an outcome decided when the stream OPENED would
// report a call as answered while the work it narrates is still running, and
// the two would be indistinguishable in the record.
//
// The outcome for a streamed refusal is still read from the status the refusal
// earned, even though the bytes left here under a status of two hundred: the
// outcomes name what became of a call rather than the number that carried it,
// and a refusal that had to travel as an event is the same refusal.
//
// # The stream, and what closes it
//
// The stream is watched against the CONNECTION rather than against the composed
// context above. A caller that disconnects has ended the exchange, so the
// stream closes and nothing more is written on it. A run that ends has not: the
// caller is still connected and still reading, so the answer it was waiting for
// is still written — which is the same rule the paragraphs above keep for the
// single-object answer.
func (h *httpHandler) answer(w http.ResponseWriter, r *http.Request, era protocolEra,
	req *rpcRequest, rec *requestAudit) {
	ctx, cancel := context.WithCancelCause(r.Context())
	defer cancel(nil)
	stopWatchingRun := context.AfterFunc(h.runCtx, func() { cancel(errRunEnded) })
	defer stopWatchingRun()

	stream := newProgressStream(w, h.progressKeepAlive)
	defer stream.close()
	defer stream.stopOnContext(r.Context())()
	ctx = withProgressSink(ctx, stream.emit)

	var reached reachRecorder
	ctx = withReachRecorder(ctx, &reached)

	result, rpcErr := h.srv.dispatchInEra(ctx, era, req)
	rec.recordReach(&reached)

	if r.Context().Err() != nil {
		rec.Outcome = outcomeCancelled
		return
	}
	if rpcErr != nil {
		status := statusForRefusal(rpcErr)
		rec.Outcome = outcomeForStatus(status, false)
		if !stream.finish(&rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: rpcErr}) {
			h.respondRefusal(w, status, req.ID, rpcErr)
		}
		return
	}
	rec.Outcome = outcomeForStatus(http.StatusOK, toolReportedFailure(result))
	if !stream.opened() {
		h.respondResult(w, req.ID, result)
		return
	}
	resultBytes, err := json.Marshal(result)
	if err != nil {
		stream.finish(&rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{
			Code: codeInternalError, Message: "marshal result: " + err.Error()}})
		return
	}
	stream.finish(&rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: resultBytes})
}

// toolReportedFailure reports whether an answered call came back as a tool
// result the caller can correct.
//
// It is the one thing the record reads out of a result, and it reads it off the
// result's own flag rather than out of its content: the flag is what the
// protocol defines for it and what the client renders from, so a record deciding
// it any other way would report something the caller was never told.
func toolReportedFailure(result any) bool {
	res, isToolResult := result.(*callToolResult)
	return isToolResult && res.IsError
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

// statusInsufficientScope is what a caller whose credential did not admit what
// it asked for receives. It is the status the protocol's authorization design
// names for that case, and it is distinct from the one an absent or unusable
// credential earns: the caller is known and its credential is good, and what it
// asked for is outside what that credential carries, so obtaining a new token
// with a wider grant is the correction rather than authenticating again.
const statusInsufficientScope = http.StatusForbidden

// statusForRefusal is the status that carries a protocol refusal.
//
// It maps the protocol's own answer to the transport's, so a caller that reads
// only the status still learns what a caller that parses the envelope learns: a
// method this server does not serve is not-found, a failure of the server's own
// is a server error, and everything else is something about the request the
// caller can correct.
//
// A refusal that named its own status is carried under that instead. The mapping
// reads a code, and one code covers every request fault a caller can correct;
// where the status is a property of the refusal rather than of its code — a
// credential that does not admit what was asked for is the one such refusal
// today — the site that composed it is the only one that knows, and says so.
//
// The refusals this transport composes for itself do not come through here.
// Those are conditions of the transport rather than of the protocol — an
// unlisted origin, a body past the cap, no free slot — and each names its own
// status where it is raised.
func statusForRefusal(env *rpcError) int {
	if env.httpStatus != 0 {
		return env.httpStatus
	}
	switch env.Code {
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
//
// A refusal naming the scope that would have admitted the call carries the
// challenge that invites the caller to obtain one. It is composed here, from
// what this deployment publishes about itself, rather than by the handler that
// composed the refusal: that handler knows which scope is missing and nothing
// about where a token is obtained, and a challenge built anywhere but from the
// published document could point at an address this deployment never serves.
func (h *httpHandler) respondRefusal(w http.ResponseWriter, status int, id json.RawMessage, env *rpcError) {
	if env.insufficientScope != "" {
		w.Header().Set(headerAuthenticate,
			h.resource.challengeWith(env.insufficientScope, authErrorInsufficientScope))
	}
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
