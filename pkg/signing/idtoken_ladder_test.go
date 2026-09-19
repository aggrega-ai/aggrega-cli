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

// wireCallerSupply stands in for a caller that reaches this ladder over a
// protocol rather than from a terminal: it names a request field, and it reports
// no terminal because it has none. It is what the test uses wherever a
// caller-specific vocabulary is under test.
var wireCallerSupply = IdentityTokenSupply{
	AbsentInput: "no identityToken was sent",
	Remedy:      "send identityToken",
}

// TestResolveIdentityTokenNonInteractiveRefuses proves the unattended entry
// point refuses when nothing is supplied and nothing is ambient: it returns an
// error rather than an empty resolution, and the error names every input that
// would have satisfied it, so the caller learns what to supply instead of only
// that resolution failed.
//
// The vocabulary half is asserted in both directions, because naming an input is
// only useful if it is an input this caller HAS. So the refusal must carry what
// the caller declared, and must not carry the terminal or the command line flag
// belonging to a surface this caller is not.
func TestResolveIdentityTokenNonInteractiveRefuses(t *testing.T) {
	t.Parallel()

	idToken, source, err := ResolveIdentityTokenNonInteractive(
		"",
		lookFrom(map[string]string{}),
		nil,
		nil,
		wireCallerSupply,
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
		wireCallerSupply.AbsentInput,
		wireCallerSupply.Remedy,
		"ambient GitHub Actions OIDC endpoint",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not name %q; got: %s", want, msg)
		}
	}
	for _, unwanted := range []string{"--", "terminal", "stdin"} {
		if strings.Contains(msg, unwanted) {
			t.Errorf("refusal names %q, which belongs to a caller with a command line and a terminal, "+
				"not to this one; got: %s", unwanted, msg)
		}
	}
}

