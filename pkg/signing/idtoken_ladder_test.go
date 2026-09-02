// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing

import (
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestResolveIdentityTokenNonInteractiveRefuses proves the unattended entry
// point refuses when nothing is supplied and nothing is ambient: it returns an
// error rather than an empty resolution, and the error names every input that
// would have satisfied it, so the caller learns what to supply instead of only
// that resolution failed.
func TestResolveIdentityTokenNonInteractiveRefuses(t *testing.T) {
	t.Parallel()

	idToken, source, err := ResolveIdentityTokenNonInteractive(
		"",
		lookFrom(map[string]string{}),
		nil,
		nil,
	)
	if err == nil {
		t.Fatal("want a refusal with nothing supplied and nothing ambient, got nil error")
	}
	if idToken != "" || source != nil {
		t.Fatalf("want an empty resolution alongside the refusal, got idToken=%q source!=nil=%v", idToken, source != nil)
	}

	// Each input the caller could supply must be named, because the refusal is
	// the only thing an unattended caller receives.
	msg := err.Error()
	for _, want := range []string{
		EnvIdentityToken,
		EnvSigstoreIDToken,
		"--identity-token",
		"ambient GitHub Actions OIDC endpoint",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not name %q; got: %s", want, msg)
		}
	}
}

// TestResolveIdentityTokenNonInteractiveAcceptsSuppliedAndAmbient covers every
// rung the unattended entry point does reach. Refusing is only correct if the
// inputs that should satisfy it still do, so each rung is exercised through the
// same entry point an unattended caller uses.
func TestResolveIdentityTokenNonInteractiveAcceptsSuppliedAndAmbient(t *testing.T) {
	t.Parallel()

	const ambientURL = "https://actions.example/oidc"
	ambient := map[string]string{
		envActionsIDTokenRequestURL:   ambientURL,
		envActionsIDTokenRequestToken: "req-token",
	}
	withAmbient := func(extra map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range ambient {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	tests := []struct {
		name       string
		flagToken  string
		env        map[string]string
		wantToken  string
		wantSource bool
	}{
		{
			name:      "supplied token beats every other rung",
			flagToken: "flag-token",
			env:       withAmbient(map[string]string{EnvIdentityToken: "aggrega-token", EnvSigstoreIDToken: "sigstore-token"}),
			wantToken: "flag-token",
		},
		{
			name:      "AGGREGA_IDENTITY_TOKEN beats ambient and the interop alias",
			env:       withAmbient(map[string]string{EnvIdentityToken: "aggrega-token", EnvSigstoreIDToken: "sigstore-token"}),
			wantToken: "aggrega-token",
		},
		{
			name:       "ambient endpoint beats the frozen interop alias",
			env:        withAmbient(map[string]string{EnvSigstoreIDToken: "sigstore-token"}),
			wantSource: true,
		},
		{
			name:      "interop alias resolves when it is the only input",
			env:       map[string]string{EnvSigstoreIDToken: "sigstore-token"},
			wantToken: "sigstore-token",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			idToken, source, err := ResolveIdentityTokenNonInteractive(
				tc.flagToken,
				lookFrom(tc.env),
				nil,
				&http.Client{},
			)
			if err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
			if tc.wantSource {
				if source == nil {
					t.Fatal("want the ambient live source, got nil")
				}
				if idToken != "" {
					t.Fatalf("idToken = %q, want empty because the ambient rung resolves to a source", idToken)
				}
				return
			}
			if source != nil {
				t.Fatal("want a static resolution (nil source), got a live source")
			}
			if idToken != tc.wantToken {
				t.Fatalf("idToken = %q, want %q", idToken, tc.wantToken)
			}
		})
	}
}

// TestResolveIdentityTokenAcquirerRungIsBelowEverySuppliedInput proves an
// acquirer is consulted only after every supplied and ambient rung has
// declined. An acquirer that may block on a human must never be reached while a
// token is already available.
func TestResolveIdentityTokenAcquirerRungIsBelowEverySuppliedInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		flagToken string
		env       map[string]string
	}{
		{name: "supplied token wins", flagToken: "flag-token"},
		{name: "AGGREGA_IDENTITY_TOKEN wins", env: map[string]string{EnvIdentityToken: "aggrega-token"}},
		{name: "interop alias wins", env: map[string]string{EnvSigstoreIDToken: "sigstore-token"}},
		{
			name: "ambient endpoint wins",
			env: map[string]string{
				envActionsIDTokenRequestURL:   "https://actions.example/oidc",
				envActionsIDTokenRequestToken: "req-token",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			acquire := func() (string, bool, error) {
				t.Error("the acquirer was reached while a token was already available")
				return "acquired", true, nil
			}
			if _, _, err := ResolveIdentityToken(tc.flagToken, lookFrom(tc.env), nil, &http.Client{}, acquire); err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
		})
	}
}

