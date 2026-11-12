// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// What this server announces, and what naming a narrower set does and does not
// change.
//
// The setting narrows an ANNOUNCEMENT. Every assertion here is written so that
// it separates that from the two bounds it sits beside — what whoever runs this
// server withdrew, and what a credential admits — because the failure worth
// catching is one of the three quietly doing another's job.

// announcing renders a set of names as the server carries it. A nil result is
// what a configuration naming nothing yields and announces everything.
func announcing(names ...string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = true
	}
	return out
}

// TestAnnouncingNothingInParticularAnnouncesEverything is the control every
// assertion below needs: with no narrowing, a tool named in none of them is
// still offered, so a narrowed listing cannot pass because nothing was ever
// offered.
func TestAnnouncingNothingInParticularAnnouncesEverything(t *testing.T) {
	s := newTestServer(t)
	names := listedNames(t, s)
	for _, want := range []string{"validate", "lint", "cache-status"} {
		if !containsStr(names, want) {
			t.Errorf("a server told to announce nothing in particular did not offer %q; it offered %v",
				want, names)
		}
	}
}

// TestANarrowedAnnouncementOffersExactlyTheNamedTools is the listing half of
// the setting: what is named is offered, what is not is absent, and the order
// the registries fix is kept.
func TestANarrowedAnnouncementOffersExactlyTheNamedTools(t *testing.T) {
	s := newTestServer(t)
	// The capability tool is announced alongside the two under test because the
	// setting narrows EXACTLY what it names and grants nothing an exemption: a
	// narrowed set that left it out would refuse the call the second half of this
	// test makes, which is the behaviour rather than a gap in it.
	s.announcedTools = announcing("validate", "describe-capabilities", "lint")

	got := listedNames(t, s)
	want := []string{"validate", "describe-capabilities", "lint"}
	if !slices.Equal(got, want) {
		t.Fatalf("tools/list offered %v, want exactly %v in registry order", got, want)
	}

	// The capability tool reads the same admission, so the two surfaces agree
	// about one request rather than agreeing because neither narrowed.
	index := indexOf(t, s)
	surfaced := capabilityNames(index)
	if !containsStr(surfaced, "lint") {
		t.Errorf("the capability tool did not surface the announced lint tool: %v", surfaced)
	}
	if containsStr(surfaced, "cache-status") {
		t.Errorf("the capability tool surfaced cache-status, which this server does not announce: %v", surfaced)
	}
}

// TestAnEmptyAnnouncementOffersNothing pins the value that is one character from
// the value above and means the opposite of absent.
//
// It is asserted because a configuration reader that folded an empty list into
// "nothing was said" would leave whoever wrote one being served the whole
// surface, silently, which is the reading that cannot be told from a working one
// by looking at the running server.
func TestAnEmptyAnnouncementOffersNothing(t *testing.T) {
	s := newTestServer(t)
	s.announcedTools = announcing()

	if got := listedNames(t, s); len(got) != 0 {
		t.Errorf("a server announcing an empty set offered %v", got)
	}
}

