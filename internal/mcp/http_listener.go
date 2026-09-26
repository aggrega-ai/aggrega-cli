// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// unixAddressPrefix introduces the second of the two listen-address forms: this
// prefix followed by a filesystem path, which is bound as a Unix domain socket.
// Anything not carrying it is read as the first form, a host and a port.
const unixAddressPrefix = "unix:"

// loopbackHostname is the one name a listen address, or the address of an
// authorization server reached in plaintext, may use in place of a loopback
// literal. It is checked by resolving it and requiring every address it resolves
// to be a loopback address, because the name is what an operator types while the
// addresses behind it are what the listener will actually accept on, and what a
// request naming it will actually reach.
//
// No other name is treated this way: a name that happens to resolve to a
// loopback address is a network address here, because reading it otherwise would
// make where a listener may bind, and where a plaintext request may go, depend on
// a resolution that can change under it.
const loopbackHostname = "localhost"

// listenerDrainBudget bounds how long a listener whose run has ended keeps
// answering before it is closed outright.
//
// It is the budget the editor backend already holds for the same moment, and one
// number rather than two because both answer the same question: how long a
// request already accepted may go on being answered after the process has been
// asked to stop. A request still in flight finishes and its answer reaches the
// caller; one that has not finished by then loses its connection, which is what a
// caller of a stopping process should expect.
const listenerDrainBudget = 2 * time.Second

// listenerHeaderBudget bounds how long a peer that has asserted nothing may hold
// a connection while it delivers its request headers.
//
// A connection is accepted before anything about the caller is known, and the
// headers are read before the handler sees the request at all — so every check
// the handler makes, the credential among them, sits behind this read. Without a
// bound, a peer that opens a connection and stops mid-header holds a connection,
// a goroutine and a file descriptor for as long as it likes, having presented
// nothing that could be refused. It is the same question the drain budget above
// answers at the other end of a connection's life, and it is answered the same
// way: with a fixed number rather than a setting, because how long a peer that
// has asserted nothing may hold a connection is not a thing a deployment differs
// about.
//
// Seconds rather than milliseconds because a client on a slow or distant link
// still deserves to finish a header block that a working one sends in a single
// round trip, and single-digit seconds because nothing beyond that is a client
// still making progress. It also bounds the TLS handshake, which net/http
// derives from the smallest of the read and write deadlines set here, and which
// is this same question one layer down.
const listenerHeaderBudget = 5 * time.Second

// listenerIdleBudget bounds how long a connection may sit between requests
// before it is closed.
//
// A caller that will ask again keeps its connection rather than paying for
// another handshake, which is what makes a minute the useful number: long enough
// that an interactive caller's next request reuses the connection it already
// has, short enough that a peer which has stopped asking does not hold one
// indefinitely.
const listenerIdleBudget = time.Minute

// ListenConfig carries what selects and shapes the listener itself, as distinct
// from HTTPConfig, which carries what the handler applies to traffic already
// accepted.
//
// An empty Address is not a value this type describes: the command layer starts
// no listener at all in that case and speaks the protocol over standard input
// and output instead.
type ListenConfig struct {
	// Address is where the listener binds, in one of two forms: "host:port", or
	// "unix:" followed by a filesystem path.
	//
	// With neither TLS file below set, the listener serves plaintext, and a
	// plaintext listener is permitted only on loopback or on a Unix socket — the
	// shape a deployment behind a terminating proxy takes. Any other address is
	// refused before anything binds.
	//
	// Those same two forms are the only ones accepted, certificate or not,
	// where the deployment reads its caller's identity from a proxy in front,
	// and for a different reason: there the credential is a request header,
	// which is trustworthy only where nothing but the proxy can set it.
	Address string

	// CertFile is the path to the PEM certificate chain the listener presents,
	// and KeyFile the path to that certificate's PEM private key. They are set
	// together: with both set the process terminates TLS itself and may serve on
	// any address, with neither set it serves plaintext under the confinement
	// above, and one without the other is refused.
	CertFile string
	KeyFile  string

	// HealthAddress is where liveness and readiness are answered, in the same
	// two forms Address takes. Empty is a deployment that asked for neither
	// check, and no second listener exists for it.
	//
	// It is a second address rather than two paths on the first because the
	// endpoint refuses every request carrying no credential, and a probe that
	// could be answered there would be an unauthenticated reading of the process
	// on whatever network reaches it. The confinement Address carries does not
	// apply here and is not enforced: where this address sits is the operator's
	// decision, and the reference page states what it is for.
	HealthAddress string
}

