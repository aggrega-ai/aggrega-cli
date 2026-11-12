// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// What a deployment withdraws from itself, over a real listener.
//
// The bound under test is the deployment's rather than the caller's, so every
// assertion here is driven with a credential carrying EVERY scope this
// deployment publishes. That is the only credential that separates the two
// bounds: a token missing a scope would be refused by the scope check whatever
// the deny setting said, and a proof written against one would pass against a
// build in which the setting did nothing at all.
//
// That is a statement about a call. A listing reads no scope, so the withdrawal
// is held there under every credential, by the scope grid.

// deniedReachCredential is the grant that makes the deny setting the only thing
// left that can refuse a call.
func deniedReachCredential(t *testing.T, f *scopeGridFixture) string {
	t.Helper()
	return f.grantOf(t, scopeRead, scopeWorkspaceWrite, scopeRegistryWrite)
}

// toolsReaching names every tool in the two statically declared registries whose
// operation declares the given class, read from the operation set.
//
// It is derived rather than written out, so a publication tool added tomorrow is
// covered with no edit here — which is the property that separates this from a
// check on one tool's name.
func toolsReaching(t *testing.T, class opsurface.Reach) []string {
	t.Helper()
	var names []string
	for _, registry := range [][]toolSpec{toolRegistry, deferredToolRegistry} {
		for i := range registry {
			op, known := opsurface.Lookup(registry[i].operation)
			if !known {
				t.Fatalf("%s records the operation %q, which the operation set does not declare",
					registry[i].name, registry[i].operation)
			}
			if op.Reach == class {
				names = append(names, registry[i].name)
			}
		}
	}
	return names
}

// TestADeploymentThatWithdrewAKindOffersNoneOfIt is the listing half of the
// bound: with the publication kind withdrawn, a caller holding every scope is
// offered no tool that reaches a registry, on either listing surface.
//
// The credential is the point. It admits every class this deployment publishes a
// scope for, so nothing a scope decides can account for the absence — which is
// what makes this a proof about the setting rather than a second reading of the
// scope check.
//
// The tools that stay are asserted alongside, and they are not decoration: a
// build that refused everything would satisfy the absence above while serving
// nobody, and only naming what is still offered separates a withdrawal from an
// outage.
func TestADeploymentThatWithdrewAKindOffersNoneOfIt(t *testing.T) {
	f := newScopeGridFixtureDenying(t, false, []string{string(opsurface.ReachRegistry)})
	credential := deniedReachCredential(t, f)
	offered := append(f.listed(t, credential), f.surfaced(t, credential)...)

	withdrawn := toolsReaching(t, opsurface.ReachRegistry)
	if len(withdrawn) == 0 {
		t.Fatal("no registered tool reaches a registry, so this proof would assert nothing")
	}
	for _, name := range withdrawn {
		if slices.Contains(offered, name) {
			t.Errorf("%s reaches a registry and this deployment withdrew that kind, yet a caller holding "+
				"every scope was offered it; it was offered %v", name, sortedNames(offered))
		}
	}

	kept := toolsReaching(t, opsurface.ReachNone)
	if len(kept) == 0 {
		t.Fatal("no registered tool reaches nothing, so the control below would assert nothing")
	}
	for _, name := range kept {
		if !slices.Contains(offered, name) {
			t.Errorf("%s reaches nothing and this deployment withdrew only the publication kind, yet it "+
				"was not offered; the caller was offered %v", name, sortedNames(offered))
		}
	}
}

// TestACallIntoAWithdrawnKindNamesTheSetting is the call half, and it asserts
// the message rather than only the code.
//
// What an operator can act on is the setting's name: the caller cannot lift this
// bound with any credential, so the one useful thing the answer carries is which
// key the person running the server would change. A refusal that reported only a
// status would leave both of them with nowhere to go.
//
// The absence of a challenge is asserted too. A challenge invites a caller to
// obtain a scope and come back, and there is no scope that reaches a kind the
// deployment withdrew — so one here would send a caller round a loop it cannot
// finish.
func TestACallIntoAWithdrawnKindNamesTheSetting(t *testing.T) {
	f := newScopeGridFixtureDenying(t, false, []string{string(opsurface.ReachRegistry)})
	credential := deniedReachCredential(t, f)

	got := f.callTool(t, credential, "publish-entity", map[string]any{"entity": "component.yaml", "dry_run": true})
	if got.status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — this deployment withdrew what the call reaches (body %q)",
			got.status, got.raw)
	}
	if got.frame.Error == nil {
		t.Fatalf("a refused call was answered with a result rather than an error envelope: %q", got.raw)
	}
	if !strings.Contains(got.frame.Error.Message, "mcp.deny_reach") {
		t.Errorf("the refusal does not name the setting that produced it, so nobody reading it learns what "+
			"to change: %q", got.frame.Error.Message)
	}
	if challenge := got.header.Get(headerAuthenticate); challenge != "" {
		t.Errorf("the refusal carries the challenge %q, inviting a caller to obtain a credential that "+
			"cannot lift a bound the deployment set on itself", challenge)
	}
}

