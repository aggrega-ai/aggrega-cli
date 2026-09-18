// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"sort"
	"strings"
	"testing"
)

// A tool's description and its argument schema travel in one answer, and a
// caller reads both before it composes anything. So they are held together here
// rather than separately: a schema that withholds an argument while the prose
// beside it still offers one tells a caller two different things at once, and
// the caller believes the prose, tries the argument, and is refused by the very
// answer that advertised it.
//
// Both assertions read the payload the surfaces emit rather than the literals
// the package declares. A census over the declared strings is what a reader can
// do by eye, and it is exactly what misses a description this session composed —
// which is the only kind that matters here, since composition is where the
// transport enters.

// mentionsArgument reports whether a description names the argument at all. Any
// mention is the criterion rather than a grammatical reading of the sentence: a
// caller who sees the name in the prose will try it, whatever the sentence
// around it was doing, so a mention of an argument the schema withholds is the
// defect however it is phrased.
func mentionsArgument(description, argument string) bool {
	return strings.Contains(description, argument)
}

// TestAnAdvertisedDescriptionOffersOnlyArgumentsItsSchemaDeclares walks every
// definition both surfaces advertise, on both transports, and holds each
// description against the schema shipped beside it.
//
// The two rows are one assertion read in both directions. The listener row is
// the property under test. The standard-streams row is the control that keeps it
// from passing vacuously: on the transport that still accepts those arguments
// the descriptions must still offer them, so a change that emptied every
// description would fail here rather than reporting the listener clean.
func TestAnAdvertisedDescriptionOffersOnlyArgumentsItsSchemaDeclares(t *testing.T) {
	overHTTP := advertisedOverHTTP(t, newHTTPFixture(t, defaultTransportConfig(), nil))
	overStdio := advertisedOverStdio(t, newCommandToolServer(t, nil))

	for _, tc := range []struct {
		name string
		// advertised is every definition that transport offered.
		advertised []advertisement
		// offersWorkspaceArguments is whether a caller on that transport may name a
		// workspace, and so whether the descriptions may say it can.
		offersWorkspaceArguments bool
	}{
		{"reached over a listener", overHTTP, false},
		{"reached over the process's own streams", overStdio, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.advertised) == 0 {
				t.Fatal("no definition was advertised at all; the walk measured nothing")
			}

			var offering []string
			for _, a := range tc.advertised {
				if a.description == "" {
					t.Errorf("%s advertises %s with no description at all", a.surface, a.tool)
					continue
				}
				for _, argument := range workspaceSelectionArguments {
					if !mentionsArgument(a.description, argument) {
						continue
					}
					offering = append(offering, a.surface+"/"+a.tool+" names "+argument)
					if declared := workspaceArgumentSites(t, a.schema, []string{argument}); len(declared) == 0 {
						t.Errorf("%s advertises %s with a description naming %s, which its own schema "+
							"beside it does not declare — a caller reading the two is told the argument "+
							"is accepted and refused for sending it:\n%s",
							a.surface, a.tool, argument, a.description)
					}
				}
			}

			sort.Strings(offering)
			if !tc.offersWorkspaceArguments {
				return
			}
			if len(offering) == 0 {
				t.Fatal("no advertised description names an argument that selects a workspace, " +
					"so the walk proves nothing about the transport that withholds them")
			}
			t.Logf("%d advertised description(s) name an argument that selects a workspace: %v",
				len(offering), offering)
		})
	}
}

// TestATransportThatAcceptsAWorkspaceArgumentStillSaysSo is the other half of
// the control: not merely that some description still offers one, but that every
// tool declaring the sentence still ships it where the argument works.
//
// It names the difference rather than counting the two sides, because two sets
// both short by one still agree on length.
func TestATransportThatAcceptsAWorkspaceArgumentStillSaysSo(t *testing.T) {
	s := newCommandToolServer(t, nil)

	declaring := map[string]string{}
	for _, spec := range registeredToolSpecs(s) {
		if spec.workspaceSelectionNote != "" {
			declaring[spec.name] = spec.workspaceSelectionNote
		}
	}
	if len(declaring) == 0 {
		t.Fatal("no registered tool declares a sentence about naming a workspace; the assertion measured nothing")
	}

	advertised := advertisedOverStdio(t, s)
	carrying := map[string]string{}
	for _, a := range advertised {
		carrying[a.tool] = a.description
	}

	var dropped []string
	for name, note := range declaring {
		// A tool these surfaces never offer cannot be measured here.
		description, offered := carrying[name]
		if !offered {
			continue
		}
		if !strings.Contains(description, note) {
			dropped = append(dropped, name)
		}
	}
	sort.Strings(dropped)
	if len(dropped) > 0 {
		t.Errorf("%v declare a sentence about naming a workspace and no longer advertise it on the "+
			"transport that accepts one, so a caller that may send it is not told so", dropped)
	}
}

