// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every proof in this file drives the listener the command layer drives: a real
// address parsed by the production reader, a real bind, and a real client over
// the connection that bind produced. The one thing deliberately left out of the
// bound listeners here is the documentation-bundle acquisition NewHTTPHandler
// performs, because a test of where a listener may bind has nothing to learn
// from a registry round trip — so these bind the handler this package builds for
// its other transport proofs and reach it over the wire.

// shortSocketDir returns a directory for a Unix socket whose path stays well
// inside the length a socket address permits.
//
// It is deliberately not t.TempDir(): that path carries the test's own name, and
// the names in this file are long enough that a socket beneath one would sit
// near a limit whose failure mode is an unrelated-looking bind error.
func shortSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "aggrega-mcp-*")
	if err != nil {
		t.Fatalf("create socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// listenerFixture is one bound listener, serving, with the accept-all
// authenticator installed so what these proofs read is the transport rather than
// the credential check standing in front of it.
type listenerFixture struct {
	l      *httpListener
	served chan error
}

// startListener resolves, binds and serves the given configuration, and returns
// the running listener. A resolution or bind failure fails the test: the proofs
// that assert a refusal call resolveListenTarget directly.
func startListener(t *testing.T, ctx context.Context, listen ListenConfig) *listenerFixture {
	t.Helper()
	target, cert, err := resolveListenTarget(listen)
	if err != nil {
		t.Fatalf("resolve %q: %v", listen.Address, err)
	}
	s := newCommandToolServer(t, nil)
	h, err := newHTTPHandler(context.Background(), s, defaultTransportConfig())
	if err != nil {
		t.Fatalf("newHTTPHandler: %v", err)
	}
	h.auth = acceptAllAuthenticator{}
	return startBoundListener(t, ctx, target, cert, h)
}

// startBoundListener binds and serves an already-built handler, for the proofs
// that supply a handler of their own.
func startBoundListener(t *testing.T, ctx context.Context, target listenTarget,
	cert *tls.Certificate, handler http.Handler) *listenerFixture {
	t.Helper()
	l, err := bindHTTPListener(target, cert, handler, io.Discard)
	if err != nil {
		t.Fatalf("bind %s: %v", target.address, err)
	}
	f := &listenerFixture{l: l, served: make(chan error, 1)}
	go func() { f.served <- l.serve(ctx) }()
	t.Cleanup(func() { _ = l.srv.Close() })
	return f
}

// awaitServeReturn reads what serving returned, within the liveness ceiling. The
// ceiling separates "returned" from "hung forever" and measures nothing else.
func (f *listenerFixture) awaitServeReturn(t *testing.T) error {
	t.Helper()
	select {
	case err := <-f.served:
		return err
	case <-time.After(runReturnCeiling):
		t.Fatalf("the listener never stopped serving within the liveness ceiling")
		return nil
	}
}

// loopbackURL is the address a client reaches this listener on. A listener bound
// to every interface is reached over loopback, which is one of the interfaces it
// bound: the point of that row is where it MAY bind, not where a caller comes
// from.
func loopbackURL(t *testing.T, l *httpListener, scheme string) string {
	t.Helper()
	return fmt.Sprintf("%s://%s%s", scheme, loopbackHostPort(t, l), endpointPath)
}

// loopbackHostPort is the host and port a client dials this listener on.
func loopbackHostPort(t *testing.T, l *httpListener) string {
	t.Helper()
	tcpAddr, ok := l.ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address is %T, not a TCP address", l.ln.Addr())
	}
	return fmt.Sprintf("127.0.0.1:%d", tcpAddr.Port)
}

// awaitNoLongerAccepting waits until a fresh connection to the listener is
// refused, which is what a listener that has closed its accepting socket does.
//
// It is the discriminator the drain proof rests on: it establishes that shutdown
// has already begun while a request is still inside the handler, so what that
// request goes on to receive separates a drain from a close.
func awaitNoLongerAccepting(t *testing.T, hostPort string) {
	t.Helper()
	deadline := time.Now().Add(runReturnCeiling)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("tcp", hostPort)
		if err != nil {
			return
		}
		_ = conn.Close()
	}
	t.Fatalf("%s was still accepting connections after the run ended", hostPort)
}

// unixClient dials one Unix socket whatever host a request names, which is how
// an HTTP client reaches a listener that has no host of its own.
func unixClient(socketPath string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}}
}