// TestNoIdentityTokenRefusalReadsAsOneSentenceForEveryCaller pins the composition
// itself, across the shapes a caller's vocabulary can take.
//
// The refusal is assembled from a variable number of clauses, so the conjunction
// before the last one moves. That is exactly the kind of seam that produces a
// dangling "and" or a doubled comma for one caller while reading correctly for the
// caller whose text somebody checked — which no behavioural test would catch,
// because the error is still an error.
func TestNoIdentityTokenRefusalReadsAsOneSentenceForEveryCaller(t *testing.T) {
	t.Parallel()

	shared := "no " + EnvIdentityToken + " or " + EnvSigstoreIDToken +
		" is exported, and no ambient GitHub Actions OIDC endpoint is available"

	for _, tc := range []struct {
		name   string
		supply IdentityTokenSupply
		want   string
	}{
		{
			name:   "a caller that declared no vocabulary of its own",
			supply: IdentityTokenSupply{},
			want: "keyless signing needs an identity token: " + shared +
				"; on GitHub Actions the workflow OIDC token is used automatically, otherwise " +
				"export one of those variables (for example a CI workflow OIDC token)",
		},
		{
			name:   "a caller that names an input but has no terminal",
			supply: wireCallerSupply,
			want: "keyless signing needs an identity token: no identityToken was sent, no " +
				EnvIdentityToken + " or " + EnvSigstoreIDToken +
				" is exported, and no ambient GitHub Actions OIDC endpoint is available" +
				"; on GitHub Actions the workflow OIDC token is used automatically, otherwise " +
				"export one of those variables (for example a CI workflow OIDC token) or send identityToken",
		},
		{
			name: "a caller with a terminal, which reports the terminal last",
			supply: IdentityTokenSupply{
				AbsentInput:  "no --identity-token was passed",
				AbsentPrompt: "stdin is not an interactive terminal",
				Remedy:       "pass --identity-token",
			},
			want: "keyless signing needs an identity token: no --identity-token was passed, no " +
				EnvIdentityToken + " or " + EnvSigstoreIDToken +
				" is exported, no ambient GitHub Actions OIDC endpoint is available, and stdin is not " +
				"an interactive terminal; on GitHub Actions the workflow OIDC token is used " +
				"automatically, otherwise export one of those variables (for example a CI workflow " +
				"OIDC token) or pass --identity-token",
		},
		{
			// A caller whose resolution read no environment at all: the shared inputs
			// are named nowhere, neither as what was absent nor as what to do, because
			// nothing consulted them and nothing the caller did to them would help.
			name: "a caller whose resolution reads no environment",
			supply: IdentityTokenSupply{
				AbsentInput:              "no identityToken was sent",
				Remedy:                   "send identityToken",
				SharedInputsNotConsulted: true,
			},
			want: "keyless signing needs an identity token: no identityToken was sent; send identityToken",
		},
		{
			// The degenerate shape, kept because it is the one that could read as a
			// dangling clause: no vocabulary of its own AND nothing shared to name.
			name:   "a caller that reads no environment and declared no vocabulary either",
			supply: IdentityTokenSupply{SharedInputsNotConsulted: true},
			want:   "keyless signing needs an identity token: nothing supplied one",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := noIdentityTokenRefusal(tc.supply).Error(); got != tc.want {
				t.Errorf("refusal text moved:\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

// TestSupplyCannotChangeWhichRungResolves holds the standing contract on the
// supply parameter: it carries the caller's vocabulary into the refusal and is
// read nowhere else, so nothing declared on it moves a resolution.
//
// The clause that says the shared inputs were not consulted is the one that could
// break it, because it reads like an instruction to stop consulting them. It is
// not: what makes those rungs unreachable is the environment reader answering
// nothing, and this asserts the two are genuinely independent — a resolution whose
// reader DOES carry a token still resolves from it whatever the supply says, and a
// resolution whose reader carries none refuses whatever the supply says.
//
// Both directions are asserted because either alone passes on a broken ladder: the
// first row alone passes against a ladder that ignores the clause AND against one
// that never reads the environment, and the second alone passes against a ladder
// that reads nothing at all.
func TestSupplyCannotChangeWhichRungResolves(t *testing.T) {
	t.Parallel()

	notConsulted := wireCallerSupply
	notConsulted.SharedInputsNotConsulted = true

	for _, tc := range []struct {
		// scenario says what the row is about, so a failure reads as a rule.
		scenario  string
		env       map[string]string
		supply    IdentityTokenSupply
		wantToken string
	}{
		{
			scenario:  "the environment carries a token and the supply says the shared inputs are not consulted",
			env:       map[string]string{EnvIdentityToken: "exported-token"},
			supply:    notConsulted,
			wantToken: "exported-token",
		},
		{
			scenario:  "the environment carries a token and the supply says nothing",
			env:       map[string]string{EnvIdentityToken: "exported-token"},
			supply:    wireCallerSupply,
			wantToken: "exported-token",
		},
		{
			scenario: "the environment carries nothing and the supply says the shared inputs are not consulted",
			env:      map[string]string{},
			supply:   notConsulted,
		},
		{
			scenario: "the environment carries nothing and the supply says nothing",
			env:      map[string]string{},
			supply:   wireCallerSupply,
		},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			t.Parallel()
			idToken, source, err := ResolveIdentityTokenNonInteractive(
				"", lookFrom(tc.env), nil, nil, tc.supply)
			if tc.wantToken == "" {
				if err == nil {
					t.Fatalf("want a refusal with nothing to resolve, got idToken=%q", idToken)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
			if source != nil {
				t.Fatal("want a static resolution (nil source), got a live source")
			}
			if idToken != tc.wantToken {
				t.Fatalf("idToken = %q, want %q — the supply moved the resolution", idToken, tc.wantToken)
			}
		})
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
				wireCallerSupply,
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
			if _, _, err := ResolveIdentityToken(tc.flagToken, lookFrom(tc.env), nil, &http.Client{}, acquire, wireCallerSupply); err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
		})
	}
}

// TestResolveIdentityTokenDecliningAcquirerGetsTheSameRefusal proves the
// refusal has one home: an acquirer that declines because there is nobody to
// ask lands on exactly the message the unattended entry point produces. A
// second refusal authored anywhere would diverge from this one.
//
// Both calls pass the same vocabulary, which is what makes the comparison mean
// what it claims: the message varies with the CALLER and must not vary with which
// rung ran out, so holding the caller fixed isolates the property under test.
func TestResolveIdentityTokenDecliningAcquirerGetsTheSameRefusal(t *testing.T) {
	t.Parallel()

	declined := func() (string, bool, error) { return "", false, nil }
	_, _, withAcquirer := ResolveIdentityToken("", lookFrom(map[string]string{}), nil, nil, declined, wireCallerSupply)
	_, _, unattended := ResolveIdentityTokenNonInteractive("", lookFrom(map[string]string{}), nil, nil, wireCallerSupply)

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
	idToken, source, err := ResolveIdentityToken("", lookFrom(map[string]string{}), nil, nil, failing, wireCallerSupply)
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
