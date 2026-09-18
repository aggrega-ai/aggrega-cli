// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every proof in this file drives a real listener over a real connection. The
// rows about what a launch does with a health address drive the launch entry
// point itself, because what is under test is when an address opens and when it
// closes relative to the rest of the startup — which only the real entry point
// can show. The rows about what the two checks answer drive the construction
// point that entry point reaches, so a request travels over a bound socket to
// the handler a deployment gets rather than to one assembled here.

// healthSocketAddress returns a health listen address and the filesystem path
// behind it.
//
// A socket is the address throughout, for the reason the startup rows use one:
// the socket is a file, so its presence and its absence are direct readings of
// whether anything is listening rather than inferences from a returned error.
// Its own directory, distinct from the endpoint's, so two addresses in one
// launch cannot collide.
func healthSocketAddress(t *testing.T) (address, path string) {
	t.Helper()
	path = filepath.Join(shortSocketDir(t), "health.sock")
	return unixAddressPrefix + path, path
}

// probeAnswer is what one request to a health address produced.
type probeAnswer struct {
	status int
	body   string
	header http.Header
}

// probe sends one request and reads the whole answer, the body included, so an
// assertion that a check answers with nothing in its body reads the bytes
// rather than trusting the status beside them.
func probe(t *testing.T, client *http.Client, method, url string, headers map[string]string) probeAnswer {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, url, err)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read the answer to %s %s: %v", method, url, err)
	}
	return probeAnswer{status: resp.StatusCode, body: string(raw), header: resp.Header}
}

// wantProbeAnswer asserts one probe was answered with a status and with nothing
// else at all.
func wantProbeAnswer(t *testing.T, got probeAnswer, want int, what string) {
	t.Helper()
	if got.status != want {
		t.Errorf("%s: status = %d, want %d", what, got.status, want)
	}
	if got.body != "" {
		t.Errorf("%s: answered with the body %q, and a health answer is a status code and nothing else",
			what, got.body)
	}
}

// awaitReadyAnswer polls readiness until it is answered ready.
//
// The ceiling separates "flips" from "never flips" and measures nothing else:
// the discriminating half of the transition is the not-ready reading taken
// before the launch was released, which is deterministic because readiness is
// marked strictly after the step this test holds the launch at.
func awaitReadyAnswer(t *testing.T, client *http.Client, url string) probeAnswer {
	t.Helper()
	deadline := time.Now().Add(runReturnCeiling)
	for {
		got := probe(t, client, http.MethodGet, url, nil)
		if got.status == http.StatusOK {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("readiness was still answered %d at the liveness ceiling, so the endpoint never "+
				"reported itself ready", got.status)
		}
		time.Sleep(time.Millisecond)
	}
}

// A process that is running is not yet a process that can answer a call, and the
// two checks are what tell those apart.
//
// The launch is held at the workspace question, which is the one step the health
// address is required to be open across: everything before it is a question
// about the configuration alone, and everything after it leads to the endpoint
// binding. Held there, liveness is answered and readiness is not — and readiness
// could not have been read as not-ready by accident, because it is marked
// strictly after the step the launch is parked at.
func TestHealthAnswersLivenessBeforeTheEndpointBindsAndReadinessOnlyAfter(t *testing.T) {
	ctx, endRun := context.WithCancel(context.Background())
	t.Cleanup(endRun)

	root := rootedWorkspace(t)
	healthAddress, healthSocket := healthSocketAddress(t)
	endpointAddress, endpointSocket := socketAddress(t)

	atTheWorkspaceQuestion := make(chan struct{})
	releaseTheLaunch := make(chan struct{})
	launched := make(chan error, 1)
	go func() {
		launched <- listenAndServe(ctx,
			Config{WorkspaceRoot: root, Stderr: io.Discard},
			defaultTransportConfig(),
			ListenConfig{Address: endpointAddress, HealthAddress: healthAddress},
			func() {
				close(atTheWorkspaceQuestion)
				<-releaseTheLaunch
			})
	}()

	select {
	case <-atTheWorkspaceQuestion:
	case err := <-launched:
		t.Fatalf("the launch returned before it reached the workspace question: %v", err)
	case <-time.After(runReturnCeiling):
		t.Fatal("the launch never reached the workspace question within the liveness ceiling")
	}

	if _, err := os.Stat(endpointSocket); err == nil {
		t.Fatalf("precondition: %s is already bound at the workspace question, so a not-ready reading "+
			"below would prove nothing about the order", endpointSocket)
	}
	client := unixClient(healthSocket)
	wantProbeAnswer(t, probe(t, client, http.MethodGet, "http://health"+livenessPath, nil),
		http.StatusOK, "liveness while the launch is still deciding whether it can serve")
	wantProbeAnswer(t, probe(t, client, http.MethodGet, "http://health"+readinessPath, nil),
		http.StatusServiceUnavailable, "readiness before the workspace has resolved")

	close(releaseTheLaunch)
	awaitSocket(t, endpointSocket, launched)
	wantProbeAnswer(t, awaitReadyAnswer(t, client, "http://health"+readinessPath),
		http.StatusOK, "readiness once the endpoint has bound")
	wantProbeAnswer(t, probe(t, client, http.MethodGet, "http://health"+livenessPath, nil),
		http.StatusOK, "liveness once the endpoint has bound")

	endRun()
	select {
	case err := <-launched:
		if err != nil {
			t.Errorf("the launch returned %v, want a clean end", err)
		}
	case <-time.After(runReturnCeiling):
		t.Fatal("the launch never returned within the liveness ceiling")
	}
}