// postModernToolsList sends one well-formed request in the per-request revision
// and reports the status and the body. It is the same fixture the other
// transport proofs post, so what varies between the rows below is the listener
// and nothing about the message.
func postModernToolsList(t *testing.T, client *http.Client, url string) (int, string) {
	t.Helper()
	body := modernRequest(t, 1, "tools/list", nil)
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	for name, value := range modernHeaders("tools/list", "") {
		req.Header.Set(name, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("post to %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return resp.StatusCode, string(raw)
}

// wantServed asserts one exchange reached the protocol surface and came back
// with a result rather than a refusal.
func wantServed(t *testing.T, status int, body, what string) {
	t.Helper()
	if status != http.StatusOK {
		t.Fatalf("%s: status = %d, want %d (body %q)", what, status, http.StatusOK, body)
	}
	if !strings.Contains(body, `"result"`) {
		t.Fatalf("%s: answered %q, which carries no result", what, body)
	}
}

// testCertificate writes a self-signed certificate and key for 127.0.0.1 into
// dir and returns their paths with a pool that trusts it.
//
// It is generated rather than committed because a committed certificate expires,
// and an expired fixture fails a test whose subject is not expiry.
func testCertificate(t *testing.T, dir string) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "aggrega-mcp-listener-fixture"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	certFile = filepath.Join(dir, "listener.pem")
	keyFile = filepath.Join(dir, "listener.key")
	writeFixtureFile(t, certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	writeFixtureFile(t, keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))

	pool = x509.NewCertPool()
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	pool.AddCert(parsed)
	return certFile, keyFile, pool
}

func writeFixtureFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// A plaintext address that is neither loopback nor a Unix socket is refused
// before anything binds, and the refusal names the two settings that lift it.
//
// The pair is what makes this a proof rather than an assertion about one string:
// the SAME address binds and serves once a certificate is configured, so the
// refusal below cannot be a listener that failed to bind — nothing bound,
// because resolution refused before a bind was reached. The row that serves on
// that address is TestListenerServesTLSOnANetworkAddress.
func TestListenerRefusesPlaintextOnANetworkAddress(t *testing.T) {
	for _, address := range []string{"0.0.0.0:0", ":0", "192.0.2.1:8080", "[2001:db8::1]:8080"} {
		t.Run(address, func(t *testing.T) {
			_, _, err := resolveListenTarget(ListenConfig{Address: address})
			if err == nil {
				t.Fatalf("resolving %q produced no refusal, so a plaintext listener would bind there", address)
			}
			for _, key := range []string{"mcp.tls.cert_file", "mcp.tls.key_file"} {
				if !strings.Contains(err.Error(), key) {
					t.Errorf("the refusal does not name %s, so a reader is not told what lifts it: %v", key, err)
				}
			}
		})
	}
}

// The refusal reaches the entry point the command layer calls, and reaches it
// before anything is built or bound. ListenAndServe would otherwise block
// serving; that it returns the refusal at all is the observable.
func TestListenAndServeRefusesBeforeItBuildsOrBinds(t *testing.T) {
	err := ListenAndServe(context.Background(), Config{Stderr: io.Discard},
		defaultTransportConfig(),
		ListenConfig{Address: "0.0.0.0:0"})
	if err == nil {
		t.Fatal("ListenAndServe accepted a plaintext network address")
	}
	if !strings.Contains(err.Error(), "mcp.tls.cert_file") {
		t.Fatalf("the refusal is not the confinement's: %v", err)
	}
}

// A plaintext listener on a loopback address serves the protocol, which is the
// confinement's other half: it bounds where plaintext binds, never whether.
func TestListenerServesPlaintextOnLoopback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f := startListener(t, ctx, ListenConfig{Address: "127.0.0.1:0"})
	status, body := postModernToolsList(t, http.DefaultClient, loopbackURL(t, f.l, "http"))
	wantServed(t, status, body, "a plaintext listener on loopback")
}

// A plaintext listener on a Unix socket serves the protocol, and the socket it
// bound is the path that was configured.
func TestListenerServesPlaintextOnAUnixSocket(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	socket := filepath.Join(shortSocketDir(t), "s")
	_ = startListener(t, ctx, ListenConfig{Address: unixAddressPrefix + socket})
	if _, err := os.Stat(socket); err != nil {
		t.Fatalf("nothing is listening at the configured path: %v", err)
	}
	status, body := postModernToolsList(t, unixClient(socket), "http://socket"+endpointPath)
	wantServed(t, status, body, "a plaintext listener on a Unix socket")
}

// With both TLS settings configured the process terminates TLS itself and
// serves on an address the plaintext confinement refuses.
//
// The client trusts only the generated certificate, so a listener that answered
// in plaintext, or with anything else, would fail the handshake rather than be
// read as a success.
func TestListenerServesTLSOnANetworkAddress(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	certFile, keyFile, pool := testCertificate(t, t.TempDir())
	f := startListener(t, ctx, ListenConfig{Address: "0.0.0.0:0", CertFile: certFile, KeyFile: keyFile})

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
	}}
	status, body := postModernToolsList(t, client, loopbackURL(t, f.l, "https"))
	wantServed(t, status, body, "a TLS listener on a network address")
}