// TestResolveIdentityTokenDecliningAcquirerGetsTheSameRefusal proves the
// refusal has one home: an acquirer that declines because there is nobody to
// ask lands on exactly the message the unattended entry point produces. A
// second refusal authored anywhere would diverge from this one.
func TestResolveIdentityTokenDecliningAcquirerGetsTheSameRefusal(t *testing.T) {
	t.Parallel()

	declined := func() (string, bool, error) { return "", false, nil }
	_, _, withAcquirer := ResolveIdentityToken("", lookFrom(map[string]string{}), nil, nil, declined)
	_, _, unattended := ResolveIdentityTokenNonInteractive("", lookFrom(map[string]string{}), nil, nil)

	if withAcquirer == nil || unattended == nil {
		t.Fatalf("want both paths to refuse, got withAcquirer=%v unattended=%v", withAcquirer, unattended)
	}
	if withAcquirer.Error() != unattended.Error() {
		t.Fatalf("the two refusals diverge, so the message has more than one home:\n declining acquirer: %s\n unattended:         %s", withAcquirer, unattended)
	}
}

// TestResolveIdentityTokenAcquirerFailureIsNotMasked proves a genuine
// acquisition failure reaches the caller unchanged rather than being replaced by
// the refusal. The two outcomes call for different responses — retry the
// acquisition, or supply a token — so collapsing them would misdirect the
// caller.
func TestResolveIdentityTokenAcquirerFailureIsNotMasked(t *testing.T) {
	t.Parallel()

	sentinel := &acquisitionFailure{}
	failing := func() (string, bool, error) { return "", false, sentinel }
	idToken, source, err := ResolveIdentityToken("", lookFrom(map[string]string{}), nil, nil, failing)
	if err != sentinel {
		t.Fatalf("err = %v, want the acquisition failure returned unchanged", err)
	}
	if idToken != "" || source != nil {
		t.Fatalf("want an empty resolution alongside the failure, got idToken=%q source!=nil=%v", idToken, source != nil)
	}
}

// acquisitionFailure stands in for a failure inside an interactive acquisition
// flow. It is compared by identity, so the test can tell "the acquirer failed"
// apart from "the ladder refused".
type acquisitionFailure struct{}

func (*acquisitionFailure) Error() string { return "acquisition flow failed" }

// TestIdentityTokenLadderCannotPromptOrPersist is a structural guard over the
// file that holds the identity-token ladder. Two properties are asserted by
// import: the ladder cannot start a flow that blocks on a human, and it cannot
// touch the filesystem or the process environment — so no caller of it, however
// long-lived, can be made to hold a token at rest through this path. The
// environment reaches the ladder only through the injected lookup, and any
// interactive flow only through an acquirer the caller supplies.
//
// Both properties are invisible to a behavioural test: code that is present but
// unreached asserts nothing. This guard fails the moment such an import
// appears, which is before it can be reached.
func TestIdentityTokenLadderCannotPromptOrPersist(t *testing.T) {
	t.Parallel()

	const ladderFile = "idtoken.go"

	// A pass would be vacuous if the ladder had moved to another file, so
	// confirm this file still declares the entry point under guard.
	src, err := os.ReadFile(ladderFile)
	if err != nil {
		t.Fatalf("read %s: %v", ladderFile, err)
	}
	for _, decl := range []string{
		"func ResolveIdentityToken(",
		"func ResolveIdentityTokenNonInteractive(",
	} {
		if !strings.Contains(string(src), decl) {
			t.Fatalf("%s no longer declares %q, so this guard would pass without inspecting the ladder; point it at the file that does", ladderFile, decl)
		}
	}

	denied := map[string]string{
		"github.com/sigstore/sigstore/pkg/oauthflow": "a federated-login flow that blocks on a human",
		"os":        "the process environment and the filesystem",
		"os/exec":   "launching an external program",
		"io/ioutil": "the filesystem",
	}

	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, ladderFile, src, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", ladderFile, err)
	}
	for _, imp := range parsed.Imports {
		path, uerr := strconv.Unquote(imp.Path.Value)
		if uerr != nil {
			t.Fatalf("unquote import %s in %s: %v", imp.Path.Value, ladderFile, uerr)
		}
		if why, bad := denied[path]; bad {
			t.Errorf("%s imports %q, which reaches %s; the identity-token ladder must reach neither", ladderFile, path, why)
		}
	}
}
