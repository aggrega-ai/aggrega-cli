// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
)

// The delivery-allowlist grid, driven through the real build.
//
// The rule it exercises is invoked once per connection together with the
// surface that drew it, which is a coordinate no hand-assembled input carries:
// the origin is settled while the authoring tiers are read, so a fixture that
// fabricated a connection would be proving the fixture's account of the
// composition rather than the compiler's. Every row therefore writes documents
// and compiles them.
//
// The catalog is two directories: the shared one below supplies the target
// component, its recipe and the renderer plugin, and a per-test directory
// supplies the consuming component and the Feature whose socket declarations
// the grid varies.
const allowlistSharedCatalog = "../pipeline/testdata/delivery-key-origin/catalog/base"

// allowlistConsumer declares four sockets whose only difference is the shape of
// their inject declaration, and the Feature below declares two more. The
// Component's `open` socket and the Feature's `open` socket carry DIFFERENT
// declarations, so a rule consulting the wrong entity for an attachment-drawn
// connection produces the wrong verdict rather than accidentally the right one.
//
//   - open      allowlist [env file]  — dictation permitted, up to those two
//   - narrow    allowlist [env]       — one member, so a second mechanism is outside
//   - opaque    no inject key at all  — the Black-Box declaration
//   - emptied   inject: []            — the same declaration written explicitly
//
// `wild` is declared on neither entity: it is the ad-hoc case.
const allowlistConsumer = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/consumer
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  usesSchema:
    open:
      selector: "component.aggrega.ai/type=stateful-server"
      inject: ["env", "file"]
    narrow:
      selector: "component.aggrega.ai/type=stateful-server"
      inject: ["env"]
    opaque:
      selector: "component.aggrega.ai/type=stateful-server"
    emptied:
      selector: "component.aggrega.ai/type=stateful-server"
      inject: []
  schema:
    provides:
      host: { type: string, required: true }
`

// allowlistSidecar is the Feature whose own dependency sockets govern a
// connection its attachment draws. `peer` is declared here and on no Component;
// `open` shadows a Component socket that carries an allowlist while declaring
// none itself, which is the interior cell a per-axis reading never reaches.
const allowlistSidecar = `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/sidecar
  version: "1.0.0"
spec:
  interoperability:
    conflictsWith: []
  usesSchema:
    peer:
      selector: "component.aggrega.ai/type=stateful-server"
      inject: ["arg"]
    open:
      selector: "component.aggrega.ai/type=stateful-server"
`

const allowlistConsumerRecipe = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: consumer-recipe
  version: "1.0.0"
spec:
  capability: compose
  implements: core/consumer
  provides:
    host: "{{ .context.instance_id }}"
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: consumer:1.0
        ports:
          p: 8080
`

const allowlistSidecarRecipe = `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: sidecar-compose
  version: "1.0.0"
spec:
  capability: compose
  implements: core/sidecar
`

const allowlistManifest = `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: allowlist-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/consumer: consumer-recipe:1.0.0
        core/db: db-recipe:1.0.0
    features:
      type:
        core/sidecar: sidecar-compose:1.0.0
`

const allowlistRelease = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: allowlist-release
  environment: local
  version: "1.0.0"
spec:
  aggregate: allowlist-app
  manifest: allowlist-manifest
  boundaries:
    cluster:
      directory: ./dist/cluster
      components:
        api: {}
        db: {}