// ListenAndServe answers the protocol over HTTP until ctx ends.
//
// It is the entry point the `aggrega mcp` command calls when a listen address is
// configured, and the counterpart of Run, which speaks the same protocol over
// standard input and output. Everything a request can influence is settled by
// the handler NewHTTPHandler builds; what this adds is where that handler is
// reachable from, whether the connection carrying a request is encrypted, and
// the end of the listener's life.
//
// The order of the steps below is the contract rather than an arrangement.
// Every refusal a deployment can meet at startup is made before the endpoint is
// built or bound, so a misconfigured deployment fails at startup rather than
// after an address is already accepting: what the listener itself is asked to
// do, then what the deployment declares itself to be, then whether the
// credential it reads may be read at the address it asked for, then where the
// keys it verifies tokens against come from, and then whether there is a
// workspace for it to serve. The handler is built after all of them, because acquiring the
// documentation bundle is the expensive step and there is no reason to pay it
// for a deployment that is about to be refused. Only then is the endpoint bound.
//
// The health address is the one exception, and it is an exception to when
// something binds rather than to the order of the refusals. Where a deployment
// configured one, it opens once the questions about the configuration itself are
// settled and before the workspace question is asked. A supervisor has to be
// able to see the process alive while it is still deciding whether it can serve
// at all — that gap is the whole distinction between liveness and readiness, and
// an address that opened only after the decision could never report the state it
// exists to report. A refusal made after it is open closes it and still returns,
// so a refused launch leaves no socket behind.
func ListenAndServe(ctx context.Context, cfg Config, transport HTTPConfig, listen ListenConfig) error {
	return listenAndServe(ctx, cfg, transport, listen, nil)
}