// TestADeploymentThatWithdrewNothingIsUnchanged is the negative control for the
// two proofs above: with the setting empty, the same credential is offered the
// publication tools and the same call runs.
//
// Without it, both proofs would pass against a build that had stopped offering
// registry-reaching tools for some entirely different reason.
func TestADeploymentThatWithdrewNothingIsUnchanged(t *testing.T) {
	f := newScopeGridFixture(t, false)
	credential := deniedReachCredential(t, f)
	offered := append(f.listed(t, credential), f.surfaced(t, credential)...)

	for _, name := range toolsReaching(t, opsurface.ReachRegistry) {
		if !slices.Contains(offered, name) {
			t.Errorf("%s reaches a registry and this deployment withdrew nothing, yet a caller holding "+
				"every scope was not offered it; it was offered %v", name, sortedNames(offered))
		}
	}

	got := f.callTool(t, credential, "publish-entity", map[string]any{"entity": "component.yaml", "dry_run": true})
	wantTheCallRan(t, "publish-entity", got)
}

// TestWithdrawingAKindNoCredentialReachesChangesNothing pins the one value that
// is accepted and does nothing.
//
// Machine reach is already unreachable over this transport by construction — the
// session derives no tool that carries it — so naming it withdraws a set that is
// already empty. It is accepted rather than refused because refusing it would
// make a deployment's own statement of what it does not carry depend on which
// kinds happen to be reachable today, and an operator writing the widest bound
// they can think of should not meet a startup refusal for being thorough.
func TestWithdrawingAKindNoCredentialReachesChangesNothing(t *testing.T) {
	f := newScopeGridFixtureDenying(t, false, []string{string(opsurface.ReachMachine)})
	credential := deniedReachCredential(t, f)
	offered := append(f.listed(t, credential), f.surfaced(t, credential)...)

	for _, class := range []opsurface.Reach{opsurface.ReachNone, opsurface.ReachWorkspace, opsurface.ReachRegistry} {
		for _, name := range toolsReaching(t, class) {
			if !slices.Contains(offered, name) {
				t.Errorf("%s reaches %q and this deployment withdrew only the machine kind, yet it was not "+
					"offered; the caller was offered %v", name, string(class), sortedNames(offered))
			}
		}
	}
}

// TestAWithdrawalNamingSomethingThatIsNotAKindIsRefused drives the second half
// of the closed-set bound: the configuration loader holds the value on the way
// in, and the listener holds it again where it is read.
//
// Both are needed and neither is the other's copy. The loader's refusal is what
// an operator meets, and it reads the schema; this one is what holds a
// deployment assembled any other way, and it reads the operation set. A value
// that reached here unheld would withdraw nothing while the deployment believed
// it had withdrawn something, which is the direction that fails silently.
//
// The refusal names the setting and the kinds, because the reader of it is
// choosing a value and a refusal reporting only that the value was wrong would
// hand them the search instead of the answer.
func TestAWithdrawalNamingSomethingThatIsNotAKindIsRefused(t *testing.T) {
	issuer := newAuthorizationServerDouble(t)

	// The control first: the same construction with a real kind succeeds, so the
	// refusals below are about the value rather than about the field being set.
	transport := transportFor(issuer.authorizationConfig(), issuer.egress())
	transport.DenyReach = []string{string(opsurface.ReachRegistry)}
	if _, err := newHTTPHandler(context.Background(), newCommandToolServer(t, nil), transport); err != nil {
		t.Fatalf("a withdrawal naming a real kind was refused: %v", err)
	}

	for _, tc := range []struct {
		name string
		deny []string
	}{
		{"a kind that does not exist", []string{"everything"}},
		{"a near miss on a real kind", []string{"registries"}},
		{"a real kind beside one that is not", []string{string(opsurface.ReachRegistry), "everything"}},
		{"the empty string, which is not a kind", []string{""}},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			transport := transportFor(issuer.authorizationConfig(), issuer.egress())
			transport.DenyReach = tc.deny
			_, err := newHTTPHandler(context.Background(), newCommandToolServer(t, nil), transport)
			if err == nil {
				t.Fatalf("a withdrawal naming %v was accepted; an operator asking for a bound would be "+
					"served without one", tc.deny)
			}
			if !strings.Contains(err.Error(), "mcp.deny_reach") {
				t.Errorf("the refusal does not name the setting: %v", err)
			}
			for _, class := range opsurface.Classes() {
				if !strings.Contains(err.Error(), string(class)) {
					t.Errorf("the refusal does not name %q among the kinds to write instead: %v",
						string(class), err)
				}
			}
		})
	}
}

// TestAStdioSessionCarriesNoWithdrawal pins that the setting reaches only a
// listener.
//
// The block it is written in configures one, and a caller reaching this server
// over the process's own streams is the person who launched it — bounded by
// owning the machine rather than by what a deployment chose to serve. The
// structural form of that is where the set is installed, so what this asserts is
// the consequence: a session that never went through an HTTP handler admits
// every kind.
func TestAStdioSessionCarriesNoWithdrawal(t *testing.T) {
	s := newCommandToolServer(t, nil)
	admission := s.admittedReach(context.Background())
	for _, class := range opsurface.Classes() {
		if !admission.admits(class) {
			t.Errorf("a session speaking the process's own streams does not admit %q", string(class))
		}
	}
	if admission.boundsAnything() {
		t.Error("a session speaking the process's own streams reports that it bounds something")
	}
}