// The second listener exists exactly where an operator configured one, and
// nowhere else.
//
// The two rows share every input but the health address, which is what makes
// the absent row a proof rather than an observation: without the configured row
// beside it, "nothing is listening there" would pass just as well against a
// build that never opens a health address at all.
func TestTheHealthAddressIsListeningExactlyWhereItIsConfigured(t *testing.T) {
	for _, tc := range []struct {
		name string
		// configured reports whether the launch is given the health address the
		// row computed. Both rows compute one; only this decides whether the
		// launch is told about it.
		configured bool
	}{
		{name: "the health address is configured", configured: true},
		{name: "no health address is configured", configured: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, endRun := context.WithCancel(context.Background())
			t.Cleanup(endRun)

			root := rootedWorkspace(t)
			healthAddress, healthSocket := healthSocketAddress(t)
			endpointAddress, endpointSocket := socketAddress(t)

			listen := ListenConfig{Address: endpointAddress}
			if tc.configured {
				listen.HealthAddress = healthAddress
			}
			served := make(chan error, 1)
			go func() {
				served <- ListenAndServe(ctx,
					Config{WorkspaceRoot: root, Stderr: io.Discard},
					defaultTransportConfig(), listen)
			}()
			awaitSocket(t, endpointSocket, served)

			if tc.configured {
				if _, err := os.Stat(healthSocket); err != nil {
					t.Fatalf("nothing is listening at the configured health address: %v", err)
				}
				wantProbeAnswer(t, probe(t, unixClient(healthSocket), http.MethodGet,
					"http://health"+livenessPath, nil), http.StatusOK,
					"liveness on a health address configured as a Unix socket")
			} else {
				if _, err := os.Stat(healthSocket); !os.IsNotExist(err) {
					t.Errorf("%s exists, so a health listener was started for a deployment that "+
						"configured none (stat error %v)", healthSocket, err)
				}
				if conn, err := net.Dial("unix", healthSocket); err == nil {
					_ = conn.Close()
					t.Errorf("a connection to %s was accepted, so something is answering at an address "+
						"no deployment configured", healthSocket)
				}
			}

			endRun()
			select {
			case err := <-served:
				if err != nil {
					t.Errorf("serving returned %v, want a clean end", err)
				}
			case <-time.After(runReturnCeiling):
				t.Fatal("the listener never stopped serving within the liveness ceiling")
			}
		})
	}
}