`

// connection is one authored dependency entry: the socket it wires, the
// attachment that wrote it when an attachment did, and the delivery mechanisms
// it dictates. An empty mechanism list writes no inject block at all, which is
// the permitted shape a row needs as its own control.
type connection struct {
	socket     string
	attachment string
	alias      string
	mechanisms []string
}

// injectBlock renders one connection's dictated delivery. Every entry carries a
// literal rather than a reference to a published key, so a row exercises the
// socket's allowlist and cannot halt instead on whether the target publishes
// what the entry reads.
//
// The delivery key is unique per connection AND per mechanism, which each row
// needs for its own reason: two connections on one component delivering one key
// is a separate condition that would halt the build ahead of this one, and a row
// asserting which connection was reported would then be reading the wrong
// refusal.
func injectBlock(indent string, c connection) string {
	if len(c.mechanisms) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(indent + "inject:\n")
	for _, mechanism := range c.mechanisms {
		b.WriteString(indent + "  " + mechanism + ":\n")
		b.WriteString(indent + "    - in: " + deliveryKey(c, mechanism) + "\n")
		b.WriteString(indent + `      value: "static"` + "\n")
	}
	return b.String()
}

// deliveryKey names one connection's delivery under one mechanism, spelled so
// that no two entries in a workspace collide.
func deliveryKey(c connection, mechanism string) string {
	parts := []string{"IN", mechanism, c.socket, c.alias}
	if c.attachment != "" {
		parts = append(parts, c.attachment)
	}
	return strings.ToUpper(strings.Join(parts, "_"))
}

// allowlistAggregate writes one consuming component drawing the given
// connections, each on the surface its own entry names.
func allowlistAggregate(connections ...connection) string {
	var own, attached strings.Builder
	byAttachment := map[string][]connection{}
	var attachmentOrder []string
	for _, c := range connections {
		if c.attachment == "" {
			own.WriteString(fmt.Sprintf("        %s:\n          %s:\n            target: db\n",
				c.socket, c.alias))
			own.WriteString(injectBlock("            ", c))
			continue
		}
		if _, seen := byAttachment[c.attachment]; !seen {
			attachmentOrder = append(attachmentOrder, c.attachment)
		}
		byAttachment[c.attachment] = append(byAttachment[c.attachment], c)
	}
	for _, name := range attachmentOrder {
		attached.WriteString(fmt.Sprintf("        %s:\n          type: core/sidecar\n          uses:\n", name))
		for _, c := range byAttachment[name] {
			attached.WriteString(fmt.Sprintf("            %s:\n              %s:\n                target: db\n",
				c.socket, c.alias))
			attached.WriteString(injectBlock("                ", c))
		}
	}

	doc := `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: allowlist-app
  version: "1.0.0"
spec:
  components:
    db:
      type: core/db
    api:
      type: core/consumer