// TestAToolLeftOutOfTheAnnouncementIsRefusedAtTheCall is the call half, and it
// is the half that matters: a name absent from a listing is still a name on the
// wire, so an announcement enforced only at the listing would hide tools from a
// client that read the list and hide nothing from one that did not.
//
// The announced tool is driven alongside, and it is not decoration: a build that
// refused every call would satisfy the refusal below while serving nobody.
func TestAToolLeftOutOfTheAnnouncementIsRefusedAtTheCall(t *testing.T) {
	s := newTestServer(t)
	s.announcedTools = announcing("lint")
	s.runLint = func(_ context.Context, _ lintRequest, stdout, _ io.Writer) error {
		_, _ = io.WriteString(stdout, "lint passed\n")
		return nil
	}

	params, err := json.Marshal(callToolParams{Name: "cache-status", Arguments: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	_, rpcErr := s.handleToolsCall(context.Background(), params)
	if rpcErr == nil {
		t.Fatal("a call naming a tool this server does not announce was answered rather than refused")
	}
	if !strings.Contains(rpcErr.Message, "mcp.announce_tools") {
		t.Errorf("the refusal does not name the setting that produced it, so nobody reading it learns "+
			"what to change: %q", rpcErr.Message)
	}

	if res := callTool(t, s, "lint", lintArgs{Entity: "catalog/component/api.yaml"}); res.IsError {
		t.Fatalf("the announced tool was refused too, so the refusal above says nothing about the "+
			"setting: %s", res.Content[0].Text)
	}
}

// TestAnAnnouncementNamingSomethingThatIsNotAToolIsRefused holds the setting to
// the names this process can serve.
//
// The generated row is the one that is not obvious and is the reason the
// vocabulary is derived from the command surface rather than from the
// registries: one configuration file serves both transports, so a name that
// would name a generated tool has to be accepted even where the session carries
// none — refusing it would make a workspace's statement of what it announces
// depend on how a client happened to reach the server.
func TestAnAnnouncementNamingSomethingThatIsNotAToolIsRefused(t *testing.T) {
	commandTools := deriveCommandTools(fixtureCommandSurface())
	if len(commandTools) == 0 {
		t.Fatal("the fixture command surface derived no tool, so the generated row below asserts nothing")
	}
	generated := commandTools[0].name

	for _, tc := range []struct {
		name     string
		declared []string
		refused  bool
	}{
		{"a name no tool answers to", []string{"no-such-tool"}, true},
		{"a near miss on a real tool", []string{"lints"}, true},
		{"a real tool beside one that is not", []string{"validate", "no-such-tool"}, true},
		{"the empty string, which is not a name", []string{""}, true},
		{"a tool in the set a caller meets first", []string{"validate"}, false},
		{"a tool in the tail behind it", []string{"lint"}, false},
		{"a tool generated from a command", []string{generated}, false},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := announcedToolNames(tc.declared, commandTools)
			if tc.refused {
				if err == nil {
					t.Fatalf("announcing %v was accepted; whoever asked for a narrower surface would be "+
						"served a different one with nothing saying so", tc.declared)
				}
				if !strings.Contains(err.Error(), "mcp.announce_tools") {
					t.Errorf("the refusal does not name the setting: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("announcing %v was refused: %v", tc.declared, err)
			}
			for _, name := range tc.declared {
				if !got[name] {
					t.Errorf("announcing %v did not carry %q", tc.declared, name)
				}
			}
		})
	}

	// The two empty spellings are different statements, and the reader that has
	// to tell them apart is this one.
	if got, err := announcedToolNames(nil, commandTools); err != nil || got != nil {
		t.Errorf("a configuration naming nothing yielded %v (err %v), want the value that announces "+
			"everything", got, err)
	}
	if got, err := announcedToolNames([]string{}, commandTools); err != nil || got == nil || len(got) != 0 {
		t.Errorf("a configuration naming an empty list yielded %v (err %v), want a set that announces "+
			"nothing", got, err)
	}
}

// TestAWithdrawalWinsOverAnAnnouncement is the interaction, over a real
// listener, with a credential carrying every scope this server publishes.
//
// The credential is what makes it a proof about the two settings rather than a
// second reading of the scope check: nothing a scope decides can account for the
// absence below.
//
// It is written as a NEEDLE rather than as an absence. The same fixture is
// driven twice — once withdrawing nothing, once withdrawing the publication kind
// — with the announcement naming the same tools both times, and the listing must
// shrink by exactly the tools of that kind. Without the first run, a build whose
// announcement silently dropped everything would satisfy the second perfectly.
func TestAWithdrawalWinsOverAnAnnouncement(t *testing.T) {
	withdrawn := toolsReaching(t, opsurface.ReachRegistry)
	if len(withdrawn) == 0 {
		t.Fatal("no registered tool reaches a registry, so this proof would assert nothing")
	}
	kept := toolsReaching(t, opsurface.ReachNone)
	if len(kept) == 0 {
		t.Fatal("no registered tool reaches nothing, so the control below would assert nothing")
	}
	announce := announcing(append(append([]string(nil), withdrawn...), kept...)...)

	open := newScopeGridFixtureWith(t, false, nil, announce)
	before := open.listed(t, deniedReachCredential(t, open))

	closed := newScopeGridFixtureWith(t, false, []string{string(opsurface.ReachRegistry)}, announce)
	after := closed.listed(t, deniedReachCredential(t, closed))

	for _, name := range withdrawn {
		if !slices.Contains(before, name) {
			t.Fatalf("%s reaches a registry and nothing withdrew it, yet the announcement did not offer "+
				"it; the caller was offered %v. The comparison below would then measure the "+
				"announcement rather than the withdrawal", name, sortedNames(before))
		}
		if slices.Contains(after, name) {
			t.Errorf("%s reaches a registry, whoever runs this server withdrew that kind and the announcement "+
				"named the tool anyway, yet a caller holding every scope was offered it; it was "+
				"offered %v", name, sortedNames(after))
		}
	}
	for _, name := range kept {
		if !slices.Contains(after, name) {
			t.Errorf("%s reaches nothing and is announced, yet withdrawing the publication kind took it "+
				"away too; the caller was offered %v", name, sortedNames(after))
		}
	}
	if len(before)-len(after) != len(withdrawn) {
		t.Errorf("the listing shrank by %d tools when the publication kind was withdrawn, and that kind "+
			"has %d tools; before %v, after %v", len(before)-len(after), len(withdrawn),
			sortedNames(before), sortedNames(after))
	}

	// The call half, on the same pair: the announcement names the tool and the
	// withdrawal still refuses it, naming the setting a reader can act on.
	got := closed.callTool(t, deniedReachCredential(t, closed), "publish-entity",
		map[string]any{"entity": "component.yaml", "dry_run": true})
	if got.status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — this server withdrew what the call reaches (body %q)",
			got.status, got.raw)
	}
	if got.frame.Error == nil || !strings.Contains(got.frame.Error.Message, "mcp.deny_reach") {
		t.Errorf("the refusal does not name the withdrawal, so an announcement naming the tool reads as "+
			"the thing that turned the call away: %q", got.raw)
	}
}