// A launch refused after the health address is open closes it and still returns
// the refusal, so nothing is left behind for the next launch to trip over.
//
// The probe taken at the workspace question is this row's own control: it
// establishes that the address really was open at the moment the refusal fired,
// which is the only state in which the closing is worth anything. Without it the
// absence asserted afterwards would be satisfied by a build that never opened
// the address at all.
func TestARefusedLaunchClosesAHealthAddressItHadAlreadyOpened(t *testing.T) {
	t.Chdir(aDirectoryNoWorkspaceResolvesFrom(t))

	healthAddress, healthSocket := healthSocketAddress(t)
	endpointAddress, endpointSocket := socketAddress(t)

	ctx, endRun := context.WithCancel(context.Background())
	t.Cleanup(endRun)

	var live probeAnswer
	err := listenAndServe(ctx,
		Config{Stderr: io.Discard},
		defaultTransportConfig(),
		ListenConfig{Address: endpointAddress, HealthAddress: healthAddress},
		// The seam runs on this test's own goroutine, because the launch is
		// called in line: it is about to refuse, so nothing here can hang.
		func() {
			live = probe(t, unixClient(healthSocket), http.MethodGet, "http://health"+livenessPath, nil)
		})

	wantProbeAnswer(t, live, http.StatusOK, "liveness while the launch was still deciding")
	if err == nil {
		t.Fatal("the launch served a directory holding no workspace")
	}
	if !strings.Contains(err.Error(), "--workspace-root") {
		t.Fatalf("the refusal is not the workspace one: %v", err)
	}
	if _, statErr := os.Stat(endpointSocket); statErr == nil {
		t.Errorf("%s exists, so the endpoint bound before the refusal", endpointSocket)
	}
	if _, statErr := os.Stat(healthSocket); !os.IsNotExist(statErr) {
		t.Errorf("%s is still there after the refusal, so a refused launch leaves its health socket "+
			"behind (stat error %v)", healthSocket, statErr)
	}
	if conn, dialErr := net.Dial("unix", healthSocket); dialErr == nil {
		_ = conn.Close()
		t.Errorf("%s still accepts connections after the refusal", healthSocket)
	}
}

// startReadyHealthListener binds a health listener on a loopback address, ready,
// and returns the base address a client reaches it on.
//
// The address form here is the other one a deployment may write — a host and a
// port rather than a socket path — so the two forms are both driven, and the
// bound port is read back from the listener rather than guessed.
func startReadyHealthListener(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	ready := &readinessState{}
	ready.markReady()
	h, err := startHealthListener(ctx, "127.0.0.1:0", ready, io.Discard)
	if err != nil {
		t.Fatalf("start a health listener on loopback: %v", err)
	}
	t.Cleanup(h.close)
	return "http://" + h.l.ln.Addr().String()
}

// Where the health address sits is the operator's decision, and an address that
// is neither loopback nor a socket opens like any other.
//
// This asserts an ABSENCE of enforcement, deliberately, and that is why it
// exists. The endpoint refuses a plaintext address that is not confined; these
// two checks carry no body, ask for nothing and disclose only whether the
// process is up, so the same confinement is not applied to them and the
// reference page says what the address is for instead. The asymmetry between the
// two listeners reads like an oversight, so a reader may reach to "fix" it — and
// without this row the whole package still passes when they do, leaving nothing
// between a decision and its silent reversal. A comment cannot fail; this can.
//
// The address binds every interface, which is what makes it not loopback and so
// the only shape that exercises the absence. The port is chosen by the kernel
// and the listener is closed when the test ends.
func TestAHealthAddressThatIsNotConfinedIsAcceptedAnyway(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	ready := &readinessState{}
	ready.markReady()
	h, err := startHealthListener(ctx, "0.0.0.0:0", ready, io.Discard)
	if err != nil {
		t.Fatalf("a health address that is not confined was refused, which reverses a decision "+
			"the reference page states and this row exists to hold: %v", err)
	}
	if h == nil {
		t.Fatal("no health listener was started for an address a deployment configured")
	}
	t.Cleanup(h.close)

	// The bound address names every interface, which is not a host a client can
	// dial, so the probe goes to loopback on the port the kernel chose — one of
	// the interfaces this listener is answering on.
	_, port, err := net.SplitHostPort(h.l.ln.Addr().String())
	if err != nil {
		t.Fatalf("read the port the health listener bound: %v", err)
	}
	wantProbeAnswer(t, probe(t, http.DefaultClient, http.MethodGet,
		"http://127.0.0.1:"+port+livenessPath, nil), http.StatusOK,
		"liveness on a health address that is not confined")
}

