// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
)

// The fixture is the one the per-authored-leaf marking is proved against, so the
// values asserted here are the same values that build marks, reached through the
// real pipeline rather than through a hand-built graph. A fixture assembled here
// could carry a marking the compiler never produces.
const (
	yamlRedactionRelease   = "../../internal/compiler/testdata/authored-leaf-marking/release.yaml"
	yamlRedactionAggregate = "../../internal/compiler/testdata/authored-leaf-marking/aggregate.yaml"
	yamlRedactionCatalog   = "../../internal/compiler/testdata/authored-leaf-marking/catalog/base"
)

// authoredSecrets are the values the fixture's author declared sensitive, one
// per shape the declaration can take. Each must be absent from a withheld
// emission and present in an unredacted one, so the pair of assertions below
// reads the same list from both directions.
var authoredSecrets = []struct {
	value string
	why   string
}{
	{"hunter2-plaintext", "a leaf the Component schema declares directly"},
	{"nested-plaintext", "a leaf beneath an object the schema declares"},
	{"deep-plaintext", "a leaf two levels beneath the declaration"},
	{"backup-plaintext", "the infra pillar, which a reading of values alone would miss"},
	{"vault-token-plaintext", "an attachment's own values, declared by its Feature rather than by the Component"},
	{"ak-plaintext", "a boundary attachment's infra, which no node-level reading reaches"},
	{"store-token-plaintext", "a leaf beneath an object in a boundary attachment's infra"},
}

// authoredPublic are values in the same payloads that no declaration covers.
// They must survive both emissions: over-withholding declares public data secret
// and is the failure in this axis with no cheap undo.
var authoredPublic = []string{"demo", "not-a-secret", "visible", "boundary-wide"}

// inspectGraph runs the real inspect dispatch over the fixture Release with the
// graph requested, and returns the emitted document as searchable text.
//
// A plain substring search is sound over this emission and would NOT have been
// over the one this test was first written against. A node's own values and
// infra travel as raw JSON: a JSON encoder writes them as the nested document
// they already are, so an authored value that survived appears verbatim in the
// characters, while the encoder this surface used before wrote them as a
// sequence of byte values in which no authored text appeared at all — under
// which an absence assertion passed whether or not anything was withheld.
//
// The public values asserted below are the standing positive control for that
// claim: they are in the same payloads and must be found as plain text, so a
// search that could not see into the emission fails there rather than passing
// everywhere.
func inspectGraph(t *testing.T, unredacted bool) string {
	t.Helper()
	var buf bytes.Buffer
	opts := testInspectOptions(&buf)
	opts.irGraph = true
	opts.mode = modeForcedResolved
	opts.unredacted = unredacted
	if err := dispatchInspect(resolveTempWorkspace(t, tempWorkspaceOptions{}), yamlRedactionRelease, []string{yamlRedactionCatalog}, opts); err != nil {
		t.Fatalf("dispatchInspect(%q, unredacted=%v): %v", yamlRedactionRelease, unredacted, err)
	}
	// The graph leads the stream and the readable view follows it, so what is
	// searched below is the document alone. Searching the whole stream would let
	// a value that survived in the readable layout satisfy an assertion about the
	// graph, and vice versa.
	document, _ := splitLeadingGraph(t, buf.String())

	var tree any
	if err := json.Unmarshal([]byte(document), &tree); err != nil {
		t.Fatalf("the emitted graph is not JSON: %v", err)
	}
	return document
}

// TestInspectReleaseGraph_WithholdsDeclaredValues is the defect this surface
// carried: the structured emission of the resolved graph printed every authored
// value, including the ones an author declared sensitive, while the readable
// layouts over the same declarations withheld them.
//
// The assertion is absence of the VALUE rather than presence of a marker, and
// that is the sharper direction: a marker can be present at one coordinate while
// a secret is still emitted at another, and only the absence assertion fails on
// that.
//
// It follows the graph onto the flag that now asks for it rather than being
// retired with the format value that used to. What is worth pinning is that the
// document a person can obtain withholds; which flag obtains it is a spelling.
func TestInspectReleaseGraph_WithholdsDeclaredValues(t *testing.T) {
	t.Parallel()
	got := inspectGraph(t, false)

	for _, secret := range authoredSecrets {
		if strings.Contains(got, secret.value) {
			t.Errorf("the emitted graph carries %q in the clear — %s", secret.value, secret.why)
		}
	}
	if !strings.Contains(got, redact.WithheldMarkerKey()) {
		t.Error("the emitted graph names no withheld value; the absence assertions above would also pass on an empty graph")
	}
	for _, public := range authoredPublic {
		if !strings.Contains(got, public) {
			t.Errorf("the emitted graph does not carry %q, which no declaration covers; withholding it declares public data secret", public)
		}
	}
}

// TestInspectReleaseGraph_UnredactedEmitsThem is the other direction, and it is
// what makes the withholding above a decision rather than a limitation. A reader
// who needs the graph as the compiler produced it has a way to ask for it on the
// command that produces it, rather than being sent to a different command that
// answers a different question.
func TestInspectReleaseGraph_UnredactedEmitsThem(t *testing.T) {
	t.Parallel()
	got := inspectGraph(t, true)

	for _, secret := range authoredSecrets {
		if !strings.Contains(got, secret.value) {
			t.Errorf("--unredacted did not emit %q — %s", secret.value, secret.why)
		}
	}
	if strings.Contains(got, redact.WithheldMarkerKey()) {
		t.Error("--unredacted emitted a withheld marker; the reader asked for the graph as the compiler produced it")
	}
}

// TestInspectAggregateText_UnredactedEmitsThem states that the affordance
// reaches the readable layouts too, which is what the command's help says of it.
// The two surfaces withhold from one declaration, so a flag revealing on only
// one of them would leave the help true of half the command.
func TestInspectAggregateText_UnredactedEmitsThem(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		name       string
		unredacted bool
		wantSecret bool
	}{
		{"withheld by default", false, false},
		{"revealed with the affordance", true, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			opts := testInspectOptions(&buf)
			opts.mode = modeForcedResolved
			opts.unredacted = row.unredacted
			if err := dispatchInspect(resolveTempWorkspace(t, tempWorkspaceOptions{}), yamlRedactionAggregate, []string{yamlRedactionCatalog}, opts); err != nil {
				t.Fatalf("dispatchInspect(%q): %v", yamlRedactionAggregate, err)
			}
			got := buf.String()

			const secret = "hunter2-plaintext"
			if strings.Contains(got, secret) != row.wantSecret {
				t.Errorf("readable layout with unredacted=%v: carries %q = %v, want %v\n--- output ---\n%s",
					row.unredacted, secret, strings.Contains(got, secret), row.wantSecret, got)
			}
			if strings.Contains(got, redact.Placeholder()) == row.wantSecret {
				t.Errorf("readable layout with unredacted=%v: placeholder presence is %v, which contradicts the value assertion",
					row.unredacted, strings.Contains(got, redact.Placeholder()))
			}
		})
	}
}
