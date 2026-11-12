// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipeline

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
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

	backEndAnchor = "    backend:\n      type: core/compute/api-server\n"

	// Two keys under the mechanism the front end's type admits, plus one under a
	// mechanism that type admits only once the catalog copy below adds it. The
	// shape matters to the enumeration proof: the axis has to hold more than one
	// key, more than one mechanism and more than one component, or an axis
	// narrowed to a single subject would satisfy every assertion over it.
	frontEndTwoMechanismSelfInject = "      inject:\n" +
		"        env:\n" +
		"          - in: UI_MODE\n" +
		"            value: \"dark\"\n" +
		"          - in: UI_THEME\n" +
		"            value: \"slate\"\n" +
		"        ssm_param:\n" +
		"          - in: DB_ARN\n" +
		"            value: \"param/db\"\n"

	// One key on a second component, so the enumerated set spans more than one
	// component. Without it every subject sits on one node, and a rule invoked
	// per node would be indistinguishable from one invoked per key.
	backEndSelfInject = "      inject:\n" +
		"        env:\n" +
		"          - in: LOG_LEVEL\n" +
		"            value: \"debug\"\n"

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
// stage whose presentation is built from the Aggregate's own documents, so it
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

// TestTheSelfInjectCollisionRuleIsInvokedOverTheCompiledGraph reads the rule's
// verdict off the production path — the pre-render phases as the build runs
// them — on a document it admits and on a document it refuses.
//
// The verdict is read from the analysis the phases publish rather than from a
// traversal this test drives, because that is the whole claim: the rule is asked
// during a compilation, and what it raises reaches the slice the build halts on.
// A traversal driven from here would prove the rule computes the right answer
// and say nothing about whether anything asks it.
//
// The reading this replaced ran inside the render walk and returned the first
// collision it found for one component. It is gone, and both differences it made
// are asserted below rather than described: the position moves from the
// component identifier alone to the authored key, and the count moves from one
// to one per delivering connection.
func TestTheSelfInjectCollisionRuleIsInvokedOverTheCompiledGraph(t *testing.T) {
	t.Run("a document carrying no collision reaches the render", func(t *testing.T) {
		out, _ := compileSpliced(t, invocationCatalogPath, uncollidingSelfInject)

		// The axis must actually visit something. A subject set that came back
		// empty would raise nothing and read exactly like a rule that ran and
		// approved, so the count is measured before the verdict is read.
		visited := 0
		for _, node := range out.GraphCoordinates.Nodes() {
			for _, mechanism := range out.GraphCoordinates.Mechanisms(node.ID) {
				visited += len(out.GraphCoordinates.DeliveryKeys(node.ID, mechanism))
			}
		}
		if visited == 0 {
			t.Fatal("the axis holds no delivery key; a verdict over it would certify nothing")
		}
		if len(out.Analysis.Errors) != 0 {
			t.Fatalf("the phases reported %d finding(s) on a document carrying no collision: %v",
				len(out.Analysis.Errors), out.Analysis.Errors)
		}
		t.Logf("rule asked about %d delivery key(s) during the compilation; the document is admitted", visited)
	})

	t.Run("a key a projected connection also delivers halts the compilation before the render", func(t *testing.T) {
		catalog := projectedSocketCatalog(t)
		out, _ := compileSpliced(t, catalog, projectedCollisionSelfInject)

		// The connections that collide are the ones an attachment projected, so
		// the compilation must actually carry them. A fixture whose projection
		// stopped arriving would leave the rule with nothing to find, and the
		// silence would read as a rule that ran and approved.
		projected := 0
		for _, edge := range out.IRGraph.Spec.Edges {
			if edge.Source == "frontend" && edge.OriginFeature != "" {
				projected++
			}
		}
		if projected != 2 {
			t.Fatalf("the compilation carries %d projected connection(s) on the front end, want 2", projected)
		}

		// The fixture trips two rules and not one, because the two projected
		// connections deliver the same key into one node as well as duplicating
		// the key the component declared for itself. That second pairing is a
		// separate rule's subject — the one over what a component's connections
		// deliver between them — and it raises one finding per connection at the
		// connection's own delivery entry. Its findings are partitioned off here
		// rather than removed from the fixture: the projection is what puts this
		// rule's own collision beyond the reading that runs before the
		// composition, so narrowing the fixture to avoid the sibling would take
		// the proof with it.
		var selfInject, betweenConnections []analyzer.AnalysisError
		for _, finding := range out.Analysis.Errors {
			if errors.Is(finding.Sentinel, renderer.ErrSelfInjectKeyCollision()) {
				selfInject = append(selfInject, finding)
				continue
			}
			betweenConnections = append(betweenConnections, finding)
		}

		// One finding per delivering connection, which is the count the reading
		// this replaced could not produce: it returned the first collision and
		// stopped. Asserted by count so the change is pinned rather than noted.
		if len(selfInject) != projected {
			t.Fatalf("the phases reported %d finding(s) for this rule, want one per colliding connection (%d): %v",
				len(selfInject), projected, out.Analysis.Errors)
		}
		// The sibling's own count, asserted here so a fixture that stopped
		// tripping it — or started raising something else again — is caught by
		// this proof rather than passing as a partition that happened to be
		// whatever size it was.
		if len(betweenConnections) != projected {
			t.Errorf("the phases reported %d finding(s) outside this rule, want one per connection (%d): %v",
				len(betweenConnections), projected, betweenConnections)
		}
		for i, finding := range betweenConnections {
			if !errors.Is(finding.Sentinel, resolver.ErrInjectKeyCollision()) {
				t.Errorf("finding %d outside this rule carries %v, want the between-connections condition",
					i, finding.Sentinel)
			}
		}

		var sockets, messages []string
		for i, finding := range selfInject {
			// The position the reading this replaced could not reach: it named the
			// component identifier in the file slot and carried no locator at all,
			// so an editor placed every one of its findings at the head of the
			// document.
			if finding.Field != ".spec.components.frontend.inject.env.PROJECTED_KEY" {
				t.Errorf("finding %d is positioned at %q, want the authored key", i, finding.Field)
			}
			if finding.OriginFile != invocationAggregatePath {
				t.Errorf("finding %d names document %q, want %q", i, finding.OriginFile, invocationAggregatePath)
			}
			sockets = append(sockets, socketNamedBy(t, finding.Message))
			messages = append(messages, finding.Message)
		}

		// The order the findings reach the reader is the order the graph holds the
		// connections, which is the order the traversal invokes the rule in.
		var graphOrder []string
		for _, edge := range out.IRGraph.Spec.Edges {
			if edge.Source != "frontend" || edge.OriginFeature == "" {
				continue
			}
			graphOrder = append(graphOrder, edge.Socket)
		}
		if !slices.Equal(sockets, graphOrder) {
			t.Errorf("findings name sockets %v; the graph holds the connections as %v", sockets, graphOrder)
		}

		// Each message whole, spelled out from the graph rather than read back out
		// of the message it checks, so nothing here is compared against itself.
		//
		// It is the whole string and not the values in it because the rule names two
		// and what an author needs is to tell them apart: the delivery key and the
		// socket are both bare words, so two quoted words side by side read as one
		// kind of thing and leave a reader unable to act. A check reading each value
		// out separately passes on exactly that message, which is why the caption
		// and the punctuation around it are asserted rather than the values alone.
		wantMessages := make([]string, 0, len(graphOrder))
		for _, socket := range graphOrder {
			wantMessages = append(wantMessages,
				`duplicate delivery key across self-inject and uses-inject `+
					`("PROJECTED_KEY", socket "`+socket+`")`)
		}
		if !slices.Equal(messages, wantMessages) {
			t.Errorf("the findings read\n  %s\nwant\n  %s",
				strings.Join(messages, "\n  "), strings.Join(wantMessages, "\n  "))
		}
		t.Logf("the compilation reported %d finding(s) at %q in %q, naming sockets %v",
			len(out.Analysis.Errors), out.Analysis.Errors[0].Field, out.Analysis.Errors[0].OriginFile, sockets)
	})
}

