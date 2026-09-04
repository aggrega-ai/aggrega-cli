// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"path"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// The name a PLACED attachment chart's manifest declares, for both of the scopes
// a Feature attachment can be attached at, driven through the shipped provider
// guests rather than through a stand-in.
//
// # What is pinned here, and it is a decision rather than the current bytes
//
// A chart the umbrella places is addressed by an identity the compiler derived,
// not by the name whoever authored that chart wrote — so the placed manifest
// declares that identity, encoded into the alphabet a chart name is written in.
// That holds for a chart a component vendors and equally for a chart a Feature
// attachment brings, at either scope, because one rule with no branch is what
// keeps two contributors from ever arriving at one name.
//
// # Why an attachment needed its own assertion
//
// The emitted path set for an attachment is pinned elsewhere, and a path set
// says nothing about any file's content: every placed byte could be the author's
// own and the pinned paths would still match. So the name an attachment's placed
// manifest declares was asserted nowhere, and a rule that holds everywhere was
// covered only where a component vendors the chart. That is a coverage hole
// rather than a doubt about the behaviour, and this file closes it.
//
// # The per-component case is the one worth reading twice
//
// Two different names belong to a chart an attachment brings, and this file
// asserts one of them. What the chart's own templates RENDER AGAINST stays its
// HOST component's name, because the author's templates name resources after it
// and a different one would rename resources already deployed. What its placed
// manifest DECLARES is the attachment's own identity — composed from the host
// and the name it is attached under — so the chart is addressable apart from
// every other chart placed beside it, its host's own included.
//
// So a chart attached to a component named "api" renders against "api" and is
// placed declaring the composed identity instead. The two answers differ on
// purpose; a reader who expects one value to serve both has merged two questions
// this split exists to keep apart.
//
// # Locating the chart, and why not by its emitted path
//
// Each case finds the chart it asserts on by CONTENT: the chart manifest nearest
// above the one emitted file carrying a string unique to that attachment's own
// template body. Nothing here pins where the render places an attachment, which
// is a position expected to move — and a case that pinned it would have to be
// rewritten by whoever moves it, at exactly the moment they most need an
// independent check. Locating by content also carries its own positive control:
// a chart root reached from a marker is provably the chart that would render
// that marker, so an assertion over the wrong chart cannot pass quietly.
//
// # The expected names are MEASURED literals, and what moves them
//
// Each expected name below was read from a real render rather than derived here.
// Deriving it in the test would mean a second implementation of the one name
// derivation the product owns, and two derivations that agree today are what
// hide the day they stop agreeing. A literal moves when the identity a chart is
// addressed by moves, or when the alphabet a chart name is written in changes —
// and either of those is a change to what a deployment author reads, so it is
// meant to be re-measured deliberately rather than absorbed.
//
// # 🔴 The reasoning behind this file has a known expiry; the assertion does not
//
// Today a chart an attachment brings is placed inside another chart, where the
// tool never loads it as a chart of its own — so the name it declares keys
// nothing, and rewriting that name buys nothing beyond consistency. A later
// change that makes such a chart discoverable turns the name into the key the
// umbrella addresses it by, in dependency resolution and in values, at which
// point the same name becomes load-bearing.
//
// That change moves the REASONING for both rows. It also moved the per-component
// row's expected VALUE, and that half has already happened: a chart declaring its
// host's name would leave the umbrella able to address neither once the two are
// discovered siblings, because a dependency binds to a sub-chart by the name that
// sub-chart declares — the same collision this rewrite exists to remove,
// reappearing one level up. So that row now expects the contributor's own
// composed identity, and the remaining expiry is the reasoning alone.
//
// So do not delete this file on the grounds that the premise behind it moved: it
// will be pinning a contract rather than a consistency, which is a stronger
// reason to keep it than the one it was written under. Update the row and keep
// the file.

// attachmentChartNameCase is one attachment scope's placed chart.
type attachmentChartNameCase struct {
	name string
	// render drives a real render and returns the emitted file map. Each case
	// reuses the render its own scope is already exercised through, so no case
	// here builds a second fixture for a shape one already covers.
	render func(*testing.T) map[string]renderer.AssembledFile
	// templateMarker is a string this attachment's own template body carries and
	// no other emitted file does. It is what locates the chart, and reaching it
	// through a template body rather than a manifest is what keeps the locator
	// independent of the field under test.
	templateMarker string
	// authoredName is the name written in the chart the attachment's author
	// supplied — what the placed manifest would declare if it were copied
	// untouched.
	authoredName string
	// wantPlacedName is the name the placed manifest declares: the identity the
	// chart is addressed by, encoded. Measured from a real render, never derived
	// here.
	wantPlacedName string
}