// listenAndServe is ListenAndServe with one seam in it, which is why the
// exported entry point above is a wrapper rather than the whole of it.
//
// beforeWorkspace, where a caller supplies one, is called immediately before the
// workspace question is asked. Nothing in this binary supplies one: it exists so
// an assertion in this package can hold a launch inside the window readiness
// reports on — the health address answering, the endpoint not yet bound — and
// read what a probe receives on each side of that one step. A real launch passes
// through the window in microseconds, so holding it open is the only way to
// observe the transition at all. It is a seam on the ORDER and not on any
// duration: what it makes observable is which answer a probe gets before a step
// and which it gets after, and nothing measures how long the step takes.
func listenAndServe(ctx context.Context, cfg Config, transport HTTPConfig, listen ListenConfig,
	beforeWorkspace func()) error {
	lifecycle := lifecycleLog{w: listenerLogWriter(cfg.Stderr), root: cfg.WorkspaceRoot}
	lifecycle.note(lifecycleAudit{Event: eventStartup, Address: listen.Address})
	// Wrapped so that every refusal below reports itself without each of them
	// having to remember to: a refusal is reported where it is returned FROM
	// rather than where it is raised, so a refusal added later is reported by
	// having been returned.
	refused := func(err error) error {
		lifecycle.note(lifecycleAudit{
			Event:   eventStartupRefused,
			Address: listen.Address,
			Detail:  err.Error(),
		})
		return err
	}

	target, cert, err := resolveListenTarget(listen)
	if err != nil {
		return refused(err)
	}
	// Resolved here and the results dropped, then resolved again inside the
	// handler below. The refusals have to be made before the handler is built,
	// because building one acquires the documentation bundle; and the handler
	// cannot lean on its caller having made them, because it is reachable
	// without coming through here. One resolver each, called from both points,
	// is what keeps the two from drifting into disagreeing about what a
	// deployment must declare and where its keys come from.
	resource, err := resolveProtectedResource(transport.Authorization)
	if err != nil {
		return refused(err)
	}
	// The one refusal that is this function's alone, because it is the only
	// place both halves of it are known: what a deployment declared about its
	// callers, and where the listener it declared will be reachable from. The
	// handler is never told the second, so it could not make this refusal
	// however carefully it was written.
	if err := refuseTrustedProxyOnAnExposedAddress(target, transport.Authorization, listen.Address); err != nil {
		return refused(err)
	}
	if _, err := resolveAuthenticator(ctx, transport, resource); err != nil {
		return refused(err)
	}

	ready := &readinessState{}
	health, err := startHealthListener(ctx, listen.HealthAddress, ready, cfg.Stderr)
	if err != nil {
		return refused(err)
	}
	// Deferred so that every return below releases the address, whether the
	// launch was refused or the run ended. Placing it here rather than at each
	// return is what makes "a refusal closes it" a property of the function
	// rather than of whoever adds the next refusal.
	defer health.close()

	if beforeWorkspace != nil {
		beforeWorkspace()
	}
	if err := requireServableWorkspace(cfg.WorkspaceRoot); err != nil {
		return refused(err)
	}
	noteAnUnusedSigningKey(lifecycle, cfg.WorkspaceRoot, listen.Address)
	handler, err := NewHTTPHandler(ctx, cfg, transport)
	if err != nil {
		return refused(err)
	}
	l, err := bindHTTPListener(target, cert, handler, cfg.Stderr)
	if err != nil {
		return refused(err)
	}
	ready.markReady()
	// The address that was actually bound rather than the one that was asked
	// for, which is the difference a deployment that asked for port zero needs
	// to read, and the only line here that can report it.
	lifecycle.note(lifecycleAudit{Event: eventListening, Address: l.ln.Addr().String()})

	// The end of the run is reported whichever way it ended, and a serving
	// failure carries its own reason: the ordinary end is the run's context
	// ending, and a listener that stopped for its own reason would otherwise
	// leave a process that exited with nothing on the stream saying why.
	err = l.serve(ctx)
	shutdown := lifecycleAudit{Event: eventShutdown, Address: l.ln.Addr().String()}
	if err != nil {
		shutdown.Detail = err.Error()
	}
	lifecycle.note(shutdown)
	return err
}

// lifecycleLog reports what the serving process did on its own account, on the
// stream every other diagnostic goes to.
//
// It writes where the server does not yet exist, which is why it holds the
// stream rather than a server: every refusal a launch can meet is made before a
// handler is built, and a refusal nobody could report would leave an operator
// with a process that exited and no line saying why. It exists only on this
// path, so a process speaking its own streams writes none of these.
type lifecycleLog struct {
	w    io.Writer
	root string
}

// note writes one lifecycle line, filling in what every one of them carries.
func (l lifecycleLog) note(line lifecycleAudit) {
	line.Time = auditNow()
	line.Transport = transportName(transportHTTP)
	line.WorkspaceRoot = l.root
	writeAuditLine(l.w, line)
}

// unusedSigningKeyDetail is what the line below says, in the operator's own
// words.
//
// It names the SETTING and stops there. The setting is something the operator
// wrote and can go and find; where the file it points at sits is information about
// the machine this process runs on, which belongs on this stream no more than a
// credential does. The sentence is written for a reader supervising a process
// rather than for one composing a call, because the caller side of this is already
// answered where a call selects a key and is refused.
const unusedSigningKeyDetail = "signing.key_path names a key this server holds, and a caller reaching this " +
	"listener cannot spend it: a call answered here signs only with an identity token the call itself " +
	"carries, or is refused. Sign with the held key at a terminal on this machine instead."