`
	if own.Len() > 0 {
		doc += "      uses:\n" + own.String()
	}
	if attached.Len() > 0 {
		doc += "      features:\n" + attached.String()
	}
	return doc
}

// writeAllowlistWorkspace lays the triad and the per-test catalog entries down
// in a temp dir and returns the input that builds them. The triad sits at the
// root so the Release's pointers resolve beside it.
func writeAllowlistWorkspace(t *testing.T, aggregate string) pipelinetest.Input {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("aggregate.yaml", aggregate)
	write("manifest.yaml", allowlistManifest)
	write("release.yaml", allowlistRelease)
	write("catalog/components/consumer.yaml", allowlistConsumer)
	write("catalog/features/sidecar.yaml", allowlistSidecar)
	write("catalog/recipes/consumer.yaml", allowlistConsumerRecipe)
	write("catalog/feature-recipes/sidecar.yaml", allowlistSidecarRecipe)
	return pipelinetest.Input{
		ReleasePath:  filepath.Join(dir, "release.yaml"),
		CatalogPaths: []string{filepath.Join(dir, "catalog"), allowlistSharedCatalog},
	}
}

// TestBuildUsesInjectAllowlist enumerates the CELLS of the rule's grid rather
// than the margins of its axes. The axes are the socket's inject declaration
// (non-empty allowlist / absent / explicitly empty / socket not declared at all),
// whether the connection dictates anything, and which entity owns the socket.
// Each row states the cell it occupies, because a defect in the interior — a
// Feature-owned socket with an absent allowlist, say — is reachable while every
// per-axis row passes.
//
// Every rejected mechanism name is a real one with a single character removed, so
// a row cannot pass because the compiler rejected a string no author would type.
func TestBuildUsesInjectAllowlist(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		draw []connection
		// wantHalt is empty when the workspace must build.
		wantHalt []string
	}{
		{
			name: "declared allowlist, dictated mechanism is a member: builds",
			draw: []connection{{socket: "open", alias: "d1", mechanisms: []string{"env"}}},
		},
		{
			name: "declared allowlist, dictated mechanism is not a member: halts naming both",
			draw: []connection{{socket: "open", alias: "d2", mechanisms: []string{"fil"}}},
			wantHalt: []string{`"fil"`, `on socket "open"`, `which permits "env, file"`,
				`declared on the component type "core/consumer"`},
		},
		{
			name:     "one-member allowlist, a second core mechanism is outside it: halts",
			draw:     []connection{{socket: "narrow", alias: "d3", mechanisms: []string{"file"}}},
			wantHalt: []string{`"file"`, `on socket "narrow"`, `which permits "env"`},
		},
		{
			name:     "one-member allowlist, mixed dictation: halts on the outside one only",
			draw:     []connection{{socket: "narrow", alias: "d4", mechanisms: []string{"env", "arg"}}},
			wantHalt: []string{`"arg"`},
		},
		{
			name:     "socket declares no inject key at all, connection dictates: halts as Black-Box",
			draw:     []connection{{socket: "opaque", alias: "d5", mechanisms: []string{"env"}}},
			wantHalt: []string{`"env"`, `on socket "opaque"`, "declares no delivery allowlist"},
		},
		{
			name: "socket declares no inject key, the SAME connection without dictation: builds",
			draw: []connection{{socket: "opaque", alias: "d6"}},
		},
		{
			name:     "socket declares an explicitly empty allowlist, connection dictates: halts as Black-Box",
			draw:     []connection{{socket: "emptied", alias: "d7", mechanisms: []string{"env"}}},
			wantHalt: []string{`on socket "emptied"`, "declares no delivery allowlist"},
		},
		{
			name: "declared allowlist, no dictation: builds",
			draw: []connection{{socket: "open", alias: "d8"}},
		},
		{
			name: "ad-hoc socket, connection dictates: builds, because no declaration was made",
			draw: []connection{{socket: "wild", alias: "d9", mechanisms: []string{"env"}}},
		},
		{
			name: "Feature-owned socket, dictated mechanism is a member: builds",
			draw: []connection{{socket: "peer", attachment: "side", alias: "d10",
				mechanisms: []string{"arg"}}},
		},
		{
			name: "Feature-owned socket, dictated mechanism is outside ITS allowlist: halts against the Feature",
			draw: []connection{{socket: "peer", attachment: "side", alias: "d11",
				mechanisms: []string{"env"}}},
			wantHalt: []string{`"env"`, `on socket "peer"`, `which permits "arg"`,
				`declared on the feature type "core/sidecar"`},
		},
		{
			// The interior cell: `open` carries an allowlist on the Component and NO
			// inject key on the Feature. A rule consulting the consuming Component for
			// an attachment-drawn connection would permit this.
			name: "Feature-owned socket with no inject key, shadowing a Component socket that has one: halts",
			draw: []connection{{socket: "open", attachment: "side", alias: "d12",
				mechanisms: []string{"env"}}},
			wantHalt: []string{`on socket "open"`, "declares no delivery allowlist",
				`declared on the feature type "core/sidecar"`},
		},
		{
			// The mirror interior cell: `narrow` is on the Component only. A rule
			// consulting the Feature for a component-drawn connection would call it
			// ad-hoc and permit anything.
			name:     "Component-owned socket absent from the Feature, component-drawn: halts against the Component",
			draw:     []connection{{socket: "narrow", alias: "d13", mechanisms: []string{"arg"}}},
			wantHalt: []string{`"arg"`, `on socket "narrow"`, `"core/consumer"`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := pipelinetest.BuildE(t, writeAllowlistWorkspace(t, allowlistAggregate(tc.draw...)))
			if len(tc.wantHalt) == 0 {
				if err != nil {
					t.Fatalf("workspace must build, got halt: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("workspace must halt, but the build succeeded")
			}
			if !errors.Is(err, ir.ErrUsesInjectForbidden()) {
				t.Fatalf("halt must carry ErrUsesInjectForbidden, got %v", err)
			}
			for _, want := range tc.wantHalt {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("halt must name %q; got %q", want, err.Error())
				}
			}
		})
	}
}

// TestBuildUsesInjectAllowlistSeparatesEveryOriginOnOneSocketAndAlias is the
// proof the origin coordinate exists for: one node, one socket and one alias
// written three times, once by the component's own dependency block and once by
// each of two attachments.
//
// A rule invoked once per node, socket and alias reads one of the three and
// reports the other two as approved — which is indistinguishable, from the
// outside, from a rule that examined them. So the assertion is that the build
// refuses BOTH attachment-drawn dictations while the component's own permitted
// one passes unmentioned: the two attachment blocks are measured against the
// Feature's declaration, which permits `arg` and not `env`, while the
// component's block is measured against the Component's, which permits `env`.
func TestBuildUsesInjectAllowlistSeparatesEveryOriginOnOneSocketAndAlias(t *testing.T) {
	t.Parallel()

	_, err := pipelinetest.BuildE(t, writeAllowlistWorkspace(t, allowlistAggregate(
		connection{socket: "open", alias: "primary", mechanisms: []string{"env"}},
		connection{socket: "open", attachment: "sb_one", alias: "primary",
			mechanisms: []string{"env"}},
		connection{socket: "open", attachment: "sb_two", alias: "primary",
			mechanisms: []string{"file"}},
	)))
	if err == nil {
		t.Fatal("two attachment-drawn dictations into a socket the Feature declares Black-Box must halt")
	}
	if !errors.Is(err, ir.ErrUsesInjectForbidden()) {
		t.Fatalf("halt must carry ErrUsesInjectForbidden, got %v", err)
	}
	// Each attachment's finding is located at that attachment's own block and
	// names the mechanism that attachment dictated. A rule reading one block for
	// the whole triple could satisfy neither: it is invoked once, so it can
	// report at most one of the three.
	for _, want := range []string{
		".spec.components.api.features.sb_one.uses.open.primary",
		".spec.components.api.features.sb_two.uses.open.primary",
		`"env"`, `"file"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the halt must carry %s.\n"+
				"INVARIANT: a connection is examined once per surface that drew it.\n"+
				"CONSEQUENCE: an origin the rule is never asked about raises nothing, "+
				"which reads exactly like an origin that was examined and approved.\ngot %q",
				want, err.Error())
		}
	}
	// The component's own block dictates `env`, which the Component's own `open`
	// socket permits. A delivery finding naming it would mean the rule measured
	// one origin's dictation against another origin's declaration. The question is
	// asked of this rule's findings alone, because the same build also refuses the
	// three connections for sharing one alias, and that finding names the
	// component's own entry as the connection already holding it.
	deliveryFindings := pipelinetest.Findings(err, ir.ErrUsesInjectForbidden())
	if len(deliveryFindings) != 2 {
		t.Errorf("the halt carries %d delivery finding(s); want 2, one per attachment; got %q",
			len(deliveryFindings), err.Error())
	}
	for _, finding := range deliveryFindings {
		if strings.Contains(finding.Error(), ".spec.components.api.uses.open.primary") {
			t.Errorf("a delivery finding names the component's own dependency block, whose dictation its own "+
				"socket permits; got %q", finding.Error())
		}
	}
	// The alias refusal stands beside them, once per attachment-drawn connection,
	// each at its own block and each naming the component's own entry as the one
	// already holding the alias.
	aliasFindings := pipelinetest.Findings(err, topology.ErrDuplicateConnectionAlias())
	wantAliasAt := []string{
		".spec.components.api.features.sb_one.uses.open.primary",
		".spec.components.api.features.sb_two.uses.open.primary",
	}
	if len(aliasFindings) != len(wantAliasAt) {
		t.Fatalf("the halt carries %d alias finding(s); want %d; got %q",
			len(aliasFindings), len(wantAliasAt), err.Error())
	}
	for i, finding := range aliasFindings {
		if got := finding.Source().Locator; got != wantAliasAt[i] {
			t.Errorf("alias finding %d is positioned at %q; want %q", i, got, wantAliasAt[i])
		}
		if !strings.Contains(finding.Error(), `".spec.components.api.uses.open.primary"`) {
			t.Errorf("alias finding %d does not name the component's own entry as the connection already "+
				"holding the alias; got %q", i, finding.Error())
		}
	}
}

// TestBuildUsesInjectAllowlistReportsEveryOffendingConnection pins the
// collect-then-report contract: an author fixing several connections sees them
// all in one build. A rule returning on the first violation is
// indistinguishable from this one on a single-connection input, which is why the
// assertion needs more than one.
func TestBuildUsesInjectAllowlistReportsEveryOffendingConnection(t *testing.T) {
	t.Parallel()

	_, err := pipelinetest.BuildE(t, writeAllowlistWorkspace(t, allowlistAggregate(
		connection{socket: "opaque", alias: "first", mechanisms: []string{"env"}},
		connection{socket: "open", alias: "second", mechanisms: []string{"env"}},
		connection{socket: "narrow", alias: "third", mechanisms: []string{"arg"}},
	)))
	if err == nil {
		t.Fatal("two offending connections must halt the build")
	}
	// The permitted middle connection must not be reported, and both offenders
	// must be — each named by the socket it was written on, which is what tells
	// the three apart in the authored document.
	for _, want := range []string{`"opaque"`, `"narrow"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("halt must name the offending socket %s; got %q", want, err.Error())
		}
	}
	if strings.Contains(err.Error(), `"open"`) {
		t.Errorf("halt must not name the permitted connection's socket; got %q", err.Error())
	}
}