func attachmentChartNameCases() []attachmentChartNameCase {
	return []attachmentChartNameCase{
		{
			// Attached at a boundary, so the chart is addressed by an identity
			// composed from the scope, the boundary and the instance the author
			// named — none of which a chart name's alphabet admits whole, which
			// is why the placed name carries escapes.
			name: "a boundary-scope attachment's placed chart is named for its composed identity",
			render: func(t *testing.T) map[string]renderer.AssembledFile {
				t.Helper()
				return renderBoundaryFROutputPathCase(t, boundaryFROutputPathCase{
					name:     "a boundary-scope attachment on a chart-nesting provider",
					provider: "helm-chart",
					sourced:  true,
				})
			},
			// The resource name in the attachment's template, indented as a
			// mapping key under metadata. The indentation is what keeps it clear
			// of the manifest's own top-level name key, so this marker locates
			// the chart identically whether or not the name was rewritten.
			templateMarker: "  name: aux",
			authoredName:   "aux",
			// The scope, the boundary and the instance, joined; each join is a
			// character a chart name refuses, so each arrives as an escape.
			wantPlacedName: "bfr-zh-cluster-zh-aux",
		},
		{
			// Attached to a component, so the chart is addressed by an identity
			// composed from the component hosting it and the name it is attached
			// under — the host being what separates one instance name attached to
			// several components. The join is a character a chart name refuses,
			// which is why the placed name carries escapes.
			name:           "a per-component attachment's placed chart is named for its composed identity",
			render:         renderComponentScopeAttachmentOnChartHost,
			templateMarker: attachmentMarker,
			authoredName:   "guardrail",
			// The host and the instance, joined; each join is a character a chart
			// name refuses, so each arrives as an escape. Distinct from the host
			// component's own placed chart, which declares "api".
			wantPlacedName: "cfr-zh-api-zh-guardrail",
		},
	}
}

// declaredChartName is the name a chart manifest DECLARES, decoded rather than
// matched as text.
//
// Decoded because the claim is about the value the chart tool reads: a text
// match would also succeed on the name appearing in a comment, in another key's
// value, or anywhere else in the document, so it would assert something weaker
// than the sentence it is standing in for.
func declaredChartName(t *testing.T, body []byte) string {
	t.Helper()
	var meta struct {
		Name string `yaml:"name"`
	}
	if err := yaml.Unmarshal(body, &meta); err != nil {
		t.Fatalf("the placed chart manifest does not decode as YAML, so it declares no name at all: %v\n%s", err, body)
	}
	return meta.Name
}

// TestPlacedAttachmentChartCorpusCanFail is this corpus's own anti-needle.
//
// The negative half of each case below — that the placed manifest no longer
// declares the name its author wrote — is vacuous on a row whose expected name
// IS the authored one, and such a row passes silently: every assertion agrees,
// and nothing reports that one of them stopped discriminating. A row can arrive
// in that state by someone "correcting" an expected name to the value they see
// in the chart the author supplied, which is exactly the mistake this file
// exists to catch.
//
// It also holds the corpus to reaching both attachment scopes, because the two
// are addressed by different identities and a corpus carrying only one of them
// would leave the other's name unasserted while reading as complete.
func TestPlacedAttachmentChartCorpusCanFail(t *testing.T) {
	t.Parallel()

	cases := attachmentChartNameCases()
	if len(cases) < 2 {
		t.Fatalf("the corpus carries %d case(s); it has to reach both attachment scopes", len(cases))
	}
	for _, c := range cases {
		if c.wantPlacedName == c.authoredName {
			t.Errorf("case %q expects the placed name %q, which is the name its author wrote — so its "+
				"negative assertion holds whatever the render does and pins nothing",
				c.name, c.wantPlacedName)
		}
		if c.templateMarker == "" {
			t.Errorf("case %q names no template marker, so it has no way to locate the chart it asserts on", c.name)
		}
	}
}

// TestPlacedAttachmentChartManifestNamesItsInstanceIdentity is the assertion: a
// placed attachment chart's manifest declares the identity the umbrella
// addresses that chart by, not the name its author wrote.
func TestPlacedAttachmentChartManifestNamesItsInstanceIdentity(t *testing.T) {
	t.Parallel()

	for _, c := range attachmentChartNameCases() {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			files := c.render(t)
			root := chartRootCarrying(t, files, c.templateMarker)
			manifestKey := path.Join(root, "Chart.yaml")

			// Reported rather than pinned: this is where the render currently
			// places an attachment chart, and this case stays true when it moves.
			t.Logf("attachment chart root=%q manifest=%q", root, manifestKey)
			t.Logf("emitted keys: %v", sortedKeys(files))

			manifest, ok := files[manifestKey]
			if !ok {
				t.Fatalf("the attachment's emitted files carry no chart manifest at %q, so there is no name to read", root)
			}
			t.Logf("placed manifest:\n%s", manifest.Content)

			got := declaredChartName(t, manifest.Content)
			if got != c.wantPlacedName {
				t.Errorf("the placed chart manifest at %q declares the name %q, want %q.\n"+
					"The placed name is the identity the umbrella addresses this chart by, encoded — "+
					"so a mismatch is either that identity moving or the rewrite not happening.\nmanifest:\n%s",
					manifestKey, got, c.wantPlacedName, manifest.Content)
			}
			// Deliberately redundant while the expected name differs from the
			// authored one, and kept for the case where it stops differing: this
			// is the half that fires if an expected name is ever "corrected" to
			// the name the chart's author wrote, which is the shape a reader
			// meeting an unfamiliar placed name is most likely to reach for.
			if got == c.authoredName {
				t.Errorf("the placed chart manifest at %q still declares %q, the name its author wrote. "+
					"Two contributors may legitimately supply charts declaring one name, so a placed chart "+
					"carrying the author's own name is one the umbrella cannot address apart from another.",
					manifestKey, c.authoredName)
			}
		})
	}
}