// noteAnUnusedSigningKey reports, once as this listener starts, that the workspace
// it serves names a signing key the listener will not use.
//
// It exists because the alternative is a silent change of behaviour. A workspace
// that signs with a held key at a terminal signs with a resolved identity when the
// same workspace is served over a listener, and without this line an operator would
// learn that from a signature they did not expect rather than from the process
// they started. A deployment that names no key is told nothing, because there is
// nothing about it to say.
//
// A workspace it cannot resolve is passed over rather than refused. Whether the
// served directory is furnished yet is a question for the calls answered in it, not
// for whether the address may open — the refusal above has already settled the only
// workspace question that decides that — so a configuration this cannot read costs
// the operator a line rather than the launch.
func noteAnUnusedSigningKey(lifecycle lifecycleLog, root, address string) {
	// The same resolution a call answered over this listener makes: it begins
	// where the process was pointed, and names no root of its own, because a caller
	// reaching a listener cannot name one. So the configuration read here is the
	// configuration those calls will read.
	rw, err := workspaceconfig.Resolve(root, "")
	if err != nil || !namesASigningKey(rw.Config()) {
		return
	}
	lifecycle.note(lifecycleAudit{
		Event:   eventSigningKeyIgnored,
		Address: address,
		Detail:  unusedSigningKeyDetail,
	})
}

// namesASigningKey reports whether cfg names a signing key at any scope a
// publication could resolve one from.
//
// It asks a different question from the per-host resolution the publication layer
// makes, and the difference is that no host is known yet: a listener starts before
// any call names one. Reading the global block and every registry's own is exactly
// the set of hosts for which that per-host resolution could come back with a key,
// because a resolved key is the global one overlaid by that host's — so a
// deployment is told once, at startup, rather than left to discover it when a call
// for the one host that names a key arrives.
func namesASigningKey(cfg workspaceconfig.Config) bool {
	if cfg.Signing.KeyPath != "" {
		return true
	}
	for _, registry := range cfg.Registries {
		if registry.Signing.KeyPath != "" {
			return true
		}
	}
	return false
}

// requireServableWorkspace refuses a listener that has no workspace to serve.
//
// A process reached over its own streams was launched by the client using it,
// from wherever that client happened to be, and answering from the directory it
// was started in is the useful thing to do. A listener is the opposite: it is
// started once by an operator and answers callers who cannot see the machine, so
// the directory it happens to have been launched from is not a workspace anyone
// chose. Serving one anyway would publish whatever that directory contains to
// every caller that reaches the address.
//
// A root supplied at launch settles it outright: the operator named the
// workspace, and whether that directory is furnished yet is a question for the
// calls answered in it rather than for whether the address may open. With no
// root supplied, discovery must find something to anchor on — falling back to
// the working directory is exactly the case this refuses.
func requireServableWorkspace(root string) error {
	if root != "" {
		return nil
	}
	rw, err := workspaceconfig.Resolve("", "")
	if err != nil {
		return fmt.Errorf("resolve the workspace this server would serve: %w", err)
	}
	if rw.Marker() != workspaceconfig.MarkerCWD {
		return nil
	}
	return fmt.Errorf("this server was started in a directory no workspace resolves from, and a server "+
		"reached over HTTP serves one workspace rather than whichever directory it was launched in: name "+
		"the workspace with --workspace-root, or set AGGREGA_WORKSPACE_ROOT, or start the server inside "+
		"the workspace it is to serve (it was started in %s)", rw.Root())
}

// listenTarget is a parsed listen address: what to bind, where, and whether a
// plaintext listener is permitted there.
type listenTarget struct {
	network string
	address string

	// confined reports that a caller reaching this address is already on the
	// machine — a loopback address or a Unix socket — so a listener presenting no
	// certificate of its own exposes nothing to a network peer.
	confined bool
}

