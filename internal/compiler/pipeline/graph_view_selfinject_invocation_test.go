// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// The authored fragments these proofs splice into the fixture. They are held as
// constants so each proof names the shape it compiles rather than assembling one
// inline, and so the anchor is written once: a splice whose anchor has drifted
// out of the fixture is a proof that silently compiles the unspliced document.
const (
	frontEndAnchor = "    frontend:\n      type: core/compute/web-ui\n"

	// One delivery key the front end declares for itself, colliding with
	// nothing. The front end's own dependency block delivers API_HOST and
	// API_PORT, so a third name is admitted by every reading.
	uncollidingSelfInject = "      inject:\n" +
		"        env:\n" +
		"          - in: UI_MODE\n" +
		"            value: \"dark\"\n"

	// The same key declared for itself AND delivered by connections an
	// attachment projected. The projection is what puts the collision beyond
	// the reach of the reading that runs before the composition: that one
	// searches the component's own dependency block, which names neither of
	// these connections.
	projectedCollisionSelfInject = "      inject:\n" +
		"        env:\n" +
		"          - in: PROJECTED_KEY\n" +
		"            value: \"from-self\"\n" +
		"      features:\n" +
		"        marker:\n" +
		"          type: core/proof-injector\n" +
		"          uses:\n" +
		"            apis:\n" +
		"              via-feature:\n" +
		"                target: backend\n" +
		"                inject:\n" +
		"                  env:\n" +
		"                    - in: PROJECTED_KEY\n" +
		"                      from: host\n" +
		"            backups:\n" +
		"              also-via-feature:\n" +
		"                target: backend\n" +
		"                inject:\n" +
		"                  env:\n" +
		"                    - in: PROJECTED_KEY\n" +
		"                      from: host\n"
)

