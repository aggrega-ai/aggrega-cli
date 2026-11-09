// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// placementGraphFacts stands in for the presentation a build hands the render.
// Supplying one is what switches on the rules declared over assembled output —
// among them the refusal of two contributors writing one path. Without it these
// corpora would assemble a collision instead of halting on it, and the placement
// under test would then be proven against a weaker pipeline than a build runs.
type placementGraphFacts struct{ constraint.GraphView }

// placementHostedInput returns a render input whose named components all sit in
// ONE boundary and all carry the SAME attachment instance name, which is what
// puts their attachments in one dispatch group and so makes their output paths
// genuinely meet.
func placementHostedInput(hosts []string, instance, featureType string) renderer.RendererInput {
	input := testInput()

	componentLabels := types.OrderedMap[string]{}
	componentLabels.Set("component.aggrega.ai/type", "server")

	nodes := types.OrderedMap[types.IRNode]{}
	valuesMapping := types.OrderedMap[json.RawMessage]{}
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	order := ir.FeatureOrder{}
	for _, host := range hosts {
		nodes.Set(host, types.IRNode{
			Component: "core/compute/api-server", Labels: componentLabels,
			ResolvedVersion: "1.0.0", BoundaryPath: "local/default",
		})
		valuesMapping.Set(host, json.RawMessage(`{"port":8080}`))

		vals := types.OrderedMap[any]{}
		vals.Set("scrape_port", float64(9090))
		inner := types.OrderedMap[types.IRFeatureEntry]{}
		inner.Set(instance, types.IRFeatureEntry{Type: featureType, Values: vals})
		fvm.Set(host, inner)
		order[host] = []string{instance}
	}
	input.Graph.Spec.Nodes = nodes
	input.Graph.Spec.ValuesMapping = valuesMapping
	input.Graph.Spec.FeatureValuesMapping = fvm
	input.FeatureOrder = order
	input.GraphView = placementGraphFacts{}
	return input
}

// placementLookup binds every named component to a workload Recipe and the one
// feature type to the supplied FeatureRecipe.
func placementLookup(fr *types.FeatureRecipe, featureType string) renderer.RecipeLookup {
	return renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType != "core/compute/api-server" {
				return nil, false
			}
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe"},
				Spec: types.RecipeSpec{
					Capability: "helm",
					Implements: "core/compute/api-server",
					Workload: types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1beta1
kind: Workload
containers:
  main:
    image: myapp:latest
    ports:
      web: 8080
`},
				},
			}, true
		},
		FeatureRecipe: func(_, ft string) (*types.FeatureRecipe, bool) {
			if ft != featureType {
				return nil, false
			}
			return fr, true
		},
	}
}

// placementTwoAttachmentsOnOneHostInput returns a render input attaching TWO
// features to ONE component, under two instance names and two feature types.
//
// Two TYPES rather than two instances of one type, so the corpus cannot be
// deflected by any rule about attaching one feature twice: the subject here is
// two contributors whose emitted filenames agree, and the types are only the way
// to get two independent FeatureRecipes onto one host.
func placementTwoAttachmentsOnOneHostInput(host string, instanceTypes [][2]string) renderer.RendererInput {
	input := testInput()

	componentLabels := types.OrderedMap[string]{}
	componentLabels.Set("component.aggrega.ai/type", "server")

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(host, types.IRNode{
		Component: "core/compute/api-server", Labels: componentLabels,
		ResolvedVersion: "1.0.0", BoundaryPath: "local/default",
	})
	valuesMapping := types.OrderedMap[json.RawMessage]{}
	valuesMapping.Set(host, json.RawMessage(`{"port":8080}`))

	inner := types.OrderedMap[types.IRFeatureEntry]{}
	var order []string
	for _, it := range instanceTypes {
		vals := types.OrderedMap[any]{}
		vals.Set("scrape_port", float64(9090))
		inner.Set(it[0], types.IRFeatureEntry{Type: it[1], Values: vals})
		order = append(order, it[0])
	}
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set(host, inner)

	input.Graph.Spec.Nodes = nodes
	input.Graph.Spec.ValuesMapping = valuesMapping
	input.Graph.Spec.FeatureValuesMapping = fvm
	input.FeatureOrder = ir.FeatureOrder{host: order}
	input.GraphView = placementGraphFacts{}
	return input
}

// placementMultiTypeLookup binds every named component to a workload Recipe
// rendered by hostCapability, and each feature type to a FeatureRecipe of its own
// rendered by frCapability.
//
// The two capabilities are separate parameters because the placement shape is the
// HOST group's answer, so a case needs to be able to set the host's provider and
// the attachment's independently — a merging host carrying an attachment that
// rendered for another target is exactly the configuration that reaches the
// placed arm.
func placementMultiTypeLookup(hostCapability, frCapability string, featureTypes []string) renderer.RecipeLookup {
	return renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType != "core/compute/api-server" {
				return nil, false
			}
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe"},
				Spec: types.RecipeSpec{
					Capability: hostCapability,
					Implements: "core/compute/api-server",
					Workload: types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1beta1
kind: Workload
containers:
  main:
    image: myapp:latest
    ports:
      web: 8080
`},
				},
			}, true
		},
		FeatureRecipe: func(_, ft string) (*types.FeatureRecipe, bool) {
			for _, want := range featureTypes {
				if ft != want {
					continue
				}
				fr := artifactsHostComposedFR(ft)
				fr.Spec.Capability = frCapability
				return fr, true
			}
			return nil, false
		},
	}
}