// resolveListenTarget settles every refusal this transport can make about its
// own configuration, and reaches nothing outside the configuration and the two
// files it names.
//
// The address is parsed and the TLS material read first, because each is a
// question about one setting. The confinement follows, because it is a question
// about the two of them together: whether a plaintext listener — which is what
// having no certificate means — may bind where the address says.
func resolveListenTarget(listen ListenConfig) (listenTarget, *tls.Certificate, error) {
	target, err := parseListenAddress(listen.Address)
	if err != nil {
		return listenTarget{}, nil, err
	}
	cert, err := loadListenerCertificate(listen)
	if err != nil {
		return listenTarget{}, nil, err
	}
	if cert == nil && !target.confined {
		return listenTarget{}, nil, fmt.Errorf(
			"a listener with no certificate serves plaintext, which is permitted only on loopback or on a "+
				"Unix socket, and %q is neither: set mcp.tls.cert_file and mcp.tls.key_file so this process "+
				"terminates TLS itself, or listen on 127.0.0.1, ::1, localhost, or unix: followed by a path",
			listen.Address)
	}
	return target, cert, nil
}

// httpListener is a bound listener with the server that answers on it. Binding
// and serving are separate so a caller can read the address that was actually
// bound before any traffic reaches it.
type httpListener struct {
	ln   net.Listener
	srv  *http.Server
	cert *tls.Certificate
}

// bindHTTPListener binds the resolved target and builds the server that answers
// on it. Every refusal is already behind it, so the only failure left here is
// the bind itself.
//
// logw is where a connection-level failure is reported; a nil value is the
// process's own error stream, matching every other stream default in this
// package.
func bindHTTPListener(target listenTarget, cert *tls.Certificate,
	handler http.Handler, logw io.Writer) (*httpListener, error) {
	ln, err := net.Listen(target.network, target.address)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", target.address, err)
	}
	srv := &http.Server{
		Handler: handler,
		// net/http reports a connection-level failure on its own logger, which
		// writes to the process's standard error by default. It is pointed at the
		// stream every other diagnostic here goes to instead, carrying the prefix
		// every one of them carries, so one reader of one stream sees all of them.
		ErrorLog:          log.New(listenerLogWriter(logw), "mcp: ", 0),
		ReadHeaderTimeout: listenerHeaderBudget,
		IdleTimeout:       listenerIdleBudget,
		// ReadTimeout and WriteTimeout are left at zero, which means no deadline,
		// and that is the decision rather than the omission: they bound a whole
		// request and a whole response, and both are legitimately long here. A
		// call can drive a build that runs for minutes, and a request body can
		// carry an authored document of any size, so a deadline on either would
		// cut work that is progressing. The two budgets above bound a peer that is
		// not progressing at all, which is the different question. Setting either
		// of these to complete the set would also reach further than it looks:
		// net/http takes the TLS handshake bound from the smallest of the three
		// read and write deadlines, so a value below the header budget shortens
		// the handshake too.
	}
	if cert != nil {
		srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{*cert}, MinVersion: tls.VersionTLS12}
	}
	return &httpListener{ln: ln, srv: srv, cert: cert}, nil
}

// listenerLogWriter settles where a listener's own diagnostics go.
//
// A nil writer is the process's own error stream, matching every other stream
// default in this package. It is a function rather than a check at each site
// because both listeners a run may hold answer the question, and two sites
// deciding it separately would be two defaults that agree until one moves.
func listenerLogWriter(w io.Writer) io.Writer {
	if w == nil {
		return os.Stderr
	}
	return w
}

// serve answers on the bound listener until ctx ends, then drains it within the
// budget and closes it.
//
// Draining stops the listener accepting immediately and lets a request already
// being answered finish, which is what makes the end of a run different from the
// connection simply dropping: a caller that asked for something before the stop
// still gets its answer. A request that has not finished inside the budget loses
// its connection, so the process ends rather than being held open by one call.
// The second stop request is not handled here — the command layer ends the
// process outright on it, which is the shape every long-running command in this
// binary takes.
func (l *httpListener) serve(ctx context.Context) error {
	served := make(chan error, 1)
	go func() {
		if l.cert != nil {
			// The certificate is already loaded and installed on the TLS
			// configuration, so the two file arguments are left empty rather than
			// naming the same files a second time.
			served <- l.srv.ServeTLS(l.ln, "", "")
			return
		}
		served <- l.srv.Serve(l.ln)
	}()

	select {
	case err := <-served:
		return listenerExitError(err)
	case <-ctx.Done():
		// The budget is measured from the stop rather than inherited from the
		// context that just ended, which has no time left to give.
		drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), listenerDrainBudget)
		defer cancel()
		if err := l.srv.Shutdown(drainCtx); err != nil {
			_ = l.srv.Close()
		}
		return listenerExitError(<-served)
	}
}

