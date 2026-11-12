// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// What a credential's scopes admit, and what the server lists whatever they
// admit, over a real listener, with the tokens minted by an authorization server
// running in this process.
//
// The grid is driven rather than reasoned about: every cell issues a real
// request and reads a real answer. Its margins are the five grants an
// authorization server can issue for this deployment crossed with one tool per
// reach class a scope admits; its interior is the build, which declares no reach
// and widens to the workspace when the composition that decides a dependency
// fill says it fills. A grid specified by its margins alone would discharge every
// clause it was written for and leave that interior untested, which is the one
// cell the whole escalation exists for. The class no scope admits is measured
// with a planted tool beside the edit tool's proofs, over the same grants.
//
// Every cell is read twice, because the two answers are to different
// questions: whether the tool is LISTED, which is what the server carries and
// never what the credential holds, and what a CALL to it is answered with,
// which is where the credential's scopes bound it.

// scopeGridFixture is one listener over a workspace whose fill setting the case
// chose, with a real authorization server behind it.
//
// The authenticator is the one the handler ships with rather than a stub: what
// is under test is which tools a token's own scopes reach, so a stub that
// asserted the scopes would be asserting the answer.
type scopeGridFixture struct {
	*httpFixture
	issuer *authorizationServerDouble
	root   string

	// srv is the session the listener serves, kept so a case may install a seam
	// the production composition is handed from the command layer. The handler
	// holds this same pointer and reads such a field per request, so installing
	// one after construction is what a session with that seam wired looks like.
	srv *server
}

// newScopeGridFixture starts one. fills writes the workspace's own auto_pull
// setting, which is one of the two inputs the build's fill composition reads.
//
// The workspace has to resolve for the composition to be read at all: the
// widening reads the same two inputs the build reads, and a workspace that does
// not resolve is one the build stops at before filling, so it reaches only what
// it declares. A fixture whose root did not resolve would therefore report the
// interior cell as admitted while measuring nothing, which is why the setting is
// read back below rather than assumed from the bytes written.
func newScopeGridFixture(t *testing.T, fills bool) *scopeGridFixture {
	t.Helper()
	return newScopeGridFixtureDenying(t, fills, nil)
}

// newScopeGridFixtureDenying starts one whose deployment has withdrawn the named
// kinds of reach, which is the bound that sits above what a credential admits.
func newScopeGridFixtureDenying(t *testing.T, fills bool, deny []string) *scopeGridFixture {
	t.Helper()
	return newScopeGridFixtureWith(t, fills, deny, nil)
}

// newScopeGridFixtureWith is the one construction body the entry points above
// share, carrying both bounds that sit above a credential: the kinds of reach
// withdrawn, and the tools announced.
//
// The announcement is installed on the server before the handler is built,
// which is where the real composition installs it — it is a property of the
// server on either transport rather than of the listener, so a fixture that set
// it beside the listener settings would be exercising a wiring the product does
// not have.
func newScopeGridFixtureWith(t *testing.T, fills bool, deny []string, announce map[string]bool) *scopeGridFixture {
	t.Helper()

	root := t.TempDir()
	cfg := "workspace: {}\noffline: true\n"
	if fills {
		cfg += "auto_pull: true\n"
	}
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write the workspace config: %v", err)
	}
	rw, err := workspaceconfig.Resolve(root, root, func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolve the fixture workspace: %v", err)
	}
	if got := rw.Config().AutoPull; got != fills {
		t.Fatalf("precondition: the fixture's resolved auto_pull is %v, want %v; the setting reached a key "+
			"the loader does not read, so no cell below would measure what it claims", got, fills)
	}
	return scopeGridListenerOver(t, root, deny, announce)
}

