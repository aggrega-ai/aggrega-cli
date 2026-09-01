// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end coverage of the names a Kubernetes render declares it emitted, and
// of the halt the host raises when two contributors to one assembled output claim
// the same one.
//
// The fixture is the shape that made this reachable in ordinary authoring. A
// Feature attachment delivering a chart its author supplied renders against the
// HOST component's release name, so two attachments on one component render
// against the same release name — and a chart naming a resource after
// .Release.Name is the commonest thing a chart does. Two such attachments emit two
// resources with one name, in one boundary, and only their directories differ.
//
// Both directions are built, because a check that refuses both is worse than
// none: one release attaches two charts that name one resource alike, and the
// other attaches two that name their resources apart.

package compiler_test

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// derivedNameInput compiles the fixture for one of its two releases.
func derivedNameInput(variant string) pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath:  "testdata/derived-name-collision/release-" + variant + ".yaml",
		CatalogPaths: []string{"testdata/derived-name-collision/catalog/base"},
	}
}

// TestDerivedNames_TwoAttachmentsNamingOneResourceHalt is the case the guest's
// declaration exists for.
//
// The two charts each write `metadata.name: {{ .Release.Name }}` and both render
// against the host component's release name, so both emit a Service named alpha.
// The host cannot see either name — they live inside file bytes it writes verbatim
// — so the halt is possible only because the guest declared what it emitted.
//
// The assertion is against that halt specifically rather than against any failure:
// a build that fell over for some other reason would not prove the names were what
// stopped it. And it asserts the halt names BOTH attachments, because renaming one
// of them is the author's remedy and a halt naming only one asserts a choice the
// compiler has not made about which of the two was intended.
func TestDerivedNames_TwoAttachmentsNamingOneResourceHalt(t *testing.T) {
	t.Parallel()

	_, err := pipelinetest.BuildE(t, derivedNameInput("collide"))
	if err == nil {
		t.Fatal("two attachments emitting one resource name built cleanly; want a halt naming both")
	}
	if !errors.Is(err, renderer.ErrDerivedKeyCollision()) {
		t.Fatalf("the build failed for a different reason than the name collision: %v", err)
	}

	// The halt carries one finding per contributor per colliding name, wrapped in a
	// report whose own text names only what they have in common — so the findings
	// are read through the set it wraps rather than searched for in the halt as a
	// string.
	var envelope *cerrors.CompilerError
	if !errors.As(err, &envelope) {
		t.Fatalf("the halt is not a categorised diagnostic: %v", err)
	}
	joined, isMulti := envelope.Wrap.(interface{ Unwrap() []error })
	if !isMulti {
		t.Fatalf("the report wraps a single error rather than the set the rule raised: %T", envelope.Wrap)
	}
	findings := joined.Unwrap()
	if len(findings) != 2 {
		t.Fatalf("expected one finding per contributor, got %d: %v", len(findings), findings)
	}
	texts := make([]string, 0, len(findings))
	for _, f := range findings {
		texts = append(texts, f.Error())
	}
	whole := strings.Join(texts, "\n")

	for _, want := range []string{
		// The colliding name, byte-identical to what the release name rendered to,
		// so it doubles as the term to search the charts for.
		`"alpha"`,
		// The space, naming the two coordinates that bound a resource name.
		`key space "resource-names:Service--default"`,
		// Both attachments, in the author's own words for them.
		`derived by component "alpha", carrying attachment "one"`,
		`derived by component "alpha", carrying attachment "two"`,
	} {
		if !strings.Contains(whole, want) {
			t.Errorf("the halt does not carry %q, so the author cannot act on it\ngot:\n%s", want, whole)
		}
	}
	t.Logf("the collision halts with %d finding(s):\n%s", len(texts), whole)
}

// TestDerivedNames_TwoAttachmentsNamingDistinctResourcesBuild is the negative
// direction, and it carries two distinct classes rather than one.
//
// Two attachments emitting DIFFERENT names must build — a check that refused this
// would refuse most correct authoring. And the host component's own Deployment is
// named alpha, exactly as the attachment's Service is: a name shared across two
// KINDS must build too, because that is ordinary Kubernetes and a scope that
// dropped the kind would halt on it.
func TestDerivedNames_TwoAttachmentsNamingDistinctResourcesBuild(t *testing.T) {
	t.Parallel()

	res := pipelinetest.Build(t, derivedNameInput("distinct"))

	var services, deployments []string
	for _, body := range res.Artifacts {
		names := resourceNamesOf(string(body))
		switch {
		case strings.Contains(string(body), "kind: Service"):
			services = append(services, names...)
		case strings.Contains(string(body), "kind: Deployment"):
			deployments = append(deployments, names...)
		}
	}
	sort.Strings(services)
	sort.Strings(deployments)
	if len(services) != 2 {
		t.Fatalf("the fixture emitted %d Service(s), want 2: %v", len(services), services)
	}
	// The two attachments' Services are named apart, which is why the build stands.
	if services[0] == services[1] {
		t.Errorf("the two attachments emitted one Service name %q; the fixture no longer covers the negative direction", services[0])
	}
	// A Deployment sharing a Service's name is the second class, and it built.
	if len(deployments) != 1 {
		t.Fatalf("the fixture emitted %d Deployment(s), want 1: %v", len(deployments), deployments)
	}
	shared := false
	for _, s := range services {
		if s == deployments[0] {
			shared = true
		}
	}
	if !shared {
		t.Errorf("no Service shares the Deployment's name %q, so the fixture no longer covers a name held across two kinds", deployments[0])
	}
}

// TestDerivedNames_DeclaringMovesNoEmittedByte is the property that makes a
// declaration safe to add to a render: it tells the host what the output already
// says and changes nothing about what the output is.
//
// Asserted as byte-identity across repeated builds, which is the property the
// declaration could break — the declared payload travels in the same render output
// as the files, so a declaration assembled from a map walk would move the render's
// bytes run to run even though no emitted file changed.
func TestDerivedNames_DeclaringMovesNoEmittedByte(t *testing.T) {
	t.Parallel()

	pipelinetest.AssertDeterministic(t, derivedNameInput("distinct"))
}

// resourceNamesOf collects every metadata.name a rendered body carries, read by
// scanning lines rather than by parsing, because the property under test is what
// the emitted TEXT says.
func resourceNamesOf(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if v, ok := strings.CutPrefix(line, "  name: "); ok {
			if v = strings.TrimSpace(v); v != "" && v != `""` {
				out = append(out, v)
			}
		}
	}
	return out
}