// TestTheInjectionAxesComeFromTheCompiledGraph is the enumeration proof for the
// two axes the self-inject collision rule is invoked once per.
//
// The axes read the injection block the finalised graph carries for a node,
// keyed by the identity that graph gives it. That is the whole reason they exist
// rather than delegating to the fact naming the same keys: the fact belongs to a
// stage whose presentation is built from the application's own documents, so it
// answers emptily for a component a sub-aggregate contributed — and an axis
// answering emptily presents a rule no subject at all, which reads exactly like a
// rule that ran and approved.
func TestTheInjectionAxesComeFromTheCompiledGraph(t *testing.T) {
	t.Run("they answer for a node of a real compilation", func(t *testing.T) {
		out, catalogLookup := compileSpliced(t, invocationCatalogPath, uncollidingSelfInject)
		coordinates := coordinatesOf(t, out, catalogLookup)

		if got := coordinates.Mechanisms("frontend"); !slices.Equal(got, []string{"env"}) {
			t.Errorf("Mechanisms(%q) = %v, want the one mechanism the block declares", "frontend", got)
		}
		if got := coordinates.DeliveryKeys("frontend", "env"); !slices.Equal(got, []string{"UI_MODE"}) {
			t.Errorf("DeliveryKeys(%q, %q) = %v, want the one key the block delivers", "frontend", "env", got)
		}
		// A node whose author wrote no block contributes no subject, and a node
		// the graph does not hold contributes none either. Both are asserted
		// because an axis answering something for either would invoke a rule for
		// a coordinate no fact can answer about.
		if got := coordinates.Mechanisms("backend"); len(got) != 0 {
			t.Errorf("Mechanisms(%q) = %v for a node that declares no injection block", "backend", got)
		}
		if got := coordinates.Mechanisms("no-such-node"); len(got) != 0 {
			t.Errorf("Mechanisms(%q) = %v for a node the graph does not hold", "no-such-node", got)
		}
		if got := coordinates.DeliveryKeys("frontend", "no-such-mechanism"); len(got) != 0 {
			t.Errorf("DeliveryKeys for a mechanism the block does not declare = %v", got)
		}
	})

	t.Run("they agree with the earlier stage wherever that stage can answer", func(t *testing.T) {
		// The parity assertion that holds this reading to the one already
		// shipped. Both read one authored shape, so a divergence in what counts
		// as a delivery key, or in the order the keys come back, fails here
		// rather than as a rule that quietly visits a different subject set.
		out, catalogLookup := compileSpliced(t, invocationCatalogPath, uncollidingSelfInject)
		coordinates := coordinatesOf(t, out, catalogLookup)

		compared := 0
		for _, node := range coordinates.Nodes() {
			earlierMechanisms := out.GraphView.AuthoredInjectMechanismNames(node.ID)
			if len(earlierMechanisms) == 0 {
				continue
			}
			if got := coordinates.Mechanisms(node.ID); !slices.Equal(got, earlierMechanisms) {
				t.Errorf("node %q: axis mechanisms %v, earlier stage %v", node.ID, got, earlierMechanisms)
			}
			for _, mechanism := range earlierMechanisms {
				earlierKeys := out.GraphView.AuthoredInjectDeliveryKeys(node.ID, mechanism)
				if got := coordinates.DeliveryKeys(node.ID, mechanism); !slices.Equal(got, earlierKeys) {
					t.Errorf("node %q mechanism %q: axis keys %v, earlier stage %v",
						node.ID, mechanism, got, earlierKeys)
				}
				compared++
			}
		}
		if compared == 0 {
			t.Fatal("the parity assertion compared nothing; it would pass on any implementation")
		}
		t.Logf("compared %d (node, mechanism) pair(s) against the earlier stage's own reading", compared)
	})

	t.Run("only these axes see a component a sub-aggregate contributed", func(t *testing.T) {
		// The case that makes the second reading necessary rather than merely
		// different. Without it the corpus cannot tell the two designs apart: on
		// a single-document fixture they agree everywhere, and the design that
		// silently skips composed components passes.
		catalog := moduleInjectCatalog(t)
		out, catalogLookup := compileSpliced(t, catalog, "")
		coordinates := coordinatesOf(t, out, catalogLookup)

		const contributed = "data.main-db"
		if _, held := out.IRGraph.Spec.Nodes.Get(contributed); !held {
			t.Fatalf("the fixture no longer contributes %q; this proof would assert nothing", contributed)
		}
		if got := coordinates.Mechanisms(contributed); !slices.Equal(got, []string{"env"}) {
			t.Fatalf("Mechanisms(%q) = %v, want the mechanism the module's document declares", contributed, got)
		}
		if got := coordinates.DeliveryKeys(contributed, "env"); !slices.Equal(got, []string{"MODULE_KEY"}) {
			t.Errorf("DeliveryKeys(%q, %q) = %v, want the key the module's document delivers",
				contributed, "env", got)
		}
		// The earlier stage's fact is empty here, and that is the measurement
		// this proof exists to pin. It is asserted rather than merely noted: were
		// it ever to answer, the second reading above could be retired, and were
		// this assertion absent nothing would say why it is not.
		if earlier := out.GraphView.AuthoredInjectMechanismNames(contributed); len(earlier) != 0 {
			t.Errorf("the earlier stage now answers %v for a contributed component; the axes may delegate to it again", earlier)
		}
	})
}