// The health address serves those two paths and that one method, and answers
// everything else without serving anything.
//
// The two serving rows are the table's control: they share the listener with
// every refusing row, so a refusal here is the path or the method being refused
// rather than the listener being unreachable.
func TestTheHealthAddressServesOnlyTheTwoChecksAndOnlyGET(t *testing.T) {
	base := startReadyHealthListener(t)

	for _, tc := range []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{"liveness", http.MethodGet, livenessPath, http.StatusOK},
		{"readiness", http.MethodGet, readinessPath, http.StatusOK},
		{"the root path", http.MethodGet, "/", http.StatusNotFound},
		{"the protocol endpoint", http.MethodGet, endpointPath, http.StatusNotFound},
		{"the metadata path", http.MethodGet, wellKnownProtectedResource, http.StatusNotFound},
		{"a path below a served one", http.MethodGet, livenessPath + "/deeper", http.StatusNotFound},
		{"posting to liveness", http.MethodPost, livenessPath, http.StatusMethodNotAllowed},
		{"posting to readiness", http.MethodPost, readinessPath, http.StatusMethodNotAllowed},
		{"heading liveness", http.MethodHead, livenessPath, http.StatusMethodNotAllowed},
		{"deleting readiness", http.MethodDelete, readinessPath, http.StatusMethodNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := probe(t, http.DefaultClient, tc.method, base+tc.path, nil)
			wantProbeAnswer(t, got, tc.want, tc.name)
			if got.status == http.StatusMethodNotAllowed && got.header.Get(headerAllow) != http.MethodGet {
				t.Errorf("%s: %s = %q, want %q so a caller is told what this path does serve",
					tc.name, headerAllow, got.header.Get(headerAllow), http.MethodGet)
			}
			if challenge := got.header.Get(headerAuthenticate); challenge != "" {
				t.Errorf("%s: answered with the challenge %q, so this address asks for a credential",
					tc.name, challenge)
			}
		})
	}
}

// Nothing a caller puts in a header changes what a check answers, which is the
// readable form of the address reading no header at all.
//
// A token is among the rows deliberately: the address asks for no credential, so
// a caller that sends one must not be answered any differently from one that
// sends nothing — neither better nor worse.
func TestAHealthAnswerDoesNotDependOnAnyHeaderTheCallerSends(t *testing.T) {
	base := startReadyHealthListener(t)

	for _, tc := range []struct {
		name    string
		headers map[string]string
	}{
		{name: "no headers at all"},
		{name: "a bearer token", headers: map[string]string{headerAuthorization: "Bearer not-read-here"}},
		{name: "an unlisted origin", headers: map[string]string{headerOrigin: "https://unlisted.example.test"}},
		{name: "a content type", headers: map[string]string{headerContentType: "text/plain"}},
		{name: "a protocol revision", headers: map[string]string{headerProtocolVersion: "1999-01-01"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, path := range []string{livenessPath, readinessPath} {
				wantProbeAnswer(t, probe(t, http.DefaultClient, http.MethodGet, base+path, tc.headers),
					http.StatusOK, tc.name+" on "+path)
			}
		})
	}
}

// The endpoint answers the two health paths with the not-found it gives every
// path it does not serve, and answers it before it looks at a credential.
//
// The authenticator the handler ships with is the one installed here, so the
// refusal a caller with no token meets on the endpoint itself is live — and that
// refusal is the control: without it, a not-found on these two paths would read
// the same whether the credential check ran first or was not there at all.
func TestTheEndpointAnswersTheHealthPathsNotFoundBeforeReadingACredential(t *testing.T) {
	f := newUnauthenticatedHTTPFixture(t, context.Background(), defaultTransportConfig(), nil)

	refused := f.post(t, modernHeaders("tools/list", ""), modernRequest(t, 1, "tools/list", nil))
	if refused.status != http.StatusUnauthorized {
		t.Fatalf("precondition: the endpoint answered a request carrying no token %d, want %d, so this "+
			"fixture cannot show that a path is settled ahead of the credential",
			refused.status, http.StatusUnauthorized)
	}
	if refused.header.Get(headerAuthenticate) == "" {
		t.Fatal("precondition: the endpoint's refusal carries no challenge, so the credential check " +
			"standing in front of it is not the shipped one")
	}

	for _, path := range []string{livenessPath, readinessPath} {
		for _, tc := range []struct {
			name    string
			headers map[string]string
		}{
			{name: "carrying no token"},
			{name: "carrying a token", headers: map[string]string{headerAuthorization: "Bearer not-read-here"}},
		} {
			t.Run(path+" "+tc.name, func(t *testing.T) {
				got := f.send(t, http.MethodGet, path, tc.headers, "")
				if got.status != http.StatusNotFound {
					t.Errorf("the endpoint answered %s %d, want %d; a health path is not served here",
						path, got.status, http.StatusNotFound)
				}
				if challenge := got.header.Get(headerAuthenticate); challenge != "" {
					t.Errorf("the endpoint answered %s with the challenge %q, so the path was settled "+
						"after the credential was read rather than before it", path, challenge)
				}
				if got.raw != "" {
					t.Errorf("the endpoint answered %s with the body %q, and a path it does not serve "+
						"describes nothing", path, got.raw)
				}
			})
		}
	}
}