// TestRenderAll_TwoAttachmentsOnOneHostBothSurvive is the collision proof the
// flat shape rests on, and it is stated directly rather than by appeal to a
// guard.
//
// 🔴 NOTHING WOULD CATCH THE FAILURE THIS REFUSES. The rule that refuses one
// output path written by more than one contributor needs to see two producers at
// that path, and a component-scope attachment defeats it: its files merge into
// the bucket of the node hosting them, and that bucket carries ONE producer label
// computed per node — so two attachments on one component arrive as a single
// producer and the predicate never fires. Earlier still, both attachments write
// into one map inside the attachment loop itself, so the loser's bytes are gone
// before any producer label exists. No halt, no diagnostic, a file simply absent.
//
// Which is why BOTH files are required present. An assertion that one of them
// exists passes on exactly the defect this covers, and so does a count of one.
//
// The two attachments emit the SAME filename on purpose: an artifact's output
// filename is the author's map key verbatim, so the only thing that can separate
// them is the contributor prefix the compiler composes in front of it. Their
// resource NAMES differ because the bodies compose the host and the attachment
// key — otherwise the build halts on the names instead, and the path property
// would go untested behind a refusal.
func TestRenderAll_TwoAttachmentsOnOneHostBothSurvive(t *testing.T) {
	t.Parallel()

	const host = "api"
	instanceTypes := [][2]string{{"aux", "core/aux"}, {"bux", "core/bux"}}
	input := placementTwoAttachmentsOnOneHostInput(host, instanceTypes)

	result, err := renderer.RenderAll(context.Background(), input,
		placementMultiTypeLookup("helm", "helm", []string{"core/aux", "core/bux"}), testHelmFactory(t))
	if err != nil {
		t.Fatalf("two attachments on one component with distinct resource names must build, got: %v", err)
	}

	keys := fileKeys(result.Files)
	for _, it := range instanceTypes {
		want := "boundaries/local/default/helm/" + host + "--" + it[0] + "--policy.yaml"
		if _, present := result.Files[want]; !present {
			t.Errorf("the emitted set carries no %q; one attachment's file was dropped with nothing reporting it\nkeys: %v",
				want, keys)
		}
	}
	// The count is asserted beside the two presence reads, because a build that
	// somehow emitted a THIRD prefixed file would mean the coordinates stopped
	// identifying one contributor each.
	var prefixed int
	for _, k := range keys {
		if strings.Contains(k, host+"--") {
			prefixed++
		}
	}
	if prefixed != len(instanceTypes) {
		t.Errorf("%d files carry a contributor prefix, want %d — one per attachment\nkeys: %v",
			prefixed, len(instanceTypes), keys)
	}
	t.Logf("both attachments survived: %v", keys)
}