// TestTheSelfInjectCollisionRuleIsInvokedOverTheCompiledGraph invokes the rule
// through the framework's traversal over the presentation and the enumeration the
// pipeline itself built, and reads its verdict against the verdict of the
// hand-written reading that runs today over the same graph.
//
// The two are compared over one compiled document rather than reasoned about, and
// the hand-written reading is called directly: comparing against the path that
// runs would compare the rule with whatever that path did, which for the
// document below is nothing at all.
func TestTheSelfInjectCollisionRuleIsInvokedOverTheCompiledGraph(t *testing.T) {
	t.Run("it admits what the hand-written reading admits", func(t *testing.T) {
		out, catalogLookup := compileSpliced(t, invocationCatalogPath, uncollidingSelfInject)
		coordinates := coordinatesOf(t, out, catalogLookup)

		// The traversal must actually visit something. A subject set that came
		// back empty would report no objection and read exactly like a rule that
		// ran and approved, so the count is measured before the verdict is read.
		visited := 0
		for _, node := range coordinates.Nodes() {
			for _, mechanism := range coordinates.Mechanisms(node.ID) {
				visited += len(coordinates.DeliveryKeys(node.ID, mechanism))
			}
		}
		if visited == 0 {
			t.Fatal("the traversal would visit no delivery key; a verdict from it would certify nothing")
		}

		objections, err := constraint.RunOnNodeMechanismDeliveryKeys(
			nil, coordinates, coordinates, coordinates, out.GraphView,
			renderer.SelfInjectKeyNotDeliveredByAnEdge,
		)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}
		if len(objections) != 0 {
			t.Fatalf("rule raised %d objection(s) on a document carrying no collision: %v", len(objections), objections)
		}
		if twin := renderer.CheckSelfInjectCollisions(
			map[string]map[string]string{"env": {"UI_MODE": "dark"}},
			out.IRGraph.Spec.Edges, "frontend",
		); twin != nil {
			t.Fatalf("the hand-written reading refused a document the rule admitted: %v", twin)
		}
		t.Logf("rule invoked over %d delivery key(s); both readings admit the document", visited)
	})

	t.Run("it refuses what the hand-written reading refuses", func(t *testing.T) {
		catalog := projectedSocketCatalog(t)
		out, catalogLookup := compileSpliced(t, catalog, projectedCollisionSelfInject)
		coordinates := coordinatesOf(t, out, catalogLookup)

		// The connections that collide are the ones an attachment projected, so
		// the compilation must actually carry them. A fixture whose projection
		// stopped arriving would leave both readings with nothing to find and
		// both would agree on silence.
		projected := 0
		for _, edge := range out.IRGraph.Spec.Edges {
			if edge.Source == "frontend" && edge.OriginFeature != "" {
				projected++
			}
		}
		if projected != 2 {
			t.Fatalf("the compilation carries %d projected connection(s) on the front end, want 2", projected)
		}

		objections, err := constraint.RunOnNodeMechanismDeliveryKeys(
			nil, coordinates, coordinates, coordinates, out.GraphView,
			renderer.SelfInjectKeyNotDeliveredByAnEdge,
		)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}
		twin := renderer.CheckSelfInjectCollisions(
			map[string]map[string]string{"env": {"PROJECTED_KEY": "from-self"}},
			out.IRGraph.Spec.Edges, "frontend",
		)

		// Both readings refuse. That is the equivalence being proven; the
		// differences below are differences in how each reports, measured rather
		// than tuned away.
		if len(objections) == 0 {
			t.Fatal("the rule admitted a document the hand-written reading refuses")
		}
		if twin == nil {
			t.Fatal("the hand-written reading admitted the document; the comparison would be vacuous")
		}
		if !errors.Is(twin, renderer.ErrSelfInjectKeyCollision()) {
			t.Errorf("hand-written verdict does not carry the collision condition: %v", twin)
		}
		for _, objection := range objections {
			if !errors.Is(objection.Sentinel(), renderer.ErrSelfInjectKeyCollision()) {
				t.Errorf("objection sentinel = %v, want the collision condition", objection.Sentinel())
			}
		}

		// DIVERGENCE, one: the hand-written reading collapses every colliding
		// connection into one report and the rule raises one per connection. The
		// rule's shape is the deliberate one — an author wiring the same key
		// through two dependencies is told about both — and the collapse is a
		// property of returning a single error.
		if len(objections) != 2 {
			t.Errorf("rule raised %d objection(s), want one per colliding connection (2)", len(objections))
		}

		// DIVERGENCE, two: the sockets reach the reader in different orders. The
		// hand-written reading sorts by (mechanism, key, socket) and reports the
		// first; the rule reports in the order the graph holds the connections,
		// which is the order the traversal invokes it in. On this document the
		// two happen to coincide, so the assertion is on the rule's own order
		// against the graph rather than on the two agreeing.
		var sockets []string
		for _, objection := range objections {
			detail := objection.Detail()
			if len(detail) != 1 {
				t.Fatalf("objection detail = %v, want exactly the socket it collides through", detail)
			}
			sockets = append(sockets, detail[0])
		}
		var graphOrder []string
		for _, edge := range out.IRGraph.Spec.Edges {
			if edge.Source != "frontend" || edge.OriginFeature == "" {
				continue
			}
			graphOrder = append(graphOrder, edge.Socket)
		}
		if !slices.Equal(sockets, graphOrder) {
			t.Errorf("objections name sockets %v; the graph holds the connections as %v", sockets, graphOrder)
		}

		// DIVERGENCE, three: the locator. The rule positions its finding on the
		// authoring key the subject stands for, which is the entry the author
		// edits; the hand-written reading positions its own on the node, having
		// no subject to take a position from.
		for _, objection := range objections {
			if objection.Locator() == "" {
				t.Error("objection carries no locator; it cannot be positioned on the authoring key")
			}
			if objection.SourceFile() == "" {
				t.Error("objection names no source file")
			}
		}
		t.Logf("rule raised %d objection(s) at locator %q in %q, naming sockets %v; the hand-written reading returned one collapsed verdict: %v",
			len(objections), objections[0].Locator(), objections[0].SourceFile(), sockets, twin)

		// DIVERGENCE, four, and the one that motivates the rule: the reading that
		// runs BEFORE the composition finds nothing here. Its own search is the
		// component's own dependency block, which names neither projected
		// connection — so this compilation reached the graph at all, and a
		// collision arriving through an attachment is seen only at this stage.
		t.Log("the pre-composition reading admitted this document: the compilation reached the graph stage")
	})
}

