// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end coverage of the identity a per-component Feature attachment's
// rendered resources carry, across the three ways a FeatureRecipe can deliver
// them: a chart or fragment the author supplies, a workload the compiler
// synthesizes, and a standalone artifact set.
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

// TestAttachmentIdentity_EveryDeliveryCarriesTheAttachmentIdentity pins the
// value a per-component attachment's resources are stamped with: it is composed
// from the component the Feature is attached to and the key it is attached
// under, so it is distinct from the host component's own identity and distinct
// between two attachments on one host.
//
// It covers all THREE deliveries, and the sourced one is the case worth naming.
// An attachment delivering a chart or a fragment its author supplied stamped the
// HOST component's identity, which made its resources indistinguishable by label
// from the resources the host emitted itself: anything selecting an attachment's
// resources by identity found the synthesized and artifacts ones and silently
// missed the sourced ones. All three now answer the same way.
//
// Asserted on both targets, because the composition happens once for the whole
// render and each target writes the result onto its own label surface — a value
// reaching one surface and not the other would otherwise pass unnoticed.
func TestAttachmentIdentity_EveryDeliveryCarriesTheAttachmentIdentity(t *testing.T) {
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
		// and the key it is attached under — for every delivery.
		for _, want := range []string{
			"aggrega.ai/component-id: cfr--alpha--src\n",
			"aggrega.ai/component-id: cfr--alpha--wl\n",
			"aggrega.ai/component-id: cfr--alpha--art\n",
		} {
			if !strings.Contains(bodies, want) {
				t.Errorf("%s: emitted output is missing %q\n%s", target, want, bodies)
			}
		}
	}
}

// TestAttachmentIdentity_TheSourcedAttachmentIsSelectableApartFromItsHost is the
// consequence the case above exists for, asserted as the property rather than as
// a value: the documents the sourced attachment emitted and the documents the
// host component emitted carry DIFFERENT identity values, so a selector naming
// one of them does not return the other.
//
// It is a separate case because the presence assertions above would still pass if
// the host's own resources somehow acquired the attachment's identity too — the
// failure mode that unifies the two in the wrong direction. This one compares the
// two sides, so it fails whichever way they meet.
func TestAttachmentIdentity_TheSourcedAttachmentIsSelectableApartFromItsHost(t *testing.T) {
	t.Parallel()

	res := pipelinetest.Build(t, attachmentIdentityInput("helm"))
	var hostIDs, sourcedIDs []string
	for _, k := range sortedKeys(res.Artifacts) {
		ids := identityLabelValues(string(res.Artifacts[k]))
		if strings.Contains(k, "features/alpha/src/") {
			sourcedIDs = append(sourcedIDs, ids...)
			continue
		}
		if !strings.Contains(k, "features/") {
			hostIDs = append(hostIDs, ids...)
		}
	}
	if len(hostIDs) == 0 || len(sourcedIDs) == 0 {
		t.Fatalf("the fixture did not emit both a host document and a sourced-attachment document; host=%v sourced=%v\nkeys:\n%s",
			hostIDs, sourcedIDs, strings.Join(sortedKeys(res.Artifacts), "\n"))
	}
	for _, h := range hostIDs {
		for _, s := range sourcedIDs {
			if h == s {
				t.Errorf("the host and its sourced attachment share the identity %q, so a selector cannot tell them apart", h)
			}
		}
	}
}

// identityLabelValues collects every aggrega.ai/component-id value a rendered
// document carries. Read by scanning lines rather than by parsing YAML, because
// the property under test is what the emitted TEXT says and a parse would let a
// value reach the assertion through a path the bytes do not show.
func identityLabelValues(body string) []string {
	const key = "aggrega.ai/component-id:"
	var out []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(trimmed, key); ok {
			out = append(out, strings.TrimSpace(v))
		}
	}
	return out
}