// TestRenderAll_MergingHostPlacesAForeignAttachmentFlat is the merge row's placed
// arm, built deliberately because the configuration is reachable and was covered
// only incidentally.
//
// A host whose provider FOLDS its dispatch group into one document has no
// per-contributor position to offer. Where the attachment rendered for that same
// target its output is folded in and needs no position at all; where it rendered
// for a DIFFERENT target the assembler cannot read it, so the file is written
// beside the assembled document instead — and it is that arm which needs a
// position and gets the flat one.
//
// So the assertion is two-sided: the attachment's file exists, carrying its
// contributor prefix, and it opens no directory level. A merging host that gave a
// placed attachment an enclosing directory would satisfy a presence read while
// putting the file back where a consumer reading one directory misses it.
func TestRenderAll_MergingHostPlacesAForeignAttachmentFlat(t *testing.T) {
	t.Parallel()

	const (
		host        = "api"
		instance    = "guard"
		featureType = "core/guard"
	)
	input := placementTwoAttachmentsOnOneHostInput(host, [][2]string{{instance, featureType}})

	// A compose host folds its group into one document; the attachment renders a
	// Kubernetes manifest, so it rendered for another target and cannot be folded.
	result, err := renderer.RenderAll(context.Background(), input,
		placementMultiTypeLookup("compose", "helm", []string{featureType}), testHelmFactory(t))
	if err != nil {
		t.Fatalf("a merging host carrying an attachment for another target must build, got: %v", err)
	}

	keys := fileKeys(result.Files)
	want := host + "--" + instance + "--policy.yaml"
	var found string
	for _, k := range keys {
		if strings.HasSuffix(k, want) {
			found = k
			break
		}
	}
	if found == "" {
		t.Fatalf("no emitted key ends in %q; a placed contribution under a merging host must carry its contributor prefix\nkeys: %v",
			want, keys)
	}
	if strings.Contains(found, "/features/") {
		t.Errorf("the placed contribution %q sits under an enclosing directory; a merging host offers no position, so there is none to put it in", found)
	}
	t.Logf("the merging host placed the foreign attachment at: %s", found)
}

// artifactsOnlyFR is a FeatureRecipe declaring artifacts: and nothing else,
// naming its resource with a literal. The artifact's output filename is the
// author's map key verbatim, so nothing in the emitted PATH separates one host's
// copy from another's — the placement directory is the only thing that does. The
// name inside the two bodies is therefore identical, which is the collision the
// case below refuses.
func artifactsOnlyFR(featureType string) *types.FeatureRecipe {
	art := types.OrderedMap[types.ArtifactEntry]{}
	art.Set("policy.yaml", types.ArtifactEntry{Inline: `apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: aux-policy
spec:
  podSelector: {}
`})
	return &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "aux-fr", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Capability: "helm",
			Implements: featureType,
			Artifacts:  art,
		},
	}
}

// artifactsHostComposedFR is the same FeatureRecipe with the remedy applied: the
// host component and the attachment key are composed into the resource name, so
// one authored body reaches each host under a name of its own.
//
// It exists so the placement property keeps a two-host proof. Placement is a
// property of the attachment rather than of what its body names, so it needs a
// two-host corpus that BUILDS — and after the refusal below, a two-host corpus
// builds only when its names are distinct.
func artifactsHostComposedFR(featureType string) *types.FeatureRecipe {
	art := types.OrderedMap[types.ArtifactEntry]{}
	art.Set("policy.yaml", types.ArtifactEntry{Inline: `apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{ .context.instance_id }}-{{ .attachment_name }}-policy
spec:
  podSelector: {}
`})
	return &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "aux-fr", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Capability: "helm",
			Implements: featureType,
			Artifacts:  art,
		},
	}
}

// TestRenderAll_ArtifactsAttachmentOnTwoComponentsWithOneNameHalts is the
// end-to-end proof that one instance name attached to two components in one
// boundary, bound to an artifacts-only FeatureRecipe whose body names its
// resource with a literal, is REFUSED.
//
// 🔴 The assertion here was inverted deliberately, and a reader meeting the
// change should not read it as a regression. This case previously required the
// colliding corpus to BUILD, and the reason it gave was a property of the reader
// rather than of the authoring surface: the derived-name check compared names
// read out of the documents a render produced, and an artifacts body was not one
// of those, so nothing looked inside it. Nothing looking is a blind spot, not a
// guarantee — the two hosts each emit a NetworkPolicy under one name, and in the
// cluster the second overwrites the first while the build reports success.
//
// The emitted paths differ only by the host directory, so the assembled-path
// check cannot see the conflict: those paths genuinely differ. What conflicts is
// the name inside the bodies, and comparing it is what this now covers.
//
// The names in an artifacts body are authored rather than derived by the render.
// That deliberately does not change the answer, because two resources reaching
// one name in one namespace is a cluster overwrite whichever surface the name
// came from — the identical collision through a chart template already halts, and
// a difference in outcome there would be a difference in what the reader happened
// to look at rather than in what the output does.
//
// The remedy is on the authoring surface and it is proven beside this case rather
// than asserted: an artifacts body is a template and receives the host component
// and the attachment key, so a name that has to differ per host is composed from
// them. See the placement case below, which builds the composed form over the same
// two hosts.
func TestRenderAll_ArtifactsAttachmentOnTwoComponentsWithOneNameHalts(t *testing.T) {
	t.Parallel()

	const (
		instance    = "aux"
		featureType = "core/aux"
	)
	input := placementHostedInput([]string{"api", "worker"}, instance, featureType)

	_, err := renderer.RenderAll(context.Background(), input,
		placementLookup(artifactsOnlyFR(featureType), featureType), testHelmFactory(t))
	if err == nil {
		t.Fatal("one literal resource name reaching two hosts built cleanly; want a refusal naming both")
	}
	// Asserted against the collision specifically rather than against any failure:
	// a corpus that fell over for another reason would not prove the names were
	// what stopped it.
	if !errors.Is(err, renderer.ErrDerivedKeyCollision()) {
		t.Fatalf("the render failed for a different reason than the name collision: %v", err)
	}
	t.Logf("the colliding artifacts corpus is refused: %v", err)
}