// compileSpliced compiles the fixture workspace with one fragment spliced into
// the front end's declaration, against the catalog given.
//
// An empty fragment compiles the fixture as authored, which is what a proof about
// a component a sub-aggregate contributes wants: the splice would be on the wrong
// document.
func compileSpliced(
	t *testing.T, catalogPath string, fragment string,
) (*stepsOneToFourOutput, resolver.CatalogLookup) {
	t.Helper()
	aggregate := readAggregate(t)
	if fragment != "" {
		aggregate = spliceBytes(t, aggregate, frontEndAnchor, frontEndAnchor+fragment)
	}
	return compileWorkspace(t, workspaceFixture{
		aggregatePath: invocationAggregatePath,
		releasePath:   invocationReleasePath,
		manifestPath:  invocationManifestPath,
		catalogPath:   catalogPath,
	}, aggregate)
}

// moduleInjectCatalog copies the fixture catalog and authors an injection block
// on the component the sub-aggregate contributes, permitting one on that
// component's type so the document passes the readings that gate it.
//
// It copies rather than editing in place because the fixture on disk is shared
// with the package's other proofs and has to keep producing what they expect.
func moduleInjectCatalog(t *testing.T) string {
	t.Helper()
	catalog := copyCatalog(t)
	spliceInFile(t, filepath.Join(catalog, "base/components/postgres.yaml"),
		"spec:\n  schema:\n",
		"spec:\n  inject: [\"env\"]\n  schema:\n")
	spliceInFile(t, filepath.Join(catalog, "base/aggregates/db-module.yaml"),
		"      values:\n",
		"      inject:\n        env:\n          - in: MODULE_KEY\n            value: \"m\"\n      values:\n")
	return catalog
}

// projectedSocketCatalog copies the fixture catalog and declares two dependency
// sockets on the Feature the collision proof attaches, since an attachment may
// only wire a socket the Feature it binds declares.
func projectedSocketCatalog(t *testing.T) string {
	t.Helper()
	catalog := copyCatalog(t)
	spliceInFile(t, filepath.Join(catalog, "base/features/proof-injector.yaml"),
		"spec:\n  schema:\n",
		"spec:\n  usesSchema:\n"+
			"    apis:\n      selector: \"component.aggrega.ai/type=server\"\n      inject: [\"env\"]\n"+
			"    backups:\n      selector: \"component.aggrega.ai/type=server\"\n      inject: [\"env\"]\n"+
			"  schema:\n")
	return catalog
}

// copyCatalog copies the fixture catalog into a directory this test owns.
func copyCatalog(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "catalog")
	copyDir(t, invocationCatalogPath, dst)
	return dst
}

// copyDir copies a directory tree.
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dst, err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read dir %s: %v", src, err)
	}
	for _, entry := range entries {
		from, to := filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			copyDir(t, from, to)
			continue
		}
		content, err := os.ReadFile(from)
		if err != nil {
			t.Fatalf("read %s: %v", from, err)
		}
		if err := os.WriteFile(to, content, 0o644); err != nil {
			t.Fatalf("write %s: %v", to, err)
		}
	}
}

// spliceInFile rewrites one occurrence of anchor in a file this test owns,
// failing when the anchor is absent so a drifted fixture surfaces as a failure
// rather than as a proof that compiled the unspliced document.
func spliceInFile(t *testing.T, path, anchor, replacement string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := os.WriteFile(
		path, spliceBytes(t, content, anchor, replacement), 0o644,
	); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// spliceBytes replaces one occurrence of anchor, failing when it is absent.
func spliceBytes(t *testing.T, content []byte, anchor, replacement string) []byte {
	t.Helper()
	if !bytes.Contains(content, []byte(anchor)) {
		t.Fatalf("the fixture no longer carries %q; the splice would be silently inert", anchor)
	}
	return bytes.Replace(content, []byte(anchor), []byte(replacement), 1)
}
