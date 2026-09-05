// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"go.yaml.in/yaml/v3"
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

// inspectYAML runs the real inspect dispatch over the fixture Release and
// returns everything the emission carries as searchable text: the YAML itself,
// plus every byte-sequence in it decoded back to the text those bytes spell.
//
// THE DECODE IS LOAD-BEARING AND THIS TEST IS WRONG WITHOUT IT. A node's own
// values and infra travel as raw JSON, which this encoder writes as a sequence
// of byte values rather than as a nested document — so the authored text
// appears nowhere in the emitted characters, and a plain substring search over
// them reports every one of those secrets absent whether or not it was
// withheld. Asserting on the raw characters would therefore pass on a build
// that withholds nothing at all, which is the one failure an absence assertion
// exists to catch.
func inspectYAML(t *testing.T, unredacted bool) string {
	t.Helper()
	var buf bytes.Buffer
	opts := testInspectOptions(&buf)
	opts.output = outputYAML
	opts.mode = modeForcedResolved
	opts.unredacted = unredacted
	if err := dispatchInspect(resolveTempWorkspace(t, tempWorkspaceOptions{}), yamlRedactionRelease, []string{yamlRedactionCatalog}, opts); err != nil {
		t.Fatalf("dispatchInspect(%q, unredacted=%v): %v", yamlRedactionRelease, unredacted, err)
	}
	emitted := buf.String()

	var tree any
	if err := yaml.Unmarshal([]byte(emitted), &tree); err != nil {
		t.Fatalf("the emitted graph is not YAML: %v", err)
	}
	var b strings.Builder
	b.WriteString(emitted)
	for _, decoded := range decodeByteSequences(tree) {
		b.WriteString("\n")
		b.WriteString(decoded)
	}
	return b.String()
}

// decodeByteSequences walks a decoded YAML tree and returns the text of every
// sequence that is entirely small non-negative integers — the shape a raw-JSON
// payload takes on the way through this encoder.
func decodeByteSequences(node any) []string {
	switch n := node.(type) {
	case map[string]any:
		var out []string
		for _, v := range n {
			out = append(out, decodeByteSequences(v)...)
		}
		return out
	case []any:
		raw := make([]byte, 0, len(n))
		for _, item := range n {
			i, ok := item.(int)
			if !ok || i < 0 || i > 255 {
				var out []string
				for _, v := range n {
					out = append(out, decodeByteSequences(v)...)
				}
				return out
			}
			raw = append(raw, byte(i))
		}
		if len(raw) == 0 {
			return nil
		}
		return []string{string(raw)}
	default:
		return nil
	}
}

// TestInspectReleaseYAML_WithholdsDeclaredValues is the defect this surface
// carried: the structured emission of the resolved graph printed every authored
// value, including the ones an author declared sensitive, while the readable
// layouts over the same declarations withheld them.
//
// The assertion is absence of the VALUE rather than presence of a marker, and
// that is the sharper direction: a marker can be present at one coordinate while
// a secret is still emitted at another, and only the absence assertion fails on
// that.
func TestInspectReleaseYAML_WithholdsDeclaredValues(t *testing.T) {
	t.Parallel()
	got := inspectYAML(t, false)

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

// TestInspectReleaseYAML_UnredactedEmitsThem is the other direction, and it is
// what makes the withholding above a decision rather than a limitation. A reader
// who needs the graph as the compiler produced it has a way to ask for it on the
// command that produces it, rather than being sent to a different command that
// answers a different question.
func TestInspectReleaseYAML_UnredactedEmitsThem(t *testing.T) {
	t.Parallel()
	got := inspectYAML(t, true)

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