// TestRenderAll_ArtifactsAttachmentOnTwoComponentsPlacesUnderEachHost pins the
// placement of a two-host artifacts attachment: each attachment's output lands
// beside the component hosting it, carrying that host in its filename, and the
// filename prefix is what separates the two copies because the name the author
// wrote is the same for both.
//
// One instance key on two components is ordinary authoring, and a cascade that
// mints one instance name across many components by design produces it without
// the author choosing it — so the placement has to keep working for exactly that
// shape.
//
// It uses the host-composed body rather than the literal one, and that is the
// substance rather than an incidental choice: placement is a property of the
// attachment and not of what its body names, so proving it needs a two-host
// corpus that builds — and a two-host corpus builds when its resource names are
// distinct.
//
// 🔴 Both files are required present, not one. Two contributors meeting on one
// output path is refused where the group can see two producers, and a
// component-scope attachment is exactly the case it cannot: its files merge into
// the bucket of the node hosting it, which is stamped with one producer label, so
// the refusal's predicate never fires and the loser is dropped by a plain map
// assignment. An assertion that one of the two exists would pass on that defect.
func TestRenderAll_ArtifactsAttachmentOnTwoComponentsPlacesUnderEachHost(t *testing.T) {
	t.Parallel()

	const (
		instance    = "aux"
		featureType = "core/aux"
	)
	input := placementHostedInput([]string{"api", "worker"}, instance, featureType)

	result, err := renderer.RenderAll(context.Background(), input,
		placementLookup(artifactsHostComposedFR(featureType), featureType), testHelmFactory(t))
	if err != nil {
		t.Fatalf("one instance name on two components with distinct resource names must build, got: %v", err)
	}

	keys := fileKeys(result.Files)
	joined := strings.Join(keys, "\n")
	for _, host := range []string{"api", "worker"} {
		want := host + "--" + instance + "--policy.yaml"
		if !strings.Contains(joined, want) {
			t.Errorf("no output key carries %q; the two attachments have not been separated by their filename prefixes\nkeys: %v",
				want, keys)
		}
	}
}

// TestRenderAll_AttachmentPlacementIsOneShapeAcrossInputKinds pins the placement
// as a property of the attachment rather than of the input kind it renders from.
// Under a provider that gives no contributor a position of its own, sourced,
// synthesized and artifacts output all land beside the host's own files behind
// one <host>--<instance>-- filename prefix, so an author changing a
// FeatureRecipe's delivery mode does not find their output paths moved.
func TestRenderAll_AttachmentPlacementIsOneShapeAcrossInputKinds(t *testing.T) {
	t.Parallel()

	const (
		host        = "api"
		instance    = "aux"
		featureType = "core/aux"
		wantPrefix  = host + "--" + instance + "--"
	)

	sourcedFR := func(t *testing.T) *types.FeatureRecipe {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"),
			[]byte("apiVersion: v2\nname: aux\nversion: 1.0.0\n"), 0o644); err != nil {
			t.Fatalf("write Chart.yaml: %v", err)
		}
		if err := os.Mkdir(filepath.Join(dir, "templates"), 0o755); err != nil {
			t.Fatalf("mkdir templates: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "templates", "configmap.yaml"),
			[]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: aux-config\ndata:\n  marker: \"x\"\n"), 0o644); err != nil {
			t.Fatalf("write configmap.yaml: %v", err)
		}
		return &types.FeatureRecipe{
			Metadata: types.FeatureRecipeMetadata{Name: "aux-fr", Version: "1.0.0"},
			Spec: types.FeatureRecipeSpec{
				Capability:    "helm",
				Implements:    featureType,
				Source:        dir,
				ValuesMapping: types.ValuesMapping{Inline: `{}`},
			},
		}
	}

	workloadFR := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "aux-fr", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Capability: "helm",
			Implements: featureType,
			Workload: types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1beta1