// listenerExitError reads what serving returned. A server that was shut down or
// closed reports that as an error, and here it is the ordinary end of a run
// rather than a failure, so it is reported as no error at all.
func listenerExitError(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// parseListenAddress reads a listen address into what it takes to bind it.
//
// A value carrying the Unix prefix is a socket path and is taken as written,
// including a relative one: the path is the deployment's own and nothing here
// re-anchors it. Everything else is a host and a port, and a value that is
// neither is reported as neither rather than being bound as a host with no port.
func parseListenAddress(raw string) (listenTarget, error) {
	if raw == "" {
		return listenTarget{}, errors.New("listen address is empty")
	}
	if path, isUnix := strings.CutPrefix(raw, unixAddressPrefix); isUnix {
		if path == "" {
			return listenTarget{}, fmt.Errorf(
				"listen address %q names no socket path after %q", raw, unixAddressPrefix)
		}
		return listenTarget{network: "unix", address: path, confined: true}, nil
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		return listenTarget{}, fmt.Errorf(
			"listen address %q is neither host:port nor a path introduced by %q: %w", raw, unixAddressPrefix, err)
	}
	if port == "" {
		return listenTarget{}, fmt.Errorf("listen address %q names no port", raw)
	}
	return listenTarget{network: "tcp", address: raw, confined: hostIsLoopback(host)}, nil
}

// hostIsLoopback reports whether this host is the machine this process runs on
// and nothing else. It answers that one question for two readers: whether a
// listener bound to the host accepts only from this machine, and whether an
// address naming the host — an authorization server's, say — reaches only this
// machine. Every plaintext exception this server makes rests on it, so there is
// one reading of loopback rather than one per kind of address.
//
// It takes a bare host. A caller holding an address strips the port, and the
// brackets around an IPv6 literal, before asking.
//
// An empty host is not loopback: bound, it is every interface the machine has,
// and in an address it names no machine at all. An address literal is read
// directly. The one name is resolved, and every address behind it must be
// loopback: a name resolving to both a loopback address and a routable one
// would accept from the network on the second while looking confined on the
// first, and an address naming it could reach the network the same way. Any
// other name is not loopback however it resolves, for the reason
// loopbackHostname states.
func hostIsLoopback(host string) bool {
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	if host != loopbackHostname {
		return false
	}
	addrs, err := net.LookupHost(host)
	if err != nil || len(addrs) == 0 {
		return false
	}
	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip == nil || !ip.IsLoopback() {
			return false
		}
	}
	return true
}

// loadListenerCertificate reads the certificate the listener presents, or
// reports that the deployment configured none.
//
// The two paths are one setting in two halves: a certificate with no key cannot
// be presented and a key with no certificate names nothing, so one without the
// other is refused rather than silently falling back to plaintext — which would
// turn a typo in one path into a listener serving unencrypted traffic.
func loadListenerCertificate(listen ListenConfig) (*tls.Certificate, error) {
	switch {
	case listen.CertFile == "" && listen.KeyFile == "":
		return nil, nil
	case listen.CertFile == "":
		return nil, errors.New(
			"mcp.tls.key_file is set and mcp.tls.cert_file is not; a listener that terminates TLS needs both")
	case listen.KeyFile == "":
		return nil, errors.New(
			"mcp.tls.cert_file is set and mcp.tls.key_file is not; a listener that terminates TLS needs both")
	}
	cert, err := tls.LoadX509KeyPair(listen.CertFile, listen.KeyFile)
	if err != nil {
		return nil, fmt.Errorf(
			"read the certificate and key named by mcp.tls.cert_file and mcp.tls.key_file: %w", err)
	}
	return &cert, nil
}
