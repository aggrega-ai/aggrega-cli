// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// What a credential's scopes admit, over a real listener, with the tokens minted
// by an authorization server running in this process.
//
// The grid is driven rather than reasoned about: every cell issues a real
// request and reads a real answer. Its margins are the four grants an
// authorization server can issue for this deployment crossed with one tool per
// reach class; its interior is the build, which declares no reach and widens to
// the workspace when the composition that decides a dependency fill says it
// fills. A grid specified by its margins alone would discharge every clause it
// was written for and leave that interior untested, which is the one cell the
// whole escalation exists for.

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

	issuer := newAuthorizationServerDouble(t)
	s := newCommandToolServer(t, nil)
	s.startDir = root
	s.announcedTools = announce
	transport := transportFor(issuer.authorizationConfig(), issuer.egress())
	transport.DenyReach = deny
	h, err := newHTTPHandler(context.Background(), s, transport)
	if err != nil {
		t.Fatalf("newHTTPHandler: %v", err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return &scopeGridFixture{httpFixture: &httpFixture{ts: ts, h: h}, issuer: issuer, root: root}
}

// grantOf mints a token carrying exactly the named scopes.
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
	got := f.callTool(t, credential, describeCapabilitiesName, map[string]any{})
	if got.status != http.StatusOK {
		t.Fatalf("the capability tool was answered %d (body %q)", got.status, got.raw)
	}
	if got.frame.Error != nil {
		t.Fatalf("the capability tool was answered with an error envelope: %+v", got.frame.Error)
	}
	var result struct {
		StructuredContent describeCapabilitiesResult `json:"structuredContent"`
	}
	if err := json.Unmarshal(got.frame.Result, &result); err != nil {
		t.Fatalf("the capability tool's result does not carry a capability listing: %v (body %q)", err, got.raw)
	}
	return capabilityNames(result.StructuredContent)
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

// gridColumns is one tool per reach class, and the build twice.
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
	}
}

// gridGrants is the four credentials an authorization server can issue here.
func gridGrants() []struct {
	name  string
	grant []string
} {
	return []struct {
		name  string
		grant []string
	}{
		{"a credential carrying the read scope alone", []string{scopeRead}},
		{"a credential carrying the workspace-write scope alone", []string{scopeWorkspaceWrite}},
		{"a credential carrying the registry-write scope alone", []string{scopeRegistryWrite}},
		{"a credential carrying all three", []string{scopeRead, scopeWorkspaceWrite, scopeRegistryWrite}},
	}
}

// TestWhatEachCredentialIsOfferedAndMayCall is the grid, every cell asserted.
//
// Every cell is asserted including the ones that pass. A grid that checked only
// its refusals would report a deployment that refused everything as correct, and
// a grid that checked only what was offered would miss a tool withheld from the
// list and still callable by name — which is the shape a caller that read about
// a tool elsewhere actually produces.
//
// Planted defects, demonstrated: drop ReachNone from scopeImplications's
// workspace-write entry and the describe cell under that credential reddens;
// make buildReach return ReachWorkspace unconditionally, or delete its entry from
// the spec, and the interaction cell reddens.
func TestWhatEachCredentialIsOfferedAndMayCall(t *testing.T) {
	f := newScopeGridFixture(t, false)

	for _, row := range gridGrants() {
		t.Run(row.name, func(t *testing.T) {
			credential := f.grantOf(t, row.grant...)
			offered := append(f.listed(t, credential), f.surfaced(t, credential)...)

			for _, column := range gridColumns() {
				t.Run(column.scenario, func(t *testing.T) {
					admitted := admittedByGrant(row.grant, column.class)

					// What a caller is offered is per tool rather than per cell:
					// the two build columns are one tool, listed under the class
					// its operation declares, and refused at the call that widens
					// past what the credential admits. Which registry a tool sits
					// in decides the order it is listed in and nothing else, so
					// the union is what the credential decides.
					listedFor := admittedByGrant(row.grant, declaredReachOfTool(t, column.tool))
					if got := slices.Contains(offered, column.tool); got != listedFor {
						t.Errorf("%s is offered = %v, want %v: this credential %s what its operation "+
							"declares. It was offered %v", column.tool, got, listedFor,
							map[bool]string{true: "admits", false: "does not admit"}[listedFor],
							offered)
					}

					got := f.callTool(t, credential, column.tool, column.args)
					if admitted {
						wantTheCallRan(t, column.tool, got)
						return
					}
					wantRefusedForWantOfScope(t, column.tool, got, scopeAdmittingInThisTest(column.class))
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

// TestTheTwoListingSurfacesAgreeForOneCredential is the agreement, asserted by
// set difference in both directions.
//
// A count would prove nothing: two sets both short by the same tool agree on
// length and disagree about everything that matters. So each direction names what
// it found — a tool the credential admits that neither surface offered, and a tool
// some surface offered that the credential does not admit — and the second
// direction is the one that would catch a filter applied to one surface and not
// the other.
//
// The expected set is derived from the registries rather than written out, so a
// tool added tomorrow is covered without an edit here; what is NOT derived is the
// mapping from a grant to the classes it admits, which is restated above for the
// reason stated there.
func TestTheTwoListingSurfacesAgreeForOneCredential(t *testing.T) {
	f := newScopeGridFixture(t, false)

	for _, row := range gridGrants() {
		t.Run(row.name, func(t *testing.T) {
			credential := f.grantOf(t, row.grant...)
			offered := append(f.listed(t, credential), f.surfaced(t, credential)...)

			var want []string
			for _, registry := range [][]toolSpec{toolRegistry, deferredToolRegistry} {
				for i := range registry {
					op, known := opsurface.Lookup(registry[i].operation)
					if !known {
						t.Fatalf("%s records the operation %q, which the operation set does not declare",
							registry[i].name, registry[i].operation)
					}
					if admittedByGrant(row.grant, op.Reach) {
						want = append(want, registry[i].name)
					}
				}
			}

			if missing := namesMissingFrom(want, offered); len(missing) > 0 {
				t.Errorf("this credential admits %v, and neither surface offered them; it was offered %v",
					missing, sortedNames(offered))
			}
			if extra := namesMissingFrom(offered, want); len(extra) > 0 {
				t.Errorf("%v were offered to this credential, which admits none of them; it admits %v",
					extra, sortedNames(want))
			}
		})
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

// TestTheToolListSaysWhoMayReuseIt is the caching half: a listing shaped by the
// caller's own credential belongs to that caller alone.
//
// The two transports are one assertion read in both directions. Over a listener
// the answer is narrowed by what the request's credential admits, so it is
// private and owed again on the next request — this server holds nothing between
// requests, and the very next one on the same connection may present a different
// credential. Over the process's own streams nothing narrows it, so it is the
// same answer for the whole life of the process and any cache may keep it.
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