// TestAttachmentIdentity_EachAttachmentWritesUnderItsOwnDirectory pins the other
// half of the separation, for a target that writes discrete documents: two
// attachments on one component write to two places, named for the host and for
// the key the Feature is attached under. The directory is what tells two
// attachments apart where their emitted filenames cannot — an artifact's filename
// is the author's map key verbatim.
//
// The Kubernetes target alone, and that is the separation's own shape rather than
// a gap: a target whose provider folds its dispatch group into ONE document has no
// per-producer directory for anything to be separated by, and the sibling case
// below is where that target's answer is pinned. The identity every attachment's
// resources carry is asserted on both targets above, which is the separation that
// survives either layout.
func TestAttachmentIdentity_EachAttachmentWritesUnderItsOwnDirectory(t *testing.T) {
	t.Parallel()

	res := pipelinetest.Build(t, attachmentIdentityInput("helm"))
	keys := strings.Join(sortedKeys(res.Artifacts), "\n")
	for _, instance := range []string{"src", "wl", "art"} {
		want := "features/alpha/" + instance + "/"
		if !strings.Contains(keys, want) {
			t.Errorf("no output is placed under %q\nkeys:\n%s", want, keys)
		}
	}
}

// TestAttachmentIdentity_AMergingTargetCarriesItsAttachmentsInTheAssembledDocument
// is the same question asked of a target whose provider folds its dispatch group
// into one document: every attachment's output is INSIDE that document, and there
// is no discrete file beside it.
//
// Both halves are asserted, and the absent half is the one that matters. A build
// that folded each attachment's fragment into the assembled document AND left the
// attachment's own file beside it would satisfy the presence read on its own,
// while delivering every one of those resources twice — once merged and once
// standalone. The standalone file is also not a deployable document by itself: it
// carries a fragment of the target's format rather than a whole one, which is why
// the answer here is that it is gone rather than that it is correct.
func TestAttachmentIdentity_AMergingTargetCarriesItsAttachmentsInTheAssembledDocument(t *testing.T) {
	t.Parallel()

	res := pipelinetest.Build(t, attachmentIdentityInput("compose"))
	keys := sortedKeys(res.Artifacts)
	for _, k := range keys {
		if strings.Contains(k, "features/") {
			t.Errorf("a discrete attachment file survives at %q; its fragment was merged, so writing it too delivers the same content twice\nkeys:\n%s",
				k, strings.Join(keys, "\n"))
		}
	}
	bodies := joinArtifactBodies(res.Artifacts)
	for _, instance := range []string{"src", "wl", "art"} {
		want := "aggrega.ai/component-id: cfr--alpha--" + instance + "\n"
		if !strings.Contains(bodies, want) {
			t.Errorf("the assembled document does not carry attachment %q; it must be merged in, not dropped\n%s", instance, bodies)
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
// separate answer; this case is about the two names that belong to the chart.
//
// Both of them are asserted, because both derive from the same basis and a fix
// that moved one of them would be a fix that moved the other. The release name
// reaches the rendered bytes — the fixture's chart writes
// `metadata.name: {{ .Release.Name }}` precisely so it is observable there — and
// the emitted file KEY is prefixed with the same basis, which is observable in
// the output path. Asserting only the first would leave the prefix free to move
// silently, since nothing inside a file records the name of the file.
func TestAttachmentIdentity_AnAuthorsChartKeepsTheHostReleaseName(t *testing.T) {
	t.Parallel()

	res := pipelinetest.Build(t, attachmentIdentityInput("helm"))
	var key, body string
	for _, k := range sortedKeys(res.Artifacts) {
		if strings.Contains(k, "features/alpha/src/") {
			key, body = k, string(res.Artifacts[k])
			break
		}
	}
	if body == "" {
		t.Fatalf("the attachment's chart rendered nothing; keys:\n%s", strings.Join(sortedKeys(res.Artifacts), "\n"))
	}
	if !strings.Contains(body, "name: alpha\n") {
		t.Errorf("the chart's own resource must keep the host's release name; got:\n%s", body)
	}
	// The file key the sourced render emitted, prefixed with the same basis. The
	// expected name is written out rather than composed from anything the render
	// produced, so it cannot agree with a derivation that moved.
	if !strings.HasSuffix(key, "/alpha-service-alpha.yaml") {
		t.Errorf("the sourced render's emitted file key must stay prefixed with the host's name; got %q", key)
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