// TestTheSelfInjectCollisionRuleIsAskedAboutEveryAuthoredDeliveryKey is the
// named enumeration assertion for the axis the rule is invoked over.
//
// It exists because an agreement between two readings cannot see a narrowed
// axis: a narrower axis asks every reading about fewer subjects, they agree on
// the ones they are asked about, and nothing is raised — which is exactly what a
// satisfied rule looks like. So the subjects are recorded as the traversal
// invokes the rule and compared against the document as authored, by identity
// rather than by count alone.
//
// The recording check stands in for the rule, and the enumeration it walks is
// the one the compilation itself built and handed to the real invocation, so
// what is measured here is the set that invocation is asked about.
func TestTheSelfInjectCollisionRuleIsAskedAboutEveryAuthoredDeliveryKey(t *testing.T) {
	out, _ := compileWith(t,
		customMechanismCatalog(t),
		splice{anchor: frontEndAnchor, addition: frontEndTwoMechanismSelfInject},
		splice{anchor: backEndAnchor, addition: backEndSelfInject},
	)

	var asked []string
	recorder := constraint.Check[constraint.GraphView, constraint.NodeMechanismDeliveryKeySubject]{
		Sentinel: renderer.ErrSelfInjectKeyCollision(),
		Severity: constraint.SeverityFatal,
		Run: func(
			_ constraint.GraphView, s constraint.NodeMechanismDeliveryKeySubject,
		) []constraint.Objection {
			asked = append(asked, s.NodeID()+" "+s.MechanismName()+" "+s.DeliveryKey())
			return nil
		},
	}
	if _, err := constraint.RunOnNodeMechanismDeliveryKeys(
		nil, out.GraphCoordinates, out.GraphCoordinates, out.GraphCoordinates,
		out.GraphView, recorder,
	); err != nil {
		t.Fatalf("traversal: %v", err)
	}

	// The two guards that stop this assertion passing on an axis narrow enough to
	// be vacuous. Both are fatal: a one-subject axis and an axis whose every
	// subject sits on one component each satisfy an identity comparison against
	// themselves while proving nothing about the rule's reach.
	if len(asked) <= 1 {
		t.Fatalf("the rule was asked about %d subject(s); an axis this narrow satisfies any assertion over it: %v",
			len(asked), asked)
	}
	components := map[string]struct{}{}
	mechanisms := map[string]struct{}{}
	for _, subject := range asked {
		parts := strings.Fields(subject)
		components[parts[0]] = struct{}{}
		mechanisms[parts[1]] = struct{}{}
	}
	if len(components) <= 1 {
		t.Fatalf("every subject sits on one component (%v); a rule invoked once per component would look identical", asked)
	}
	if len(mechanisms) <= 1 {
		t.Fatalf("every subject sits on one mechanism (%v); a rule invoked once per component would look identical", asked)
	}

	// The identity of the set, written from the document rather than from the
	// axis, so an axis that stopped presenting one of these fails here.
	want := []string{
		"backend env LOG_LEVEL",
		"frontend env UI_MODE",
		"frontend env UI_THEME",
		"frontend ssm_param DB_ARN",
	}
	sorted := slices.Clone(asked)
	slices.Sort(sorted)
	if !slices.Equal(sorted, want) {
		t.Errorf("the rule was asked about %v, want exactly the keys the document declares %v", sorted, want)
	}

	// A mechanism outside the three the block names as fields is presented on the
	// same footing as those three. It is asserted here because it is the only
	// place the property is now observable: the reading this rule replaced took
	// the resolved keys of every mechanism, custom ones included, and the
	// equivalent claim about the axis is what carries that coverage forward.
	if got := out.GraphCoordinates.Mechanisms("frontend"); !slices.Equal(got, []string{"env", "ssm_param"}) {
		t.Errorf("Mechanisms(%q) = %v, want the field mechanism then the custom one", "frontend", got)
	}
	if got := out.GraphCoordinates.DeliveryKeys("frontend", "ssm_param"); !slices.Equal(got, []string{"DB_ARN"}) {
		t.Errorf("DeliveryKeys(%q, %q) = %v, want the key the custom mechanism delivers", "frontend", "ssm_param", got)
	}
	t.Logf("the rule was asked about %d subject(s) across %d component(s) and %d mechanism(s): %v",
		len(asked), len(components), len(mechanisms), asked)

	t.Run("a withdrawn signal stops the walk partway rather than at the end", func(t *testing.T) {
		// The property is that the walk STOPS, not that an error comes back
		// afterwards, and the two are indistinguishable from the returned error
		// alone. So the subjects are counted: the walk must cover the components up
		// to and including the first one carrying a subject, and none after it.
		nodes := out.GraphCoordinates.Nodes()
		total := 0
		for _, node := range nodes {
			for _, mechanism := range out.GraphCoordinates.Mechanisms(node.ID) {
				total += len(out.GraphCoordinates.DeliveryKeys(node.ID, mechanism))
			}
		}

		var reachable []string
		stopAfter := 0
		for i, node := range nodes {
			for _, mechanism := range out.GraphCoordinates.Mechanisms(node.ID) {
				for _, key := range out.GraphCoordinates.DeliveryKeys(node.ID, mechanism) {
					reachable = append(reachable, node.ID+" "+mechanism+" "+key)
				}
			}
			if len(reachable) > 0 {
				stopAfter = i
				break
			}
		}
		if len(reachable) == 0 || len(reachable) >= total {
			t.Fatalf("the walk would cover %d of %d subject(s) before the signal turns; a stop here proves nothing",
				len(reachable), total)
		}

		reads := 0
		withdrawn := constraint.Cancellation(func() error {
			reads++
			if reads > stopAfter+1 {
				return context.Canceled
			}
			return nil
		})

		var visited []string
		counting := constraint.Check[constraint.GraphView, constraint.NodeMechanismDeliveryKeySubject]{
			Sentinel: renderer.ErrSelfInjectKeyCollision(),
			Severity: constraint.SeverityFatal,
			Run: func(
				_ constraint.GraphView, s constraint.NodeMechanismDeliveryKeySubject,
			) []constraint.Objection {
				visited = append(visited, s.NodeID()+" "+s.MechanismName()+" "+s.DeliveryKey())
				return nil
			},
		}
		objections, err := constraint.RunOnNodeMechanismDeliveryKeys(
			withdrawn, out.GraphCoordinates, out.GraphCoordinates, out.GraphCoordinates,
			out.GraphView, counting,
		)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("walk returned %v, want the withdrawn signal's own error", err)
		}
		if len(objections) != 0 {
			t.Errorf("a withdrawn walk returned %d objection(s); a truncated set reads as a complete verdict",
				len(objections))
		}
		if !slices.Equal(visited, reachable) {
			t.Errorf("the walk visited %v, want it to stop after the first component carrying a subject %v",
				visited, reachable)
		}
		t.Logf("the walk visited %d of %d subject(s) and read the signal %d time(s) before stopping",
			len(visited), total, reads)
	})
}

