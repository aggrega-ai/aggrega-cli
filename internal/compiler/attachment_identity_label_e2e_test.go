// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end coverage of the identity a per-component Feature attachment's
// rendered resources carry, across the three ways a FeatureRecipe can deliver
// them: a chart or fragment the author supplies, a workload the compiler
// synthesises, and a standalone artifact set.
//
// The fixture attaches all three to ONE component in one boundary, so the three
// deliveries meet in one build and can be compared against each other rather
// than each against its own expectation. It is built for both a Kubernetes
// target and a compose target, because the identity is derived once host-side
// and each target writes it onto a different surface — Kubernetes labels on one,
// service labels on the other.

package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// attachmentIdentityInput compiles the three-delivery fixture for one target.
func attachmentIdentityInput(target string) pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath:  "testdata/attachment-identity-label/release-" + target + ".yaml",
		CatalogPaths: []string{"testdata/attachment-identity-label/catalog/base"},
	}
}

// TestAttachmentIdentity_SynthesisedAndArtifactsCarryTheAttachmentIdentity pins
// the value a per-component attachment's resources are stamped with: it is
// composed from the component the Feature is attached to and the key it is
// attached under, so it is distinct from the host component's own identity and
// distinct between two attachments on one host.
//
// Asserted on both targets, because the composition happens once for the whole
// render and each target writes the result onto its own label surface — a value
// reaching one surface and not the other would otherwise pass unnoticed.
func TestAttachmentIdentity_SynthesisedAndArtifactsCarryTheAttachmentIdentity(t *testing.T) {
	t.Parallel()

	for _, target := range []string{"helm", "compose"} {
		res := pipelinetest.Build(t, attachmentIdentityInput(target))
		bodies := joinArtifactBodies(res.Artifacts)

		// The host component's own resources carry the identity its author wrote,
		// unmodified — that is what makes the name in the Aggregate the name to
		// look for in the cluster.
		if !strings.Contains(bodies, "aggrega.ai/component-id: alpha\n") {
			t.Errorf("%s: the host component's own resources must carry its authored identity\n%s", target, bodies)
		}
		// Each attachment carries an identity of its own, composed from the host
		// and the key it is attached under.
		for _, want := range []string{
			"aggrega.ai/component-id: cfr--alpha--wl\n",
			"aggrega.ai/component-id: cfr--alpha--art\n",
		} {
			if !strings.Contains(bodies, want) {
				t.Errorf("%s: emitted output is missing %q\n%s", target, want, bodies)
			}
		}
	}
}

// TestAttachmentIdentity_EachAttachmentWritesUnderItsOwnDirectory pins the other
// half of the separation: two attachments on one component write to two places,
// named for the host and for the key the Feature is attached under. The
// directory is what tells two attachments apart where their emitted filenames
// cannot — an artifact's filename is the author's map key verbatim.
func TestAttachmentIdentity_EachAttachmentWritesUnderItsOwnDirectory(t *testing.T) {
	t.Parallel()

	for _, target := range []string{"helm", "compose"} {
		res := pipelinetest.Build(t, attachmentIdentityInput(target))
		keys := strings.Join(sortedKeys(res.Artifacts), "\n")
		for _, instance := range []string{"src", "wl", "art"} {
			want := "features/alpha/" + instance + "/"
			if !strings.Contains(keys, want) {
				t.Errorf("%s: no output is placed under %q\nkeys:\n%s", target, want, keys)
			}
		}
	}
}

// TestAttachmentIdentity_AnAuthorsChartKeepsTheHostReleaseName pins the
// asymmetry that makes an author-supplied chart safe to attach.
//
// A chart the author wrote renders its own templates against the release name,
// and charts routinely name their resources after it. So the release name an
// attachment's chart renders against stays the HOST component's — changing it
// would rename resources the chart's author declared, in a cluster where they
// are already deployed, for a reason that has nothing to do with the chart. The
// identity the resources are LABELLED with is a separate question with a
// separate answer; this case is about the name inside the rendered bytes.
//
// The fixture's chart writes `metadata.name: {{ .Release.Name }}` precisely so
// this is observable in the emitted output rather than only in the host.
func TestAttachmentIdentity_AnAuthorsChartKeepsTheHostReleaseName(t *testing.T) {
	t.Parallel()

	res := pipelinetest.Build(t, attachmentIdentityInput("helm"))
	var body string
	for _, k := range sortedKeys(res.Artifacts) {
		if strings.Contains(k, "features/alpha/src/") {
			body = string(res.Artifacts[k])
			break
		}
	}
	if body == "" {
		t.Fatalf("the attachment's chart rendered nothing; keys:\n%s", strings.Join(sortedKeys(res.Artifacts), "\n"))
	}
	if !strings.Contains(body, "name: alpha\n") {
		t.Errorf("the chart's own resource must keep the host's release name; got:\n%s", body)
	}
}

// joinArtifactBodies concatenates every rendered body so a case can assert on
// what the build emitted as a whole. Sorted by path so a failure prints the same
// text run to run.
func joinArtifactBodies(artifacts map[string][]byte) string {
	var b strings.Builder
	for _, k := range sortedKeys(artifacts) {
		b.WriteString("===== " + k + " =====\n")
		b.Write(artifacts[k])
		b.WriteString("\n")
	}
	return b.String()
}
