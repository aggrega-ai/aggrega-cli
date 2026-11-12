// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipeline

import (
	"os"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// The invocation proofs for the originated-connection axis.
//
// They sit in the internal test package because the enumeration under proof is
// carried on an unexported output, and reaching the production enumeration is
// the whole point: an axis a test assembles for itself proves the test's account
// of the composition rather than the compiler's.

const (
	originatedFixtureDir      = "testdata/delivery-key-origin/"
	originatedFixtureCatalog  = originatedFixtureDir + "catalog/base"
	originatedFixtureManifest = originatedFixtureDir + "manifest.yaml"
)

// originatedCoordinates compiles one of this fixture's workspaces through the
// real pre-render pipeline and returns the enumeration the pipeline itself
// built.
func originatedCoordinates(t *testing.T, variant string) []constraint.OriginatedConnectionInstance {
	t.Helper()
	aggregatePath := originatedFixtureDir + "aggregate-" + variant + ".yaml"
	aggregate, err := os.ReadFile(aggregatePath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	out, _ := compileWorkspace(t, workspaceFixture{
		aggregatePath: aggregatePath,
		releasePath:   originatedFixtureDir + "release-" + variant + ".yaml",
		manifestPath:  originatedFixtureManifest,
		catalogPath:   originatedFixtureCatalog,
	}, aggregate)
	if out.GraphCoordinates == nil {
		t.Fatal("the compilation produced no enumeration of the finalised graph; no rule over it can be invoked")
	}
	// Read through the interface the traversal takes, not through the concrete
	// type: what the rule needs is that the production enumeration SATISFIES the
	// axis, and a concrete-type read would go on compiling if it stopped doing so.
	var enumerator constraint.OriginatedConnectionEnumerator = out.GraphCoordinates
	return enumerator.OriginatedConnections()
}

// TestTheProductionEnumerationSuppliesTheOriginatedConnectionAxis is the
// implemented-in-production proof.
//
// A subject kind is realised when a check can declare it AND something the build
// runs can enumerate it. A kind with an interface, a traversal and only a test
// fixture behind the enumerator reads as complete — every declaration assertion
// is green and the rule compiles — while no build ever invokes it. So this
// compiles a real workspace and takes the enumeration the pipeline itself built,
// never one assembled here.
//
// The workspace draws one connection from each origin the vocabulary admits, so
// a verdict here covers every arm rather than the arm the fixture happened to
// use.
func TestTheProductionEnumerationSuppliesTheOriginatedConnectionAxis(t *testing.T) {
	t.Parallel()

	connections := originatedCoordinates(t, "three-way")
	if len(connections) == 0 {
		t.Fatal("the production enumeration yields no originated connection for a workspace that draws three, " +
			"so a rule keyed by this axis is never invoked and reports exactly what a rule that ran and approved reports")
	}

	// The origin is what the axis adds, so a verdict that never reads it would
	// pass over an enumeration carrying the coordinate and nothing else.
	kinds := map[string]int{}
	for _, connection := range connections {
		kinds[connection.Origin.Kind]++
	}
	if kinds[constraint.OriginComponent] == 0 {
		t.Errorf("no connection carries the component's own dependency block as its origin; saw %v", kinds)
	}
	if kinds[constraint.OriginAttachment] == 0 {
		t.Errorf("no connection carries an attachment as its origin; saw %v", kinds)
	}
}

// TestTheCoordinateSeparatesEveryOriginOnOneSocketAndAlias is the ambiguity
// proof.
//
// The workspace writes ONE socket and ONE alias three times on one node: once in
// the component's own dependency block and once in each of two attachments. A
// coordinate keyed by node, socket and alias names all three at once, so a rule
// over it reads one authored block for all three and reports the other two as
// approved — which is indistinguishable, from the outside, from a rule that
// examined them.
//
// So the assertion is that the three arrive as three coordinates AND that each
// one's own authored delivery comes back through the fact keyed by it. The
// second half is what the first cannot cover: three distinct coordinates that
// all read one payload would satisfy a count.
func TestTheCoordinateSeparatesEveryOriginOnOneSocketAndAlias(t *testing.T) {
	t.Parallel()

	aggregatePath := originatedFixtureDir + "aggregate-three-way.yaml"
	aggregate, err := os.ReadFile(aggregatePath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	out, _ := compileWorkspace(t, workspaceFixture{
		aggregatePath: aggregatePath,
		releasePath:   originatedFixtureDir + "release-three-way.yaml",
		manifestPath:  originatedFixtureManifest,
		catalogPath:   originatedFixtureCatalog,
	}, aggregate)

	if out.GraphCoordinates == nil {
		t.Fatal("the compilation produced no enumeration of the finalised graph")
	}
	var enumerator constraint.OriginatedConnectionEnumerator = out.GraphCoordinates

	// The delivery key each origin wrote, which is what tells the three apart in
	// the authored documents.
	want := map[string]string{
		"component/":        "OWN_HOST",
		"attachment/sb_one": "ONE_HOST",
		"attachment/sb_two": "TWO_PORT",
	}
	seen := map[string]string{}
	for _, connection := range enumerator.OriginatedConnections() {
		if connection.Node.ID != "api" || connection.Socket != "backend" || connection.Alias != "primary" {
			continue
		}
		key := connection.Origin.Kind + "/" + connection.Origin.Attachment
		payloads := out.GraphView.OriginatedConnectionAuthoredDelivery(
			connection.Node.ID, connection.Origin.Kind, connection.Origin.Attachment,
			connection.Socket, connection.Alias, connection.Target,
		)
		if len(payloads) != 1 {
			t.Errorf("origin %s reads %d authored delivery payload(s); want exactly 1 — "+
				"the workspace writes one block per origin", key, len(payloads))
			continue
		}
		env, dictated := payloads[0].Get("env")
		if !dictated {
			t.Errorf("origin %s dictates no env delivery, but its authored block does", key)
			continue
		}
		entries, entriesShaped := env.([]any)
		if !entriesShaped || len(entries) != 1 {
			t.Errorf("origin %s reads an env payload of an unexpected shape %T; the authored block is a "+
				"one-entry sequence", key, env)
			continue
		}
		entry, entryShaped := entries[0].(map[string]any)
		if !entryShaped {
			t.Errorf("origin %s reads an env entry of an unexpected shape %T", key, entries[0])
			continue
		}
		delivered, named := entry["in"].(string)
		if !named {
			t.Errorf("origin %s reads an env entry naming no delivery key", key)
			continue
		}
		seen[key] = delivered
	}

	for key, delivered := range want {
		got, present := seen[key]
		if !present {
			t.Errorf("no coordinate for origin %s on api/backend/primary.\n"+
				"INVARIANT: a connection is enumerated once per surface that drew it.\n"+
				"CONSEQUENCE: an origin absent from the axis is never presented to the rule keyed by it, "+
				"and a rule never asked raises nothing — which reads exactly like a rule that ran and approved.",
				key)
			continue
		}
		if got != delivered {
			t.Errorf("origin %s reads the delivery key %q; want %q — the fact keyed by this coordinate is "+
				"answering with another origin's authored block", key, got, delivered)
		}
	}
	if len(seen) != len(want) {
		t.Errorf("api/backend/primary enumerates %d origin(s); want %d", len(seen), len(want))
	}
}

// TestTheOriginSiteNamesTheAttachmentsOwnBlock is the locator-provenance proof
// at the coordinate level.
//
// The connection under proof is drawn by an attachment declared scope-wide, so
// the consuming component's own dependency block is empty and naming it would
// spell a path the file does not contain. The site has to name the scope's block
// instead, and it can only do so because the reading that settled it retained
// it: the finalised graph records which attachment instance drew the edge and
// never which surface declared that attachment.
func TestTheOriginSiteNamesTheAttachmentsOwnBlock(t *testing.T) {
	t.Parallel()

	for _, connection := range originatedCoordinates(t, "scope-contributed") {
		if connection.Origin.Kind != constraint.OriginAttachment {
			continue
		}
		if connection.Origin.Site.ScopePath != "tier" {
			t.Errorf("the attachment origin names the scope %q; want %q — the attachment is declared on that "+
				"scope's own block, which is the one place anyone can edit it",
				connection.Origin.Site.ScopePath, "tier")
		}
		if connection.Origin.Site.Component != "" {
			t.Errorf("the attachment origin names the component %q; a scope-wide block names no component, "+
				"and spelling one would point at a key the file does not contain",
				connection.Origin.Site.Component)
		}
		if connection.Origin.Document != constraint.AuthoringDocumentAggregate {
			t.Errorf("the attachment origin names the document %q; want %q — the scope nesting the site path "+
				"is spelled with follows from it",
				connection.Origin.Document, constraint.AuthoringDocumentAggregate)
		}
		return
	}
	t.Fatal("the production enumeration yields no attachment-originated connection for a workspace whose only " +
		"connection an attachment drew")
}
