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

// artifactsNameInput compiles the artifacts-path fixture for one of its two
// releases. It is a second pair of releases over the SAME catalog as the sourced
// pair above, which is the point: one component, one recipe and one provider
// Plugin serve both, so the two paths are compared over one corpus rather than
// over two that could drift apart.
func artifactsNameInput(variant string) pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath:  "testdata/derived-name-collision/release-artifacts-" + variant + ".yaml",
		CatalogPaths: []string{"testdata/derived-name-collision/catalog/base"},
	}
}

// TestArtifactsNames_OneAttachmentOnTwoHostsNamingOneResourceHalts is the
// artifacts path's form of the case above, and the shape is the one that reaches
// it in ordinary authoring: ONE artifacts-delivering FeatureRecipe, attached under
// ONE instance key, to TWO components in one boundary.
//
// An artifact's output filename is the author's map key verbatim, so the emitted
// paths differ only by the host directory and the assembled-path check cannot see
// a conflict — the paths genuinely differ. What collides is the name INSIDE the
// two bodies, which is one authored literal reaching two hosts.
//
// The names in an artifact's body are authored rather than derived by the render,
// and that distinction deliberately does not change the answer: two resources
// reaching one name in one namespace is a cluster overwrite whichever surface the
// name came from, so the emitted document is compared the same way a chart's
// rendered document is. The halt must name BOTH hosts, because renaming either is
// the author's remedy and naming one would assert a choice the compiler has not
// made.
func TestArtifactsNames_OneAttachmentOnTwoHostsNamingOneResourceHalts(t *testing.T) {
	t.Parallel()

	_, err := pipelinetest.BuildE(t, artifactsNameInput("collide"))
	if err == nil {
		t.Fatal("one artifacts attachment on two hosts emitting one resource name built cleanly; want a halt naming both")
	}
	if !errors.Is(err, renderer.ErrDerivedKeyCollision()) {
		t.Fatalf("the build failed for a different reason than the name collision: %v", err)
	}

	// Read through the wrapped set for the reason the sourced case does: the
	// report's own text names only what the findings have in common.
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
		t.Fatalf("expected one finding per contributing host, got %d: %v", len(findings), findings)
	}
	texts := make([]string, 0, len(findings))
	for _, f := range findings {
		texts = append(texts, f.Error())
	}
	whole := strings.Join(texts, "\n")

	for _, want := range []string{
		// The colliding name, byte-identical to the literal in the artifact body,
		// so it doubles as the term to search that body for.
		`"aux-policy"`,
		// The same key space the sourced path declares into — the fixed prefix,
		// then the kind and the namespace that bound a Kubernetes resource name.
		`key space "resource-names:NetworkPolicy--default"`,
		// Both hosts, under one attachment key, in the author's own words.
		`derived by component "alpha", carrying attachment "aux"`,
		`derived by component "beta", carrying attachment "aux"`,
	} {
		if !strings.Contains(whole, want) {
			t.Errorf("the halt does not carry %q, so the author cannot act on it\ngot:\n%s", want, whole)
		}
	}
	t.Logf("the artifacts collision halts with %d finding(s):\n%s", len(texts), whole)
}

// TestArtifactsNames_ComposingTheHostBuildsWithDistinctNames is the half that
// makes the halt above answerable, and it is the whole justification for adding a
// refusal: the author has a remedy, demonstrated rather than asserted.
//
// The FeatureRecipe is the same shape as the colliding one and differs only in
// that its body composes the host component and the attachment key into the
// resource name — the two fields the template context contract states an
// `artifacts:` body receives. One authored body then reaches two hosts as two
// distinct names, and the build stands.
//
// Without this case the increment would have added a refusal with no demonstrated
// way out, which is what makes a halt indefensible rather than loud.
func TestArtifactsNames_ComposingTheHostBuildsWithDistinctNames(t *testing.T) {
	t.Parallel()

	res := pipelinetest.Build(t, artifactsNameInput("distinct"))

	var policies []string
	for _, body := range res.Artifacts {
		if !strings.Contains(string(body), "kind: NetworkPolicy") {
			continue
		}
		policies = append(policies, resourceNamesOf(string(body))...)
	}
	sort.Strings(policies)
	if len(policies) != 2 {
		t.Fatalf("the fixture emitted %d NetworkPolicy name(s), want 2: %v", len(policies), policies)
	}
	// Two distinct names from ONE authored body is the remedy working. Equality
	// here would mean the composition never reached the body, and the case would
	// then prove nothing about the remedy while still passing on the count.
	if policies[0] == policies[1] {
		t.Fatalf("both hosts emitted the NetworkPolicy name %q; the composition did not reach the artifact body, so this case no longer demonstrates the remedy", policies[0])
	}
	// Each name carries its own host, which is what makes the two distinct rather
	// than merely different — a body that composed some other varying value would
	// satisfy the inequality above without giving the author a name they can
	// predict.
	for _, host := range []string{"alpha", "beta"} {
		found := false
		for _, p := range policies {
			if strings.HasPrefix(p, host+"-") {
				found = true
			}
		}
		if !found {
			t.Errorf("no emitted NetworkPolicy name opens with the host %q; got %v", host, policies)
		}
	}
	t.Logf("composing the host emits two distinct names: %v", policies)
}

// TestArtifactsNames_DeclaringMovesNoEmittedByte is the artifacts path's form of
// the property beside it: the declaration reports what the output already says and
// changes nothing about what the output is.
func TestArtifactsNames_DeclaringMovesNoEmittedByte(t *testing.T) {
	t.Parallel()

	pipelinetest.AssertDeterministic(t, artifactsNameInput("distinct"))
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