// TestTheTwoTransportsDifferOnlyByWhatTheListenerWithholds pins what the
// scoping does not touch.
//
// A description is withheld from, never rewritten for, a transport. Comparing
// the two answers with the withheld parts taken off the stdio side catches a
// scoping that composed a second description rather than declining to add one —
// which would satisfy every assertion above while quietly serving two different
// texts.
//
// Two parts are withheld, and both are withheld for the same reason: each offers
// the caller something the listener does not have. The workspace sentence offers
// arguments a call over HTTP is refused for sending. The pairing sentence names
// the tool that reaches the same capability by running a command, and a session
// reached over HTTP has none — so a curated tool still carrying it would send a
// caller after a tool that is not there.
func TestTheTwoTransportsDifferOnlyByWhatTheListenerWithholds(t *testing.T) {
	s := newCommandToolServer(t, nil)
	overHTTP := advertisedOverHTTP(t, newHTTPFixture(t, defaultTransportConfig(), nil))
	overStdio := advertisedOverStdio(t, s)

	notes := map[string]string{}
	operations := map[string]string{}
	for _, spec := range registeredToolSpecs(s) {
		notes[spec.name] = spec.workspaceSelectionNote
		operations[spec.name] = spec.operation
	}
	// The pairing sentence is recomposed from the session's own twins rather than
	// matched by shape, so what is trimmed here is the exact text the composition
	// added and a reworded sentence fails rather than being trimmed anyway.
	twins := commandTwins(s.commandTools)

	remote := map[string]string{}
	for _, a := range overHTTP {
		remote[a.surface+"/"+a.tool] = a.description
	}

	compared, withheldWorkspace, withheldPairing := 0, 0, 0
	for _, a := range overStdio {
		got, ok := remote[a.surface+"/"+a.tool]
		if !ok {
			continue
		}
		compared++
		want := a.description
		if note := notes[a.tool]; note != "" {
			want = strings.TrimSuffix(want, " "+note)
			withheldWorkspace++
		}
		if twin, paired := twins[operations[a.tool]]; paired {
			want = strings.TrimSuffix(want, commandTwinNote(twin))
			withheldPairing++
		}
		if got != want {
			t.Errorf("%s advertises %s with a description that differs by more than what the listener "+
				"withholds:\n  over a listener: %s\n  expected:        %s", a.surface, a.tool, got, want)
		}
	}
	if compared == 0 {
		t.Fatal("no description was compared across the two transports; the assertion measured nothing")
	}
	if withheldWorkspace == 0 {
		t.Fatal("no compared description carried a workspace sentence to withhold, " +
			"so the comparison would pass against a scoping that does nothing")
	}
	if withheldPairing == 0 {
		t.Fatal("no compared description carried a pairing sentence to withhold, " +
			"so the comparison would pass against a session that composes none")
	}
	t.Logf("compared %d description(s) across the two transports, %d carrying a withheld workspace "+
		"sentence and %d a withheld pairing sentence", compared, withheldWorkspace, withheldPairing)
}

// TestAdvertisedDescriptionWithholdsOnlyOnTheListener measures the composition
// itself, on the shapes the surface walk cannot reach because no tool is written
// in them today.
//
// A spec with no sentence to withhold is the positive control: without it every
// row would pass against a composition that returned the bare description.
func TestAdvertisedDescriptionWithholdsOnlyOnTheListener(t *testing.T) {
	for _, tc := range []struct {
		// scenario says what the row is about, so a failure reads as a rule.
		scenario  string
		transport transportMode
		spec      toolSpec
		want      string
	}{
		{
			scenario:  "the sentence is offered where the argument works",
			transport: transportStdio,
			spec:      toolSpec{description: "Does a thing.", workspaceSelectionNote: "Optional `workspace_root` overrides discovery."},
			want:      "Does a thing. Optional `workspace_root` overrides discovery.",
		},
		{
			scenario:  "the sentence is withheld where the argument is refused",
			transport: transportHTTP,
			spec:      toolSpec{description: "Does a thing.", workspaceSelectionNote: "Optional `workspace_root` overrides discovery."},
			want:      "Does a thing.",
		},
		{
			scenario:  "a tool saying nothing about a workspace is advertised as authored",
			transport: transportStdio,
			spec:      toolSpec{description: "Does a thing."},
			want:      "Does a thing.",
		},
		{
			scenario:  "and is advertised as authored on the other transport too",
			transport: transportHTTP,
			spec:      toolSpec{description: "Does a thing."},
			want:      "Does a thing.",
		},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			s := &server{transport: tc.transport}
			if got := s.advertisedDescription(tc.spec); got != tc.want {
				t.Errorf("the composition produced\n  %q\nwant\n  %q", got, tc.want)
			}
		})
	}
}