// The two TLS settings are one setting in two halves, and half of it is refused
// rather than falling back to a plaintext listener.
func TestListenerRefusesOneTLSPathWithoutTheOther(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, _ := testCertificate(t, dir)

	for _, tc := range []struct {
		name   string
		listen ListenConfig
		names  string
	}{
		{"certificate with no key", ListenConfig{Address: "127.0.0.1:0", CertFile: certFile}, "mcp.tls.key_file"},
		{"key with no certificate", ListenConfig{Address: "127.0.0.1:0", KeyFile: keyFile}, "mcp.tls.cert_file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := resolveListenTarget(tc.listen)
			if err == nil {
				t.Fatal("one half of the TLS setting was accepted, so the listener would have served plaintext")
			}
			if !strings.Contains(err.Error(), tc.names) {
				t.Errorf("the refusal does not name the missing setting %s: %v", tc.names, err)
			}
		})
	}
}

// A certificate path naming nothing readable is refused where it is read, and
// the refusal names both settings so a reader knows which pair produced it.
func TestListenerRefusesAnUnreadableCertificate(t *testing.T) {
	dir := t.TempDir()
	_, _, err := resolveListenTarget(ListenConfig{
		Address:  "127.0.0.1:0",
		CertFile: filepath.Join(dir, "absent.pem"),
		KeyFile:  filepath.Join(dir, "absent.key"),
	})
	if err == nil {
		t.Fatal("a certificate that cannot be read was accepted")
	}
	if !strings.Contains(err.Error(), "mcp.tls.cert_file") {
		t.Fatalf("the refusal does not name the setting that produced it: %v", err)
	}
}

// An address in neither form is refused as neither, rather than being bound as
// something a reader did not write.
func TestListenerRefusesAnAddressInNeitherForm(t *testing.T) {
	for _, address := range []string{"", "127.0.0.1", "unix:", "http://127.0.0.1:8080"} {
		t.Run(fmt.Sprintf("%q", address), func(t *testing.T) {
			if _, err := parseListenAddress(address); err == nil {
				t.Fatalf("%q was read as a listen address", address)
			}
		})
	}
}

