// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs in this file drive the whole render, through the embedded helm guest,
// over a host component carrying one Feature attachment, and read what the
// attachment's templates rendered for .context.rendered_name.
//
// What they establish is that an attachment reads its HOST's name — the one the
// host's own provider derived, and the host's own Recipe reads — on every surface
// an attachment author names a resource from: an inline template, a local, and the
// provides the attachment publishes to its host. The host identities are chosen so
// the identity as written and the name a cluster accepts differ in each of the ways
// they can: an underscore, a capital, and the join a composed Aggregate writes into
// an identity. A valid identity is the control, where the two coincide.
//
// The expected names are written out by hand rather than computed, so a change to
// the derivation arrives as a failure here instead of being tracked silently.

const (
	// hostNameProbeComponent is the component type every host in this file has.
	hostNameProbeComponent = "core/compute/host-name-host"
	// hostNameProbeFeature is the Feature type the probe attachment carries.
	hostNameProbeFeature = "core/host-name-probe"
	// hostNameProbeInstance is the instance name the probe is attached under.
	hostNameProbeInstance = "probe"
)

// hostNameProbeKind names how the host's own Recipe renders, which decides
// whether its provider reports a name at all.
type hostNameProbeKind string

const (
	// hostSynthesized declares a workload the helm provider synthesizes.
	hostSynthesized hostNameProbeKind = "synthesized"
	// hostSourced declares a chart the author supplied.
	hostSourced hostNameProbeKind = "sourced"
	// hostArtifactsOnly declares a placed artifact set and nothing else, so the
	// helm provider reports no name for it.
	hostArtifactsOnly hostNameProbeKind = "artifacts-only"
	// hostOtherProvider renders through a provider that reports no name at all.
	hostOtherProvider hostNameProbeKind = "other-provider"
)

// hostNameProbeChart writes a chart whose one template names a ConfigMap after the
// release name, so the name the chart renders under is visible in its output.
func hostNameProbeChart(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"),
		[]byte("apiVersion: v2\nname: host\nversion: 1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "templates"), 0o755); err != nil {
		t.Fatalf("mkdir templates: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "templates", "configmap.yaml"),
		[]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}-chart\ndata:\n  marker: \"x\"\n"),
		0o644); err != nil {
		t.Fatalf("write configmap.yaml: %v", err)
	}
	return dir
}

// hostNameProbeRecipe is the host's own Recipe for one kind. A host whose provider
// reports a name reads it in its own artifact, which is the value the attachment's
// reads are compared against; a host reporting none reads its identity instead,
// because reading the name there is refused on the host's own surfaces too.
func hostNameProbeRecipe(t *testing.T, kind hostNameProbeKind) *types.Recipe {
	t.Helper()
	ownText := `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .context.rendered_name }}-own
data:
  own: "{{ .context.rendered_name }}"
  seen: "{{ .features.probe.provides.host_name }}"
`
	if kind == hostArtifactsOnly || kind == hostOtherProvider {
		ownText = `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .context.instance_id }}-own
`
	}
	artifacts := types.OrderedMap[types.ArtifactEntry]{}
	artifacts.Set("own.yaml", types.ArtifactEntry{Inline: ownText})

	spec := types.RecipeSpec{Capability: "helm", Implements: hostNameProbeComponent, Artifacts: artifacts}
	switch kind {
	case hostSynthesized:
		spec.Workload = types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: myapp:latest
    ports:
      web: 8080
`}
	case hostSourced:
		spec.Source = hostNameProbeChart(t)
		spec.ValuesMapping = types.ValuesMapping{Inline: `{}`}
	case hostOtherProvider:
		other := types.OrderedMap[types.ArtifactEntry]{}
		other.Set("own.yaml", types.ArtifactEntry{Inline: "services: {}\n"})
		spec = types.RecipeSpec{Capability: "compose", Implements: hostNameProbeComponent, Artifacts: other}
	}
	return &types.Recipe{Metadata: types.RecipeMetadata{Name: "host-recipe", Version: "1.0.0"}, Spec: spec}
}

// hostNameProbeFR is the attachment's FeatureRecipe: one local and one provides
// entry, each holding the template given, and one artifact rendering the name
// inline, through the local and through the provides.
func hostNameProbeFR(localText, providesText, artifactName string) *types.FeatureRecipe {
	locals := types.OrderedMap[string]{}
	locals.Set("host_name", localText)
	provides := types.OrderedMap[string]{}
	provides.Set("host_name", providesText)
	artifacts := types.OrderedMap[types.ArtifactEntry]{}
	artifacts.Set("probe.yaml", types.ArtifactEntry{Inline: `apiVersion: v1
kind: ConfigMap
metadata:
  name: ` + artifactName + `-probe
data:
  inline: "` + artifactName + `"
  local: "{{ .locals.host_name }}"
  published: "{{ .provides.host_name }}"
`})
	return &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "host-name-probe", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Capability: "helm",
			Implements: hostNameProbeFeature,
			Locals:     locals,
			Provides:   provides,
			Artifacts:  artifacts,
		},
	}
}

// renderHostNameProbe renders one host, of the kind given and under the identity
// given, carrying the probe attachment.
func renderHostNameProbe(
	t *testing.T, hostID string, kind hostNameProbeKind, fr *types.FeatureRecipe,
) (*renderer.RenderResult, error) {
	t.Helper()

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(hostID, types.IRNode{Component: hostNameProbeComponent, ResolvedVersion: "1.0.0", BoundaryPath: "local/default"})
	attached := types.OrderedMap[types.IRFeatureEntry]{}
	attached.Set(hostNameProbeInstance, types.IRFeatureEntry{Type: hostNameProbeFeature})
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set(hostID, attached)

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "host-name"},
			Spec: types.IRSpec{
				Nodes:                nodes,
				ValuesMapping:        types.OrderedMap[json.RawMessage]{},
				InfraMapping:         types.OrderedMap[json.RawMessage]{},
				FeatureValuesMapping: fvm,
				Provenance:           types.IRProvenance{CompilerVersion: "0.1.0", AggregateVersion: "1.0.0"},
			},
		},
		FeatureOrder: ir.FeatureOrder{hostID: []string{hostNameProbeInstance}},
		ResolveResult: &resolver.ResolveResult{
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "host-name"}},
			Release: &types.Release{
				Metadata: types.ReleaseMetadata{Name: "host-name-release", Version: "1.0.0", Environment: "local"},
			},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
	}
	hostRecipe := hostNameProbeRecipe(t, kind)
	lookup := renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType != hostNameProbeComponent {
				return nil, false
			}
			return hostRecipe, true
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			if featureType != hostNameProbeFeature {
				return nil, false
			}
			return fr, true
		},
	}
	return renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
}

// renderedField returns the value one data key carries in a rendered document,
// quoted or not, and whether the key is there.
func renderedField(doc, key string) (string, bool) {
	m := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `:\s*"?([^"\s]*)"?\s*$`).FindStringSubmatch(doc)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// renderedFileEndingIn returns the content of the one emitted file whose key ends