// scopeGridListenerOver starts one real listener over the workspace at root, with
// a real authorization server behind it and the two bounds that sit above a
// credential: the kinds of reach withdrawn, and the tools announced. It is the one
// construction every listener fixture in this package composes, so a grant minted
// against one is a grant minted against any other.
func scopeGridListenerOver(t *testing.T, root string, deny []string, announce map[string]bool) *scopeGridFixture {
	t.Helper()
	issuer := newAuthorizationServerDouble(t)
	s := newCommandToolServer(t, nil)
	s.launchRoot = root
	s.announcedTools = announce
	transport := transportFor(issuer.authorizationConfig(), issuer.egress())
	transport.DenyReach = deny
	h, err := newHTTPHandler(context.Background(), s, transport)
	if err != nil {
		t.Fatalf("newHTTPHandler: %v", err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return &scopeGridFixture{httpFixture: &httpFixture{ts: ts, h: h}, issuer: issuer, root: root, srv: s}
}

// grantOf mints a token carrying exactly the named scopes. Naming none mints a
// token whose authorization server granted nothing this server publishes, which
// this server still accepts as a credential.
func (f *scopeGridFixture) grantOf(t *testing.T, scopes ...string) string {
	t.Helper()
	return bearer(f.issuer.mint(t, tokenClaimSet{scopeList: scopes}))
}

// listed is the tool names tools/list answers this credential with.
func (f *scopeGridFixture) listed(t *testing.T, credential string) []string {
	t.Helper()
	headers := modernHeaders("tools/list", "")
	headers[headerAuthorization] = credential
	got := f.send(t, http.MethodPost, endpointPath, headers, modernRequest(t, 1, "tools/list", nil))
	return wantReachedTheProtocolSurface(t, got)
}

// surfaced is the tool names the capability tool answers this credential with,
// unscoped, which is the tail behind the set a caller meets first.
func (f *scopeGridFixture) surfaced(t *testing.T, credential string) []string {
	t.Helper()
	return f.surfacedWith(t, credential, map[string]any{})
}

// surfacedWith is the tool names the capability tool answers this credential
// with for the given arguments: the index for none or for a kind, and the
// definitions for named tools.
func (f *scopeGridFixture) surfacedWith(t *testing.T, credential string, args map[string]any) []string {
	t.Helper()
	got := f.callTool(t, credential, describeCapabilitiesName, args)
	if got.status != http.StatusOK {
		t.Fatalf("the capability tool was answered %d (body %q)", got.status, got.raw)
	}
	if got.frame.Error != nil {
		t.Fatalf("the capability tool was answered with an error envelope: %+v", got.frame.Error)
	}
	var result struct {
		IsError           bool                       `json:"isError"`
		Content           []toolTextContent          `json:"content"`
		StructuredContent describeCapabilitiesResult `json:"structuredContent"`
	}
	if err := json.Unmarshal(got.frame.Result, &result); err != nil {
		t.Fatalf("the capability tool's result does not carry a capability listing: %v (body %q)", err, got.raw)
	}
	if result.IsError {
		t.Fatalf("the capability tool reported a failure for %v, so no listing came back: %q", args, got.raw)
	}
	return capabilityNames(result.StructuredContent)
}

// indexedFor is what the capability tool lists this credential for the given
// arguments, and whether it answered at all.
//
// The capability tool is a tool like any other, and it reaches nothing. So a
// credential admitting no read is refused the call itself — which is an answer
// about a call rather than about a listing — and what this asserts for it is
// that refusal, naming the scope that would admit the call, rather than an empty
// index a caller could mistake for a server carrying nothing.
func (f *scopeGridFixture) indexedFor(t *testing.T, credential string, grant []string,
	args map[string]any) ([]string, bool) {
	t.Helper()
	class := declaredReachOfTool(t, describeCapabilitiesName)
	if !admittedByGrant(grant, class) {
		wantRefusedForWantOfScope(t, describeCapabilitiesName,
			f.callTool(t, credential, describeCapabilitiesName, args), scopeAdmittingInThisTest(class))
		return nil, false
	}
	return f.surfacedWith(t, credential, args), true
}

// callTool posts one tools/call carrying this credential.
func (f *scopeGridFixture) callTool(t *testing.T, credential, tool string, args map[string]any) answer {
	t.Helper()
	headers := modernHeaders("tools/call", tool)
	headers[headerAuthorization] = credential
	return f.send(t, http.MethodPost, endpointPath, headers,
		modernRequest(t, 1, "tools/call", map[string]any{"name": tool, "arguments": args}))
}

// admittedByGrant is the mapping every assertion in this file is written
// against, spelled out here rather than read from the package.
//
// Asking the code which classes a grant admits would produce a comparison that
// agreed with the code whatever the code said, which is the one thing a grid
// like this must not do. So the hierarchy is restated: each write scope admits
// its own class and implies the read scope's, and nothing admits the machine.
func admittedByGrant(grant []string, class opsurface.Reach) bool {
	for _, scope := range grant {
		switch scope {
		case scopeRead:
			if class == opsurface.ReachNone {
				return true
			}
		case scopeWorkspaceWrite:
			if class == opsurface.ReachNone || class == opsurface.ReachWorkspace {
				return true
			}
		case scopeRegistryWrite:
			if class == opsurface.ReachNone || class == opsurface.ReachRegistry {
				return true
			}
		}
	}
	return false
}

// everyPublishedScope is the grant carrying every scope this server publishes,
// restated here for the reason the mapping above is.
//
// It is what separates a listing from a call in this file: a tool is listed
// where this grant would admit its class — some credential could run it — and
// called where the credential actually presented admits it.
var everyPublishedScope = []string{scopeRead, scopeWorkspaceWrite, scopeRegistryWrite}

// gridColumn is one call the grid drives, with the class it reaches and the one
// scope that admits that class.
type gridColumn struct {
	// scenario says what the column is, so a failure reads as a sentence.
	scenario string
	tool     string
	args     map[string]any
	// class is what this call reaches, which for one column is not what its
	// operation declares.
	class opsurface.Reach
}

// gridColumns is one tool per reach class a scope admits, and the build asking
// for nothing, asking to fill, and asking to write.
//
// The arguments name subjects the fixture workspace does not hold, deliberately:
// what each cell separates is a call that was REFUSED from one that RAN, and a
// call that ran and then reported that it could not find its subject is on the
// admitted side of that line. Reaching for a real subject would put a compiler
// run inside every admitted cell and measure the compiler rather than the bound.
func gridColumns() []gridColumn {
	return []gridColumn{
		{
			scenario: "a read, which reaches nothing",
			tool:     "describe",
			args:     map[string]any{"name": "core/compute/nothing-here"},
			class:    opsurface.ReachNone,
		},
		{
			scenario: "a dependency resolution, which writes inside the workspace",
			tool:     "pull",
			args:     map[string]any{"release": "release.yaml"},
			class:    opsurface.ReachWorkspace,
		},
		{
			scenario: "a publication, which changes what a registry holds",
			tool:     "publish-entity",
			args:     map[string]any{"entity": "component.yaml", "dry_run": true},
			class:    opsurface.ReachRegistry,
		},
		{
			scenario: "a build that does not ask to fill, which reaches nothing",
			tool:     "build",
			args:     map[string]any{"release": "release.yaml"},
			class:    opsurface.ReachNone,
		},
		{
			// The interior of the grid. The tool is the same one the row above
			// calls and its operation declares the same class; what differs is
			// one argument, and the whole of the escalation is that the argument
			// moves the class.
			scenario: "a build that asks to fill, which widens to the workspace",
			tool:     "build",
			args:     map[string]any{"release": "release.yaml", "auto_pull": true},
			class:    opsurface.ReachWorkspace,
		},
		{
			// The interior again, through the other argument that moves the
			// class: a build asking for its artifacts to be written beneath the
			// workspace root.
			scenario: "a build that asks to write, which widens to the workspace",
			tool:     "build",
			args:     map[string]any{"release": "release.yaml", "write": true},
			class:    opsurface.ReachWorkspace,
		},
	}
}

// gridGrants is the five credentials an authorization server can issue here:
// one carrying none of the scopes this server publishes, each of them alone, and
// all three.
func gridGrants() []struct {
	name  string
	grant []string
} {
	return []struct {
		name  string
		grant []string
	}{
		{"a credential carrying none of the three", nil},
		{"a credential carrying the read scope alone", []string{scopeRead}},
		{"a credential carrying the workspace-write scope alone", []string{scopeWorkspaceWrite}},
		{"a credential carrying the registry-write scope alone", []string{scopeRegistryWrite}},
		{"a credential carrying all three", everyPublishedScope},
	}
}

// aKindServedBy is an entity kind the named tool serves, for the kind-scoped
// listing cell: the first it declares, or any kind at all where it declares
// none, because a tool declaring no kind serves every one.
func aKindServedBy(t *testing.T, name string) string {
	t.Helper()
	spec, found := lookupTool(name)
	if !found {
		t.Fatalf("no tool is registered as %q, so the kind-scoped cell naming it measures nothing", name)
	}
	if len(spec.kinds) == 0 {
		return "release"
	}
	return spec.kinds[0]
}

// scopeARefusalNames is the scope a refused call's challenge names: the one
// admitting the class the tool declares where the credential does not admit even
// that, and otherwise the one admitting the class the call reaches.
//
// That order is the refusal's documented one — a call refused for its declared
// class is told the scope that admits the tool at all, rather than the scope
// that admits a widening it also happened to ask for — and it is restated here
// for the reason the mapping above is.
func scopeARefusalNames(grant []string, declared, reached opsurface.Reach) string {
	if !admittedByGrant(grant, declared) {
		return scopeAdmittingInThisTest(declared)
	}
	return scopeAdmittingInThisTest(reached)
}

// TestWhatEachCredentialIsOfferedAndMayCall is the grid, every cell asserted.
//
// Every cell is asserted including the ones that pass. A grid that checked only
// its refusals would report a deployment that refused everything as correct, and
// a grid that checked only what was offered would miss a tool withheld from the
// list and still callable by name — which is the shape a caller that read about
// a tool elsewhere actually produces.
//
// The listing half is asserted on each surface on its own — tools/list, the
// capability tool's unscoped index, and its index narrowed to a kind the tool
// serves — never on their union. A union would pass a surface that dropped a
// tool the other one carried, which is exactly the disagreement the two are
// composed on one predicate to rule out.
//
// Planted defects, demonstrated: drop ReachNone from scopeImplications's
// workspace-write entry and the describe cell under that credential reddens;
// make buildReach return ReachWorkspace unconditionally, or delete its entry from
// the spec, and the interaction cell reddens. Delete buildReach's read of
// writesArtifacts and the write column reddens under every credential that admits
// a read and not the workspace — the read scope alone and the registry-write
// scope alone — because the call runs instead of being refused.
//
// Planted defect, routing the listings back through the call check: in
// reachAdmission.listed, replace the condition `a.lists(specs[i])` with
// `a.announces(specs[i].name) && a.admits(declaredReach(specs[i]))`. Every listing
// cell of a tool whose declared class the credential does not admit reddens —
// under the read-scoped credential, pull's and publish-entity's on all three
// surfaces — and every call cell stays green, because the call check reads the
// same admission it read before and nothing about it moved.
func TestWhatEachCredentialIsOfferedAndMayCall(t *testing.T) {
	f := newScopeGridFixture(t, false)

	for _, row := range gridGrants() {
		t.Run(row.name, func(t *testing.T) {
			credential := f.grantOf(t, row.grant...)
			listedHere := f.listed(t, credential)
			index, indexAnswered := f.indexedFor(t, credential, row.grant, map[string]any{})

			for _, column := range gridColumns() {
				t.Run(column.scenario, func(t *testing.T) {
					declared := declaredReachOfTool(t, column.tool)

					// Listed is per tool and per server rather than per cell or per
					// credential: every tool of a class some credential could run
					// is listed to every credential this server accepts, and the
					// two build columns are one tool listed under the class its
					// operation declares.
					listedFor := admittedByGrant(everyPublishedScope, declared)
					if got := slices.Contains(listedHere, column.tool); got != listedFor {
						t.Errorf("tools/list names %s = %v, want %v: the list is what this server carries, "+
							"whatever this credential holds. It named %v", column.tool, got, listedFor,
							sortedNames(listedHere))
					}
					if indexAnswered {
						if got := slices.Contains(index, column.tool); got != listedFor {
							t.Errorf("the capability index names %s = %v, want %v, the same answer tools/list "+
								"owes. It named %v", column.tool, got, listedFor, sortedNames(index))
						}
					}
					kind := aKindServedBy(t, column.tool)
					if scoped, answered := f.indexedFor(t, credential, row.grant,
						map[string]any{"kind": kind}); answered {
						if got := slices.Contains(scoped, column.tool); got != listedFor {
							t.Errorf("the capability index narrowed to %s names %s = %v, want %v. It named %v",
								kind, column.tool, got, listedFor, sortedNames(scoped))
						}
					}

					got := f.callTool(t, credential, column.tool, column.args)
					// No column names a Release the served workspace holds, so no
					// cell renders anything and none may leave an output directory —
					// a refused write least of all.
					if _, err := os.Lstat(filepath.Join(f.root, "dist")); err == nil {
						t.Errorf("%s left a dist directory in the served workspace", column.scenario)
					}
					if admittedByGrant(row.grant, declared) && admittedByGrant(row.grant, column.class) {
						wantTheCallRan(t, column.tool, got)
						return
					}
					wantRefusedForWantOfScope(t, column.tool, got,
						scopeARefusalNames(row.grant, declared, column.class))
				})
			}
		})
	}
}

// scopeAdmittingInThisTest names the scope that admits a class, restated here
// for the same reason the hierarchy above is: an assertion that read the name
// from the package would agree with it whatever it said.
func scopeAdmittingInThisTest(class opsurface.Reach) string {
	switch class {
	case opsurface.ReachNone:
		return "aggrega:read"
	case opsurface.ReachWorkspace:
		return "aggrega:workspace-write"
	case opsurface.ReachRegistry:
		return "aggrega:registry-write"
	default:
		return ""
	}
}

// declaredReachOfTool is the class the named tool's operation declares, read
// from the operation set rather than off the spec.
//
// A spec records the class when it enters a registry, and this deliberately does
// not read that field: reading it back would compare the server's answer against
// the same value the server used, which compares nothing. Going to the set for it
// is the second route, so a spec recording a class the set does not declare for
// its operation fails here rather than propagating quietly into every listing.
//
// What this file asserts independently is the MAPPING from a grant to the classes
// it admits. Which class each operation carries is one declaration with one home,
// and a test restating it would be a second copy of the operation table.
func declaredReachOfTool(t *testing.T, name string) opsurface.Reach {
	t.Helper()
	spec, found := lookupTool(name)
	if !found {
		t.Fatalf("no tool is registered as %q, so the row naming it measures nothing", name)
	}
	op, known := opsurface.Lookup(spec.operation)
	if !known {
		t.Fatalf("%s records the operation %q, which the operation set does not declare", name, spec.operation)
	}
	if spec.reach != op.Reach {
		t.Errorf("%s recorded the class %q while the operation set declares %q for %q, so every listing "+
			"narrows on a class the set did not give", name, spec.reach, op.Reach, spec.operation)
	}
	return op.Reach
}

// wantTheCallRan asserts a cell on the admitted side: the request was answered
// by the tool rather than refused before it.
//
// The result is read back rather than the status alone. A refusal composed
// elsewhere would carry its own status, and a 200 carrying an error envelope
// would satisfy a status check while nothing had run — so what is asserted is
// that a tool result came back, which only the tool can produce.
func wantTheCallRan(t *testing.T, tool string, got answer) {
	t.Helper()
	if got.status != http.StatusOK {
		t.Fatalf("%s: status = %d, want 200 — this credential admits what the call reaches (body %q)",
			tool, got.status, got.raw)
	}
	if challenge := got.header.Get(headerAuthenticate); challenge != "" {
		t.Errorf("%s: the answer carries the challenge %q, so a call this credential admits was "+
			"nonetheless invited to obtain another credential", tool, challenge)
	}
	if got.frame.Error != nil {
		t.Fatalf("%s: the answer carries an error envelope rather than a tool result: %+v",
			tool, got.frame.Error)
	}
	var result struct {
		Content []toolTextContent `json:"content"`
	}
	if err := json.Unmarshal(got.frame.Result, &result); err != nil {
		t.Fatalf("%s: the result is not a tool result: %v (body %q)", tool, err, got.raw)
	}
	if len(result.Content) == 0 {
		t.Errorf("%s: the tool result carries no content, so nothing shows the tool was reached: %q",
			tool, got.raw)
	}
}

// wantRefusedForWantOfScope asserts a cell on the refused side: the status the
// protocol's authorization design names for it, and a challenge that says which
// scope would have admitted the call.
//
// The challenge is parsed into its parameters rather than matched as a substring,
// so a challenge carrying the right characters in the wrong place — a scope name
// inside the address, a parameter spelled as part of another — fails here instead
// of passing a contains check.
func wantRefusedForWantOfScope(t *testing.T, tool string, got answer, wantScope string) {
	t.Helper()
	if got.status != http.StatusForbidden {
		t.Fatalf("%s: status = %d, want 403 — this credential does not admit what the call reaches "+
			"(body %q)", tool, got.status, got.raw)
	}
	if got.frame.Error == nil {
		t.Fatalf("%s: a refused call was answered with a result rather than an error envelope: %q",
			tool, got.raw)
	}
	scheme, params := parseChallenge(t, got.header.Get(headerAuthenticate))
	if scheme != bearerScheme {
		t.Errorf("%s: the challenge scheme is %q, want %q", tool, scheme, bearerScheme)
	}
	if params["error"] != "insufficient_scope" {
		t.Errorf("%s: the challenge states error=%q, want %q — a caller told its credential was invalid "+
			"would obtain the same one again", tool, params["error"], "insufficient_scope")
	}
	if params["scope"] != wantScope {
		t.Errorf("%s: the challenge names scope=%q, want %q — the one scope that would admit this call",
			tool, params["scope"], wantScope)
	}
	if params["resource_metadata"] != testMetadataAddress {
		t.Errorf("%s: the challenge points at %q, want %q — the document this deployment publishes",
			tool, params["resource_metadata"], testMetadataAddress)
	}
}

// TestABuildWidensOnTheCompositionAndNotOnItsArgument is the half of the
// interaction the grid above cannot reach, and it is what makes the single-source
// claim falsifiable.
//
// The grid drives the argument. A widening that read the argument alone would
// pass every cell of it — and would then let a caller admitted to read alone
// drive a fill in every workspace configured to fill, which is exactly the case
// the class exists to bound. So this pair drives the OTHER input: one workspace
// configured to fill and one not, the same call in both, carrying no argument at
// all.
//
// The configured-off half is the control. Without it the configured-on half would
// pass against a widening that refused every build, which would be a different
// defect reading as this one being held.
func TestABuildWidensOnTheCompositionAndNotOnItsArgument(t *testing.T) {
	call := map[string]any{"release": "release.yaml"}

	t.Run("a workspace configured to fill widens a build that asked for nothing", func(t *testing.T) {
		f := newScopeGridFixture(t, true)
		got := f.callTool(t, f.grantOf(t, scopeRead), "build", call)
		wantRefusedForWantOfScope(t, "build", got, scopeAdmittingInThisTest(opsurface.ReachWorkspace))
	})

	t.Run("a workspace configured not to fill leaves the same build where it was", func(t *testing.T) {
		f := newScopeGridFixture(t, false)
		got := f.callTool(t, f.grantOf(t, scopeRead), "build", call)
		wantTheCallRan(t, "build", got)
	})
}

// carriedOverAListener is every statically declared tool a listener lists: the
// ones whose operation declares a class some credential could be admitted to.
//
// It is derived from the registries rather than written out, so a tool added
// tomorrow is covered without an edit here; what is NOT derived is the mapping
// from a grant to the classes it admits, which is restated above for the reason
// stated there.
func carriedOverAListener(t *testing.T) []string {
	t.Helper()
	var want []string
	for _, registry := range [][]toolSpec{toolRegistry, deferredToolRegistry} {
		for i := range registry {
			op, known := opsurface.Lookup(registry[i].operation)
			if !known {
				t.Fatalf("%s records the operation %q, which the operation set does not declare",
					registry[i].name, registry[i].operation)
			}
			if admittedByGrant(everyPublishedScope, op.Reach) {
				want = append(want, registry[i].name)
			}
		}
	}
	return want
}

// TestTheTwoListingSurfacesAgreeForOneCredential is the agreement, asserted by
// set difference in both directions on each surface separately.
//
// A count would prove nothing: two sets both short by the same tool agree on
// length and disagree about everything that matters. So each direction names what
// it found — a tool this server carries that the surface did not list, and a
// tool the surface listed that this server does not carry — and each surface is
// held to the expected set on its own, which is what catches a filter applied to
// one surface and not the other.
//
// The expected set is the same for every credential, because the list is what
// the server carries: a credential carrying none of the three is listed the
// same tools as one carrying all of them, and is refused the capability tool's
// own call rather than handed a shorter index.
func TestTheTwoListingSurfacesAgreeForOneCredential(t *testing.T) {
	f := newScopeGridFixture(t, false)
	want := carriedOverAListener(t)

	for _, row := range gridGrants() {
		t.Run(row.name, func(t *testing.T) {
			credential := f.grantOf(t, row.grant...)
			surfaces := []struct {
				surface string
				names   []string
			}{{"tools/list", f.listed(t, credential)}}
			if index, answered := f.indexedFor(t, credential, row.grant, map[string]any{}); answered {
				surfaces = append(surfaces, struct {
					surface string
					names   []string
				}{describeCapabilitiesName, index})
			}

			for _, s := range surfaces {
				if missing := namesMissingFrom(want, s.names); len(missing) > 0 {
					t.Errorf("this server carries %v, and %s did not list them to this credential; it "+
						"listed %v", missing, s.surface, sortedNames(s.names))
				}
				if extra := namesMissingFrom(s.names, want); len(extra) > 0 {
					t.Errorf("%s listed %v to this credential, and this server carries none of them over "+
						"a listener; it carries %v", s.surface, extra, sortedNames(want))
				}
			}
		})
	}
}

// TestAListingReachedWithoutACredentialListsNothing is the row beneath the
// grid's credential carrying none of the three: a request that presented no
// credential at all.
//
// No request reaches that row over a real listener, because the authenticator
// answers a request carrying no credential before any listing is composed. So it
// is driven here, in process, on the very session the listener above serves. What
// it pins is that the admission every listing reads holds the same line on its
// own: a listing is answered only to a caller who presented a credential, so a
// request carrying no principal is listed nothing and admitted no class, while a
// principal carrying no scope is listed what this server carries. The second
// cell is the control: without it the first would pass against an admission that
// listed nothing to anyone.
//
// Planted defect: in classAdmission, drop the guard — attach reachable to the
// admission whether or not principalFrom returned a principal — and the first
// cell reddens, listing every carried tool to a request that presented nothing,
// while the second stays green.
func TestAListingReachedWithoutACredentialListsNothing(t *testing.T) {
	s := newScopeGridFixture(t, false).srv

	anonymous := context.Background()
	if listed := toolSpecNames(s.servedTools(anonymous)); len(listed) != 0 {
		t.Errorf("a request over a listener carrying no principal was listed %v; a listing is answered only to "+
			"a caller who presented a credential", sortedNames(listed))
	}
	admission := s.admittedReach(anonymous)
	for _, class := range opsurface.Classes() {
		if admission.admits(class) {
			t.Errorf("a request over a listener carrying no principal is admitted %q", string(class))
		}
	}

	scopeless := context.WithValue(context.Background(), principalKey{},
		&principal{Subject: "a-caller-granted-no-scope"})
	if listed := s.servedTools(scopeless); len(listed) == 0 {
		t.Error("a request over a listener whose principal carries no scope was listed nothing; the list is " +
			"what this server carries, whatever the credential holds")
	}
}

// TestAToolTheServerDoesNotCarryIsListedToNoCredential is the grid's two
// remaining rows: a kind withdrawn under mcp.deny_reach, and a tool this server
// does not announce. Each is absent from both listings under every credential,
// because no credential changes either bound, and a call naming it is refused
// with no challenge and the setting named, because no scope would admit it.
//
// A tool each bound leaves alone is asserted alongside under every credential,
// and it is not decoration: a listing that dropped everything would satisfy the
// absence while serving nobody, and only naming what is still listed separates a
// bound from an outage.
func TestAToolTheServerDoesNotCarryIsListedToNoCredential(t *testing.T) {
	for _, bound := range []struct {
		scenario string
		deny     []string
		announce map[string]bool
		// absent is the tool the bound takes out of the list, called with args.
		absent string
		args   map[string]any
		// kept is a tool the bound leaves in the list.
		kept string
		// setting is what the refusal of a call to absent names.
		setting string
	}{
		{
			scenario: "a kind withdrawn under mcp.deny_reach",
			deny:     []string{string(opsurface.ReachRegistry)},
			absent:   "publish-entity",
			args:     map[string]any{"entity": "component.yaml", "dry_run": true},
			kept:     "describe",
			setting:  "mcp.deny_reach",
		},
		{
			// The capability tool is announced beside the two under test, because
			// the announcement narrows exactly what it names and a set leaving it
			// out would refuse the index call the listing half makes.
			scenario: "a tool this server does not announce",
			announce: announcing("describe", describeCapabilitiesName, "build"),
			absent:   "pull",
			args:     map[string]any{"release": "release.yaml"},
			kept:     "describe",
			setting:  "mcp.announce_tools",
		},
	} {
		t.Run(bound.scenario, func(t *testing.T) {
			f := newScopeGridFixtureWith(t, false, bound.deny, bound.announce)
			for _, row := range gridGrants() {
				t.Run(row.name, func(t *testing.T) {
					credential := f.grantOf(t, row.grant...)

					surfaces := []struct {
						surface string
						names   []string
					}{{"tools/list", f.listed(t, credential)}}
					if index, answered := f.indexedFor(t, credential, row.grant, map[string]any{}); answered {
						surfaces = append(surfaces, struct {
							surface string
							names   []string
						}{describeCapabilitiesName, index})
					}
					for _, s := range surfaces {
						if slices.Contains(s.names, bound.absent) {
							t.Errorf("%s listed %s to %s, which %s takes out of every listing; it listed %v",
								s.surface, bound.absent, row.name, bound.setting, sortedNames(s.names))
						}
						if !slices.Contains(s.names, bound.kept) {
							t.Errorf("%s did not list %s to %s, which %s leaves alone, so the absence above "+
								"measures an outage rather than the bound; it listed %v",
								s.surface, bound.kept, row.name, bound.setting, sortedNames(s.names))
						}
					}

					got := f.callTool(t, credential, bound.absent, bound.args)
					if got.frame.Error == nil {
						t.Fatalf("a call naming %s was answered with a result rather than refused: %q",
							bound.absent, got.raw)
					}
					if challenge := got.header.Get(headerAuthenticate); challenge != "" {
						t.Errorf("the refusal carries the challenge %q, inviting a caller to obtain a credential "+
							"that cannot lift a bound the server's own configuration set", challenge)
					}
					if !strings.Contains(got.frame.Error.Message, bound.setting) {
						t.Errorf("the refusal does not name %s, so nobody reading it learns what to change: %q",
							bound.setting, got.frame.Error.Message)
					}
				})
			}
		})
	}
}

// TestTheCapabilityToolDefinesAWriteToolForAReadScopedCaller is the definitions
// half of the listing rule: a caller holding only the read scope that names a
// write tool it was listed receives that tool's full definition, and it is the
// definition tools/list gave the same caller.
//
// The comparison with tools/list is what makes this more than a presence check.
// A definition composed differently for a caller its credential does not admit
// — a schema emptied, a description cut short — would pass a check that only
// found the name, and would hand that caller a call it cannot compose.
func TestTheCapabilityToolDefinesAWriteToolForAReadScopedCaller(t *testing.T) {
	f := newScopeGridFixture(t, false)
	credential := f.grantOf(t, scopeRead)
	named := []string{"pull", "publish-entity"}
	for _, name := range named {
		if admittedByGrant([]string{scopeRead}, declaredReachOfTool(t, name)) {
			t.Fatalf("%s is admitted by the read scope, so this case would not measure a tool the credential "+
				"does not admit", name)
		}
	}

	headers := modernHeaders("tools/list", "")
	headers[headerAuthorization] = credential
	listedAnswer := f.send(t, http.MethodPost, endpointPath, headers, modernRequest(t, 1, "tools/list", nil))
	if listedAnswer.frame.Error != nil {
		t.Fatalf("tools/list was answered with an error envelope: %+v", listedAnswer.frame.Error)
	}
	var listing listToolsResult
	if err := json.Unmarshal(listedAnswer.frame.Result, &listing); err != nil {
		t.Fatalf("the tools/list result is not a listing: %v", err)
	}
	listedAs := map[string]tool{}
	for _, entry := range listing.Tools {
		listedAs[entry.Name] = entry
	}

	res := toolResultOf(t, describeCapabilitiesName,
		f.callTool(t, credential, describeCapabilitiesName, map[string]any{"tools": named}))
	if res.IsError {
		t.Fatalf("the capability tool refused to define %v for a read-scoped caller: %s", named, res.Content[0].Text)
	}
	var payload describeCapabilitiesResult
	if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
		t.Fatalf("the capability payload does not decode: %v", err)
	}
	if got := capabilityNames(payload); !slices.Equal(got, named) {
		t.Fatalf("the capability tool defined %v, want exactly %v in the order named", got, named)
	}
	for _, c := range payload.Capabilities {
		entry, ok := listedAs[c.Name]
		if !ok {
			t.Errorf("%s was defined to a read-scoped caller and not listed to it by tools/list", c.Name)
			continue
		}
		if c.Description != entry.Description {
			t.Errorf("%s is defined with a different description than tools/list gave the same caller:\n"+
				"  defined: %s\n  listed:  %s", c.Name, c.Description, entry.Description)
		}
		if onTheWire(t, c.InputSchema) != onTheWire(t, entry.InputSchema) {
			t.Errorf("%s is defined with a different schema than tools/list gave the same caller:\n"+
				"  defined: %s\n  listed:  %s", c.Name, c.InputSchema, entry.InputSchema)
		}
	}
}

// namesMissingFrom names the members of want that are absent from got, sorted so
// a failure reads the same across runs.
func namesMissingFrom(want, got []string) []string {
	have := map[string]bool{}
	for _, name := range got {
		have[name] = true
	}
	var missing []string
	for _, name := range want {
		if !have[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

// sortedNames is a copy of names in a fixed order, for a failure message.
func sortedNames(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}

// TestTheToolListSaysWhoMayReuseIt is the caching half: a listing answered only
// behind a credential belongs behind it.
//
// The two transports are one assertion read in both directions. Over a listener
// the answer is served only once the request's credential has been accepted, so
// it is private and owed again on the next request — this server holds nothing
// between requests and checks the credential on every one of them, so a copy kept
// past its request would be served without that check. Over the process's own
// streams no credential is asked for, so it is the same answer for the whole life
// of the process and any cache may keep it.
//
// The stdio half is not decoration: without it the listener assertion would pass
// against a build that marked every listing private, which would tell every
// client to refetch a list that cannot change.
func TestTheToolListSaysWhoMayReuseIt(t *testing.T) {
	t.Run("over a listener it is the caller's own", func(t *testing.T) {
		f := newScopeGridFixture(t, false)
		headers := modernHeaders("tools/list", "")
		headers[headerAuthorization] = f.grantOf(t, scopeRead)
		got := f.send(t, http.MethodPost, endpointPath, headers, modernRequest(t, 1, "tools/list", nil))
		fields := wireFields(t, got.frame)
		assertCarriesAFreshnessHint(t, "tools/list", fields, "private", 0)
	})

	t.Run("over the process's own streams it is everyone's", func(t *testing.T) {
		resps, _, _, err := drive(t, perRequestRequest(1, "tools/list", ""))
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
		if len(resps) != 1 {
			t.Fatalf("want 1 response, got %d", len(resps))
		}
		assertCarriesAFreshnessHint(t, "tools/list", wireFields(t, resps[0]), "public", 3_600_000)
	})
}

// TestTheCapabilityToolStatesNoFreshnessOnEitherTransport records what the
// capability tool does about caching, which is nothing, and why that is the
// answer rather than a gap.
//
// Its answer is a tool result, and the revision this server speaks declares a
// tool result an ordinary result rather than a cacheable one — so there is no
// field on it for a scope to be stated in, on either transport. That is a
// stronger guarantee than marking it private would be: a result no cache may keep
// cannot be served to a caller whose credential has since changed, which is the
// hazard marking it private exists to bound.
//
// It is asserted here, over a listener, rather than left to the assertion over
// the process's own streams that already pins it: the two transports answer from
// one stamping site, and a change that gave a tool result a hint on one of them
// would be exactly the change this states cannot happen.
func TestTheCapabilityToolStatesNoFreshnessOnEitherTransport(t *testing.T) {
	f := newScopeGridFixture(t, false)
	got := f.callTool(t, f.grantOf(t, scopeRead), describeCapabilitiesName, map[string]any{})
	if got.frame.Error != nil {
		t.Fatalf("the capability tool was answered with an error envelope: %+v", got.frame.Error)
	}
	assertCarriesNoFreshnessHint(t, describeCapabilitiesName, wireFields(t, got.frame))
}
