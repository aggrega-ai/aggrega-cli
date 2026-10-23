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
	"regexp"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// attachmentIdentityInput compiles the three-delivery fixture for one target.
func attachmentIdentityInput(target string) pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath:  "testdata/attachment-identity-label/release-" + target + ".yaml",
		CatalogPaths: []string{"testdata/attachment-identity-label/catalog/base"},
	}
}

// attachmentFilePrefix is the prefix the compiler puts in front of whatever a
// provider named an attachment's file, where the provider's group gives no
// contributor a position of its own. The host and the attachment key, joined and
// closed by the encoding's own two-character separator.
//
// Written out here rather than derived from the composition, so a change to the
// separator arrives as a failure in these cases rather than being tracked
// silently. The fixture's host component is "alpha" throughout.
func attachmentFilePrefix(instance string) string { return "alpha--" + instance + "--" }

// isAttachmentArtifact reports whether an emitted key belongs to one of the
// fixture's three attachments rather than to the host component's own render.
//
// Keyed on the attachment prefixes rather than on the absence of some marker: the
// flat shape leaves an attachment's files in the same directory as the host's, so
// there is no directory left to tell them apart and the prefix is the only thing
// that does.
func isAttachmentArtifact(key string) bool {
	for _, instance := range []string{"src", "wl", "art"} {
		if strings.Contains(key, attachmentFilePrefix(instance)) {
			return true
		}
	}
	return false
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
		if strings.Contains(k, attachmentFilePrefix("src")) {
			sourcedIDs = append(sourcedIDs, ids...)
			continue
		}
		if !isAttachmentArtifact(k) {
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

// TestAttachmentIdentity_EachAttachmentCarriesItsOwnFilenamePrefix pins the other
// half of the separation, for a target that writes discrete documents: three
// attachments on one component reach three distinct filename prefixes, each named
// for the host and for the key the Feature is attached under. The prefix is what
// tells two attachments apart where their emitted filenames cannot — an artifact's
// filename is the author's map key verbatim.
//
// 🔴 The files sit in the host's own directory, which is the whole point: this
// target reads only the files sitting directly in a directory, so an attachment
// under a sub-directory of its own is one a consumer misses at its defaults while
// reporting success. Both halves are asserted — the prefix is present, and no
// output opens a directory level.
//
// The Kubernetes target alone, and that is the separation's own shape rather than
// a gap: a target whose provider folds its dispatch group into ONE document has
// nothing to separate, and the sibling case below is where that target's answer is
// pinned. The identity every attachment's resources carry is asserted on both
// targets above, which is the separation that survives either layout.
func TestAttachmentIdentity_EachAttachmentCarriesItsOwnFilenamePrefix(t *testing.T) {
	t.Parallel()

	res := pipelinetest.Build(t, attachmentIdentityInput("helm"))
	sorted := sortedKeys(res.Artifacts)
	keys := strings.Join(sorted, "\n")
	for _, instance := range []string{"src", "wl", "art"} {
		want := attachmentFilePrefix(instance)
		if !strings.Contains(keys, want) {
			t.Errorf("no output carries the contributor prefix %q\nkeys:\n%s", want, keys)
		}
	}
	// The flatness itself, asserted rather than implied by the prefixes above: a
	// build that emitted both a prefixed name AND kept an enclosing directory
	// would satisfy the reads above while leaving the omission in place.
	for _, k := range sorted {
		if strings.Contains(k, "/features/") {
			t.Errorf("attachment output %q still sits under an enclosing directory, so a consumer reading "+
				"only the files directly in a directory still misses it\nkeys:\n%s", k, keys)
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
		if isAttachmentArtifact(k) {
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
		if strings.Contains(k, attachmentFilePrefix("src")) {
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
	// The name the sourced render itself emitted, which the chart derived from the
	// host's release name and which the contributor prefix sits in front of rather
	// than replacing. Written out rather than composed from anything the render
	// produced, so it cannot agree with a derivation that moved.
	if !strings.HasSuffix(key, "alpha--src--alpha-service-alpha.yaml") {
		t.Errorf("the sourced render's emitted file key must keep the host's name in the name the chart derived; got %q", key)
	}
}

// TestAttachmentIdentity_DetachingTheFeaturesRemovesExactlyTheirContributions is
// the other direction of the case above, and it is the direction a presence read
// structurally cannot supply.
//
// That case finds each attachment's identity inside the assembled document. On
// its own that says nothing about the merge: a document carrying those values
// whatever was attached — because the host's own render emitted them, or because
// the assembler emits a fixed set — satisfies the presence read. So the same
// shape is built a second time with every Feature detached and nothing else
// changed, and the two documents' identity sets are compared.
//
// The comparison is a set difference that NAMES what moved rather than a count,
// and it is asserted both ways. The detached document must lack exactly the three
// attachment identities; it must also still carry the host component's own. That
// second half is what a fixture rendering nothing would fail — a build emitting
// an empty document equally "lacks the attachment identities", and would pass an
// absence read written on its own.
//
// What a planted defect does to it: routing an attachment into the dispatch group
// without regard to whether its Feature is attached leaves its identity in the
// detached document, and the difference then comes up short by that member and
// names it. Dropping the merge instead widens the difference beyond the three,
// because the host's own identity goes missing too.
func TestAttachmentIdentity_DetachingTheFeaturesRemovesExactlyTheirContributions(t *testing.T) {
	t.Parallel()

	attached := pipelinetest.Build(t, attachmentIdentityInput("compose"))
	detached := pipelinetest.Build(t, attachmentIdentityInput("compose-detached"))

	t.Logf("the document assembled with every Feature detached:\n%s", joinArtifactBodies(detached.Artifacts))

	attachedIDs := distinctIdentityValues(attached.Artifacts)
	detachedIDs := distinctIdentityValues(detached.Artifacts)
	t.Logf("attached document identities: %v", attachedIDs)
	t.Logf("detached document identities: %v", detachedIDs)

	inDetached := make(map[string]bool, len(detachedIDs))
	for _, id := range detachedIDs {
		inDetached[id] = true
	}
	var removed []string
	for _, id := range attachedIDs {
		if !inDetached[id] {
			removed = append(removed, id)
		}
	}
	t.Logf("detaching the Features removed: %v", removed)

	want := []string{"cfr--alpha--art", "cfr--alpha--src", "cfr--alpha--wl"}
	if strings.Join(removed, ",") != strings.Join(want, ",") {
		t.Errorf("detaching the Features must remove exactly the three attachment identities\nremoved  %v\nexpected %v\nattached %v\ndetached %v",
			removed, want, attachedIDs, detachedIDs)
	}

	// The host component's own contribution survives the detachment. Without this
	// an empty document would satisfy the difference read above.
	if !inDetached["alpha"] {
		t.Errorf("the host component's own identity must survive detaching the Features; the detached document carries %v", detachedIDs)
	}

	// And no attachment output survives anywhere in the emitted set, merged or
	// beside it — a build that stopped merging the fragment but still wrote it
	// discretely would satisfy the identity difference while shipping the content.
	for _, k := range sortedKeys(detached.Artifacts) {
		if strings.Contains(k, "features/") {
			t.Errorf("the detached build still emits an attachment file at %q\nkeys:\n%s",
				k, strings.Join(sortedKeys(detached.Artifacts), "\n"))
		}
	}
}

// capitalIdentityInput compiles the fixture whose host component and its one
// Feature attachment are named with a capital: component `myApi`, attached under
// `worker`, by a FeatureRecipe declaring a workload with a `default` Service.
func capitalIdentityInput(target string) pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath:  "testdata/attachment-identity-capital/release-" + target + ".yaml",
		CatalogPaths: []string{"testdata/attachment-identity-capital/catalog/base"},
	}
}

// capitalAttachmentIdentity is the identity the `worker` attachment on `myApi`
// composes to: the capital written out as an escape, so the value is a resource
// name as well as a label value. Written out here rather than composed, so a
// composition that moved arrives as a failure rather than as agreement.
const capitalAttachmentIdentity = "cfr--my-zx000041pi--worker"

// rfc1123LabelName and rfc1035LabelName are the grammars the Kubernetes API
// server holds a Deployment and a Service name to, written out rather than taken
// from the compiler so the check is independent of what it checks.
var (
	rfc1123LabelName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	rfc1035LabelName = regexp.MustCompile(`^[a-z]([-a-z0-9]*[a-z0-9])?$`)
)

// emittedObject is the part of a rendered Kubernetes document these cases read.
type emittedObject struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name   string            `yaml:"name"`
		Labels map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	Spec struct {
		Selector yaml.Node `yaml:"selector"`
		Template struct {
			Metadata struct {
				Labels map[string]string `yaml:"labels"`
			} `yaml:"metadata"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

// selectorLabels reads a selector in either of the two shapes a workload's
// documents carry it: a Service's flat map, or a Deployment's matchLabels.
func selectorLabels(t *testing.T, node yaml.Node) map[string]string {
	t.Helper()
	var flat map[string]any
	if err := node.Decode(&flat); err != nil {
		t.Fatalf("decode selector: %v", err)
	}
	out := map[string]string{}
	if nested, ok := flat["matchLabels"].(map[string]any); ok {
		flat = nested
	}
	for k, v := range flat {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// TestAttachmentIdentity_ACapitalInAnAuthoredNameReachesNoResourceName builds a
// workload a Feature attachment synthesizes, under a host component named with a
// capital, through the real helm provider, and reads every name it emitted.
//
// The cluster holds a Deployment's name to an RFC 1123 label and a Service's to an
// RFC 1035 one, and neither admits a capital. The workload is named after the
// attachment's composed identity, so the capital the author wrote has to be
// written out there — and the labels and selectors, which carry the same value,
// have to agree with the names, or the Service would select nothing. Each is read
// from the emitted bytes. The capital spelling is also asserted absent from the
// whole output, since a single surface still carrying it is a name a cluster
// refuses while the build reports success.
func TestAttachmentIdentity_ACapitalInAnAuthoredNameReachesNoResourceName(t *testing.T) {
	t.Parallel()

	res := pipelinetest.Build(t, capitalIdentityInput("helm"))
	bodies := joinArtifactBodies(res.Artifacts)
	if strings.Contains(bodies, "cfr--myApi") {
		t.Errorf("the emitted output still spells the attachment's identity with its capital\n%s", bodies)
	}

	var controllers, services int
	for _, k := range sortedKeys(res.Artifacts) {
		dec := yaml.NewDecoder(strings.NewReader(string(res.Artifacts[k])))
		for {
			var obj emittedObject
			if err := dec.Decode(&obj); err != nil {
				break
			}
			if obj.Metadata.Labels["aggrega.ai/component-id"] != capitalAttachmentIdentity {
				continue
			}
			for _, key := range []string{"aggrega.ai/component-id", "app.kubernetes.io/name", "app"} {
				if got := obj.Metadata.Labels[key]; got != capitalAttachmentIdentity {
					t.Errorf("%s %q in %s: label %s = %q, want %q", obj.Kind, obj.Metadata.Name, k, key, got, capitalAttachmentIdentity)
				}
			}
			if sel := selectorLabels(t, obj.Spec.Selector); sel["app"] != capitalAttachmentIdentity {
				t.Errorf("%s %q in %s selects %v, want app=%q", obj.Kind, obj.Metadata.Name, k, sel, capitalAttachmentIdentity)
			}
			switch obj.Kind {
			case "Deployment":
				controllers++
				if obj.Metadata.Name != capitalAttachmentIdentity || !rfc1123LabelName.MatchString(obj.Metadata.Name) {
					t.Errorf("the attachment's Deployment is named %q, want %q as a name the cluster accepts",
						obj.Metadata.Name, capitalAttachmentIdentity)
				}
				if got := obj.Spec.Template.Metadata.Labels["app"]; got != capitalAttachmentIdentity {
					t.Errorf("the attachment's pod template carries app=%q, want %q", got, capitalAttachmentIdentity)
				}
			case "Service":
				services++
				want := capitalAttachmentIdentity + "-default"
				if obj.Metadata.Name != want || !rfc1035LabelName.MatchString(obj.Metadata.Name) {
					t.Errorf("the attachment's Service is named %q, want %q as a name the cluster accepts", obj.Metadata.Name, want)
				}
			default:
				t.Errorf("the attachment emitted an unexpected %s %q in %s", obj.Kind, obj.Metadata.Name, k)
			}
		}
	}
	if controllers != 1 || services != 1 {
		t.Fatalf("the attachment emitted %d Deployment objects and %d Services, want one of each\nkeys:\n%s",
			controllers, services, strings.Join(sortedKeys(res.Artifacts), "\n"))
	}
}

// TestAttachmentIdentity_ACapitalInAnAuthoredNameReachesNoComposeServiceName is
// the same build for the compose target, which names the attachment's service
// after the same composed identity: the service key and its identity label carry
// the escaped value, and the capital spelling appears nowhere in the document.
func TestAttachmentIdentity_ACapitalInAnAuthoredNameReachesNoComposeServiceName(t *testing.T) {
	t.Parallel()

	res := pipelinetest.Build(t, capitalIdentityInput("compose"))
	bodies := joinArtifactBodies(res.Artifacts)
	if strings.Contains(bodies, "cfr--myApi") {
		t.Errorf("the emitted output still spells the attachment's identity with its capital\n%s", bodies)
	}

	found := false
	for _, k := range sortedKeys(res.Artifacts) {
		var doc struct {
			Services map[string]struct {
				Labels map[string]string `yaml:"labels"`
			} `yaml:"services"`
		}
		if err := yaml.Unmarshal(res.Artifacts[k], &doc); err != nil {
			continue
		}
		for name, svc := range doc.Services {
			id := svc.Labels["aggrega.ai/component-id"]
			if !strings.HasPrefix(id, "cfr--") {
				continue
			}
			if name != capitalAttachmentIdentity || id != capitalAttachmentIdentity {
				t.Errorf("the attachment's compose service is %q carrying component-id %q, want both %q",
					name, id, capitalAttachmentIdentity)
			}
			if !rfc1123LabelName.MatchString(name) {
				t.Errorf("the attachment's compose service %q is not an RFC 1123 label", name)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("no compose document carries a service named %q\n%s", capitalAttachmentIdentity, bodies)
	}
}

// distinctIdentityValues collects every aggrega.ai/component-id value the build
// emitted, deduplicated and ordered, so two builds' identity sets can be compared
// as sets. Read from the emitted text through identityLabelValues, for the reason
// stated there.
func distinctIdentityValues(artifacts map[string][]byte) []string {
	seen := map[string]bool{}
	for _, k := range sortedKeys(artifacts) {
		for _, v := range identityLabelValues(string(artifacts[k])) {
			seen[v] = true
		}
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
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