kind: Workload
containers:
  main:
    image: prom/aux:v1
`},
		},
	}

	cases := []struct {
		kind string
		fr   func(t *testing.T) *types.FeatureRecipe
	}{
		{kind: "sourced", fr: sourcedFR},
		{kind: "workload", fr: func(*testing.T) *types.FeatureRecipe { return workloadFR }},
		{kind: "artifacts", fr: func(*testing.T) *types.FeatureRecipe { return artifactsOnlyFR(featureType) }},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.kind, func(t *testing.T) {
			t.Parallel()
			input := placementHostedInput([]string{host}, instance, featureType)
			result, err := renderer.RenderAll(context.Background(), input,
				placementLookup(tc.fr(t), featureType), testHelmFactory(t))
			if err != nil {
				t.Fatalf("%s attachment must render, got: %v", tc.kind, err)
			}
			var placed []string
			for _, k := range fileKeys(result.Files) {
				if strings.Contains(k, wantPrefix) {
					placed = append(placed, k)
				}
			}
			sort.Strings(placed)
			if len(placed) == 0 {
				t.Fatalf("%s attachment emitted nothing carrying the contributor prefix %q; keys: %v",
					tc.kind, wantPrefix, fileKeys(result.Files))
			}
			// No output opens a directory level under this provider: the whole
			// point of the shape is that a consumer reading only the files sitting
			// directly in a directory finds the attachment's, so a surviving
			// features/ directory would leave the defect in place while the prefix
			// assertion above passed.
			for _, k := range fileKeys(result.Files) {
				if strings.Contains(k, "/features/") {
					t.Errorf("%s attachment output %q still sits under an enclosing directory", tc.kind, k)
				}
			}
			t.Logf("%s attachment emitted: %v", tc.kind, placed)
		})
	}
}

// TestRenderAll_DottedHostEscapesTheDotIntoTheFilenamePrefix pins that a host
// whose id carries a dot reaches a filename prefix with that dot ESCAPED, under a
// provider that gives no contributor a position of its own.
//
// The escape is not tidiness. A coordinate becomes part of a filename here rather
// than a path segment of its own, so a dot carried through as authored would let
// the prefix read as a relative-path reference and would let a derived filename
// begin with the character that hides a file. The file's own name keeps whatever
// dots it carries, which is why the suffix still reads as a suffix.
//
// 🔴 The opposite property — a dot written into a DIRECTORY segment as authored,
// unescaped, because every character an author may put in a component identity is
// legal in a directory name — is still the behaviour for a provider whose group
// gives each contributor a position of its own, and it is held to account by the
// directory-shape row in TestAttachmentOutputPrefixKeepsTheDirectoryShapeUnchanged.
// The two shapes escape differently on purpose; neither assertion covers the other.
func TestRenderAll_DottedHostEscapesTheDotIntoTheFilenamePrefix(t *testing.T) {
	t.Parallel()

	const (
		host        = "data.main-db"
		instance    = "aux"
		featureType = "core/aux"
	)
	input := placementHostedInput([]string{host}, instance, featureType)

	result, err := renderer.RenderAll(context.Background(), input,
		placementLookup(artifactsOnlyFR(featureType), featureType), testHelmFactory(t))
	if err != nil {
		t.Fatalf("a dotted host must render, got: %v", err)
	}

	keys := fileKeys(result.Files)
	joined := strings.Join(keys, "\n")
	if strings.Contains(joined, host+"--") {
		t.Errorf("an output key carries the host's dot as authored; a filename prefix must escape it\nkeys: %v", keys)
	}
	// Written out rather than computed from the encoding under test. A name
	// derived by calling the composition would agree with it whatever it did, so
	// the assertion could not fail; the literal is what makes a change to the
	// escape rule arrive as a failure a reader has to look at.
	const want = "data-zdmain-db--aux--policy.yaml"
	if !strings.Contains(joined, want) {
		t.Errorf("no output key carries %q — the dotted host must reach an escaped filename prefix\nkeys: %v", want, keys)
	}
	t.Logf("dotted host emitted: %v", keys)
}