// in suffix.
func renderedFileEndingIn(t *testing.T, result *renderer.RenderResult, suffix string) string {
	t.Helper()
	var found []string
	var content string
	for key, file := range result.Files {
		if strings.HasSuffix(key, suffix) {
			found = append(found, key)
			content = string(file.Content)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one emitted file ending in %q, found %v among %v", suffix, found, fileKeys(result.Files))
	}
	return content
}

// kubernetesNamePattern is the shape a Kubernetes object name of the label form
// has to take: lowercase letters, digits and hyphens, beginning and ending with a
// letter or a digit.
var kubernetesNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// TestRenderAll_AnAttachmentReadsTheValidNameOfItsHost is the invocation proof for
// the host's name on an attachment: for each host identity, on a synthesized host
// and on a sourced host alike, every surface of the attachment reads the name the
// host's own Recipe reads, that name is the one written out below, and it is one a
// cluster accepts.
//
// The equality with the host's own read is what single-sources the value: a second
// derivation on the attachment's side would agree on the valid identity and part
// on the others. The hand-written name is what pins the derivation itself, and the
// shape check is what the whole exercise is for.
func TestRenderAll_AnAttachmentReadsTheValidNameOfItsHost(t *testing.T) {
	t.Parallel()

	for _, host := range []struct {
		id   string
		want string
	}{
		// The control: an identity a cluster already accepts reads as itself.
		{id: "kv", want: "kv"},
		// An underscore, which a Kubernetes name refuses.
		{id: "my_kv", want: "my-zukv"},
		// A capital, which a Kubernetes name refuses too.
		{id: "myKv", want: "my-zx00004bv"},
		// The join a composed Aggregate writes between its name for an inner
		// Aggregate and that Aggregate's name for the component.
		{id: "inner.kv", want: "inner-zdkv"},
	} {
		for _, kind := range []hostNameProbeKind{hostSynthesized, hostSourced} {
			t.Run(string(kind)+" host "+host.id, func(t *testing.T) {
				t.Parallel()
				fr := hostNameProbeFR(
					"{{ .context.rendered_name }}",
					"{{ .context.rendered_name }}",
					"{{ .context.rendered_name }}",
				)
				result, err := renderHostNameProbe(t, host.id, kind, fr)
				if err != nil {
					t.Fatalf("an attachment reading its host's name on a %s host must render: %v", kind, err)
				}

				own := renderedFileEndingIn(t, result, "own.yaml")
				probe := renderedFileEndingIn(t, result, "probe.yaml")
				t.Logf("host's own artifact:\n%s", own)
				t.Logf("attachment's artifact:\n%s", probe)

				hostReads, ok := renderedField(own, "own")
				if !ok || hostReads == "" {
					t.Fatalf("the host's own Recipe read no name; the comparison below would certify nothing:\n%s", own)
				}
				if hostReads != host.want {
					t.Errorf("the host's own Recipe reads %q, want %q", hostReads, host.want)
				}
				if !kubernetesNamePattern.MatchString(hostReads) {
					t.Errorf("the host's name %q is not one a Kubernetes name admits", hostReads)
				}
				for _, surface := range []string{"inline", "local", "published"} {
					got, ok := renderedField(probe, surface)
					if !ok {
						t.Errorf("the attachment's artifact carries no %s value:\n%s", surface, probe)
						continue
					}
					if got != hostReads {
						t.Errorf("the attachment reads %q through its %s surface, and its host's own Recipe reads %q",
							got, surface, hostReads)
					}
				}
				// What the attachment publishes is what its host reads of it.
				if seen, _ := renderedField(own, "seen"); seen != hostReads {
					t.Errorf("the host reads %q from the attachment's published name, want %q", seen, hostReads)
				}
				// A sourced host's chart renders under the same name. The chart's
				// output is found by what it carries rather than by its file name,
				// which the provider derives from the resource the chart declares.
				if kind == hostSourced {
					var chart []string
					for _, file := range result.Files {
						if strings.Contains(string(file.Content), "marker:") {
							chart = append(chart, string(file.Content))
						}
					}
					if len(chart) != 1 {
						t.Fatalf("want the chart's one document among the output, found %d; keys: %v",
							len(chart), fileKeys(result.Files))
					}
					if !strings.Contains(chart[0], "name: "+hostReads+"-chart") {
						t.Errorf("the host's chart did not render under %q:\n%s", hostReads, chart[0])
					}
				}
			})
		}
	}
}

// TestRenderAll_AnAttachmentOnAHostReportingNoNameIsRefusedItsName pins the
// refusal's scope from the other side: where the host's provider reports no name —
// a host rendering a placed artifact set, a host on a provider deriving no such name
// — an attachment's local or provides reading the key halts with the
// reference-not-admitted condition rather than rendering it away, one fault per row.
//
// The control is the artifacts-only host with the attachment reading its identity
// instead, which renders: the refusal is about the one key, not about the host or
// the attachment. It is drawn on that host alone, because the refusal rows halt
// before anything renders while the control renders to the end, and an attachment
// rendering through a different provider from its host would put a second subject
// into the one row meant to show the key is the only fault.
func TestRenderAll_AnAttachmentOnAHostReportingNoNameIsRefusedItsName(t *testing.T) {
	t.Parallel()

	const identity = "{{ .context.instance_id }}"
	for _, kind := range []hostNameProbeKind{hostArtifactsOnly, hostOtherProvider} {
		for _, tc := range []struct {
			name      string
			local     string
			provides  string
			wantError bool
		}{
			{name: "a local reading the name", local: "{{ .context.rendered_name }}", provides: identity, wantError: true},
			{name: "a provides entry reading the name", local: identity, provides: "{{ .context.rendered_name }}", wantError: true},
			{name: "control: both reading the identity", local: identity, provides: identity},
		} {
			if !tc.wantError && kind != hostArtifactsOnly {
				continue
			}
			t.Run(string(kind)+" host: "+tc.name, func(t *testing.T) {
				t.Parallel()
				fr := hostNameProbeFR(tc.local, tc.provides, identity)
				_, err := renderHostNameProbe(t, "my_kv", kind, fr)
				if !tc.wantError {
					if err != nil {
						t.Fatalf("the control must render: %v", err)
					}
					return
				}
				if err == nil {
					t.Fatalf("%s on a %s host rendered; the key carries no value there and must halt", tc.name, kind)
				}
				if !errors.Is(err, renderer.ErrTemplateUnknownRoot()) {
					t.Fatalf("the refusal does not carry %v: %v", renderer.ErrTemplateUnknownRoot(), err)
				}
				// The refusal is read the way it is emitted: every frame of the chain,
				// since a single frame's text is only the outermost locator, and the
				// short code a reader quotes is the first one the chain reports.
				wantShort, hasShort := cerrors.ShortCodeOf(renderer.ErrTemplateUnknownRoot())
				sentence, hasSentence := cerrors.MessageOf(renderer.ErrTemplateUnknownRoot())
				if !hasShort || !hasSentence {
					t.Fatalf("the condition reports no short code or no sentence; the checks below would certify nothing")
				}
				if gotShort, ok := cerrors.ShortCodeOf(err); !ok || gotShort != wantShort {
					t.Errorf("the refusal reports short code %q (reported: %v), want the condition's own %q: %v",
						gotShort, ok, wantShort, err)
				}
				emitted := cerrors.CauseChain(err)
				if !strings.Contains(emitted, sentence) {
					t.Errorf("the refusal does not carry the condition's sentence %q: %s", sentence, emitted)
				}
				if !strings.Contains(emitted, ".context.rendered_name") {
					t.Errorf("the refusal does not name the reference: %s", emitted)
				}
				t.Logf("REFUSED %s [%s]: %s", tc.name, wantShort, emitted)
			})
		}
	}
}