// socketNamedBy reads the socket a finding's message quotes.
//
// The message is composed from the condition's registered text and the values the
// rule supplied, each quoted. The rule supplies two — the delivery key and the
// socket that also delivers it — so the socket is found by the caption ahead of
// it rather than by position, and reading it back is how the order the findings
// arrive in is compared against the graph's.
//
// The caption is spelled here rather than read from the rule, deliberately. A
// helper that took the word from the code it checks would agree with any word the
// code came to use, including none, and the message reaching an author unlabelled
// is exactly the failure this reads for.
func socketNamedBy(t *testing.T, message string) string {
	t.Helper()
	const caption = `socket "`
	at := strings.Index(message, caption)
	if at < 0 {
		t.Fatalf("finding %q names no socket; it cannot say which connection it collides through, "+
			"and an unlabelled value leaves an author unable to tell it from the delivery key "+
			"quoted beside it", message)
	}
	rest := message[at+len(caption):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		t.Fatalf("finding %q has an unterminated quoted value", message)
	}
	return rest[:end]
}

// customMechanismCatalog copies the fixture catalog and admits one mechanism
// outside the three the injection block names as fields on the front end's type,
// since a component may only deliver through a mechanism its type admits.
func customMechanismCatalog(t *testing.T) string {
	t.Helper()
	catalog := copyCatalog(t)
	spliceInFile(t, filepath.Join(catalog, "base/components/web-ui.yaml"),
		"  inject: [\"env\"]\n",
		"  inject: [\"env\", \"ssm_param\"]\n")
	return catalog
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
	if fragment == "" {
		return compileWith(t, catalogPath)
	}
	return compileWith(t, catalogPath, splice{anchor: frontEndAnchor, addition: fragment})
}

// splice is one addition to the fixture, named by the anchor it is written after.
type splice struct {
	anchor   string
	addition string
}

// compileWith compiles the fixture workspace with each addition written after
// its anchor, against the catalog given.
//
// It takes a set rather than one fragment because a proof about how many
// subjects a rule is asked about needs them spread across more than one
// component, and a single-anchor splice can only reach one.
func compileWith(
	t *testing.T, catalogPath string, splices ...splice,
) (*stepsOneToFourOutput, resolver.CatalogLookup) {
	t.Helper()
	aggregate := readAggregate(t)
	for _, s := range splices {
		aggregate = spliceBytes(t, aggregate, s.anchor, s.anchor+s.addition)
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