// The loopback reading is what the confinement rests on, so both directions are
// pinned: what counts as loopback, and what does not.
func TestLoopbackReadingOfAListenHost(t *testing.T) {
	for _, tc := range []struct {
		host string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.0.0.53", true},
		{"::1", true},
		{loopbackHostname, true},
		{"", false},
		{"0.0.0.0", false},
		{"192.0.2.1", false},
		{"::", false},
		{"example.test", false},
	} {
		t.Run(fmt.Sprintf("%q", tc.host), func(t *testing.T) {
			if got := hostIsLoopback(tc.host); got != tc.want {
				t.Errorf("hostIsLoopback(%q) = %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}

// Ending the run drains the listener: a request already being answered finishes
// and its answer reaches the caller, and only then does serving return.
//
// The discriminator is the ORDER, not a duration. The request is held inside the
// handler until after the run has ended, so a listener that closed rather than
// drained would break that connection and the client would read an error instead
// of the answer it is asserted to read.
func TestEndingTheRunDrainsTheListenerBeforeClosingIt(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("drained"))
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	target, cert, err := resolveListenTarget(ListenConfig{Address: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	f := startBoundListener(t, ctx, target, cert, handler)
	url := loopbackURL(t, f.l, "http")
	hostPort := loopbackHostPort(t, f.l)

	answered := make(chan string, 1)
	failed := make(chan error, 1)
	go func() {
		resp, reqErr := http.Post(url, contentTypeJSON, strings.NewReader("{}"))
		if reqErr != nil {
			failed <- reqErr
			return
		}
		defer func() { _ = resp.Body.Close() }()
		raw, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			failed <- readErr
			return
		}
		answered <- string(raw)
	}()

	select {
	case <-entered:
	case err := <-failed:
		t.Fatalf("the request failed before it reached the handler: %v", err)
	case <-time.After(runReturnCeiling):
		t.Fatal("the request never reached the handler within the liveness ceiling")
	}

	cancel()
	// The request is still inside the handler at this point, and it stays there
	// until the listener has stopped accepting. So what it receives below is the
	// answer of a listener that was already shutting down — which is the one
	// thing a listener that closed rather than drained could not produce.
	awaitNoLongerAccepting(t, hostPort)
	close(release)

	select {
	case got := <-answered:
		if got != "drained" {
			t.Errorf("the drained request was answered %q, want %q", got, "drained")
		}
	case err := <-failed:
		t.Fatalf("the in-flight request lost its connection instead of being drained: %v", err)
	case <-time.After(runReturnCeiling):
		t.Fatal("the in-flight request was never answered within the liveness ceiling")
	}

	if err := f.awaitServeReturn(t); err != nil {
		t.Errorf("serving reported %v; the end of a run is not a failure", err)
	}
}

// The drain is BOUNDED, so a call that never finishes does not hold the process
// open. Nothing here measures how long the budget is: the assertion is that
// serving returns at all while a request is still parked inside the handler.
func TestTheDrainIsBoundedSoAParkedCallCannotHoldTheProcessOpen(t *testing.T) {
	parked := make(chan struct{})
	entered := make(chan struct{})
	t.Cleanup(func() { close(parked) })
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(entered)
		<-parked
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	target, cert, err := resolveListenTarget(ListenConfig{Address: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	f := startBoundListener(t, ctx, target, cert, handler)
	url := loopbackURL(t, f.l, "http")

	go func() {
		resp, reqErr := http.Post(url, contentTypeJSON, strings.NewReader("{}"))
		if reqErr == nil {
			_ = resp.Body.Close()
		}
	}()

	select {
	case <-entered:
	case <-time.After(runReturnCeiling):
		t.Fatal("the request never reached the handler within the liveness ceiling")
	}

	cancel()
	if err := f.awaitServeReturn(t); err != nil {
		t.Errorf("serving reported %v; a drain that ran out of budget is not a failure", err)
	}
}

// A peer that never finishes its request headers loses its connection, so
// asserting nothing is not a way to hold one.
//
// The headers are read before the handler is called at all, so neither the
// authenticator nor the concurrency slot stands in front of this: both are
// claimed inside ServeHTTP, and a peer that stops mid-header never gets that
// far. It reaches no gate because it presents nothing any gate could refuse.
//
// Nothing here measures how long the budget is. The assertion is that the
// connection ends at all, which holds for any finite bound and fails for the
// zero value that means no deadline; the ceiling separates "ends" from "never"
// and is not a performance budget.
//
// The control is the well-formed request underneath, posted to the SAME listener
// while the half-open connection is still held. Without it this would pass
// against a listener that had stopped serving everyone, which is the opposite of
// what is being claimed.
func TestAPeerThatNeverFinishesItsHeadersLosesItsConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f := startListener(t, ctx, ListenConfig{Address: "127.0.0.1:0"})
	hostPort := loopbackHostPort(t, f.l)

	conn, err := net.Dial("tcp", hostPort)
	if err != nil {
		t.Fatalf("dial %s: %v", hostPort, err)
	}
	defer func() { _ = conn.Close() }()
	// A request line and one header, and then nothing: no terminating blank line,
	// so the header block a server waits for never arrives.
	if _, err := io.WriteString(conn, "POST "+endpointPath+" HTTP/1.1\r\nHost: "+hostPort+"\r\n"); err != nil {
		t.Fatalf("write a partial request: %v", err)
	}

	status, body := postModernToolsList(t, http.DefaultClient, loopbackURL(t, f.l, "http"))
	wantServed(t, status, body, "a well-formed request while a peer holds a half-open connection")

	// Reading to the end of the connection is how its end is observed: whatever
	// the server does with it, closing it ends this read. The deadline here is
	// the client's own, so a listener that never closed shows up as this read
	// timing out rather than as a test that hangs.
	if err := conn.SetReadDeadline(time.Now().Add(runReturnCeiling)); err != nil {
		t.Fatalf("set the liveness ceiling: %v", err)
	}
	if _, err := io.Copy(io.Discard, conn); errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatal("the half-open connection was still held at the liveness ceiling, " +
			"so a peer that has asserted nothing can hold a connection indefinitely")
	}
}

// A Unix socket listener removes the socket it created when it closes, so a
// later run of the same deployment binds the same path rather than meeting a
// file left behind by the last one.
func TestAUnixSocketIsRemovedWhenTheListenerCloses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	socket := filepath.Join(shortSocketDir(t), "s")
	f := startListener(t, ctx, ListenConfig{Address: unixAddressPrefix + socket})
	if _, err := os.Stat(socket); err != nil {
		t.Fatalf("the socket was not created: %v", err)
	}

	cancel()
	if err := f.awaitServeReturn(t); err != nil {
		t.Fatalf("serving reported %v", err)
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Errorf("the socket is still at %s after the listener closed (stat error %v)", socket, err)
	}
}
