// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import (
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// The authored-site assertion: every locator a traversal produces is walked
// through the document it is attributed to, key by key.
//
// It is a different instrument from the provenance transcript beside it, and the
// difference is the whole point. A transcript compares a produced locator against
// a string written in the same file by the same hand, so a locator and its
// expectation are wrong together whenever the author of both held one wrong idea
// of what the authored document looks like — and a flattened scope path is exactly
// that shape: ".spec.boundaries.prod/edge" reads as a locator, and a transcript
// asserting it passes forever. This walks the locator through a document instead,
// so the document is the oracle and no expectation string stands between them.
//
// The documents below are the authored counterparts of the traversal fixture: one
// per source file the fixture attributes a coordinate to. They are written in the
// authoring shapes the schemas declare — a subsystem block nesting under
// "subsystems" again, a boundary block nesting under "boundaries" again, a Recipe
// pipeline as a list of entries carrying an "id" — because a walk over a document
// that is not shaped like the real one proves nothing about the real one.

// authoredDocuments maps each source file the traversal fixture names to the
// document an author would have written to produce that fixture's coordinates.
//
// A file the fixture attributes a locator to and this map has no entry for is a
// failure rather than a skip: a walk that silently passes over the document it
// cannot find reports a clean result over a corpus it never read.
func authoredDocuments() map[string]string {
	return map[string]string{
		// A components-only Aggregate. Its single component is the one normalization
		// folds into a synthesized container, which is why the fixture's web node
		// carries a synthesized subsystem path and still names the top-level site.
		//
		// Both authoring forms of a dependency socket appear, because a socket's value
		// is either an alias-keyed mapping of connections or a sequence of target
		// names, and a locator over a connection names a key in only one of them. The
		// "zebra" socket carries two connections, which is the shape a per-connection
		// locator has to descend into; "alpha" carries the sequence form, where the
		// alias is the target's own name and appears in no key.
		//
		// It also imports one module. A module is written as an entry under
		// `aggregates` naming a type and nothing about the module's contents, so the
		// site a locator over that module names is this one key — the whole of what
		// this document says about it.
		"aggregate.yaml": `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: storefront
spec:
  parameters:
    db_size:
      type: string
  aggregates:
    data:
      type: modules/data-layer:1.0.0
  components:
    web:
      type: core/service:1.0.0
      uses:
        zebra:
          primary:
            target: cache
          replica:
            target: cache-follower
        alpha:
          - queue
      features:
        tls:
          uses:
            issuer:
              ca:
                target: ca
          values:
            app:
              issuer: internal
        logging: {}
      inject:
        env:
          DB_HOST: "{{ .context.inject.db.host }}"
          DB_PORT: "{{ .context.inject.db.port }}"
        file: {}
      values:
        app:
          replicas: 2
          image: storefront/web
`,
		// A subsystems Aggregate. The scope the fixture enumerates as
		// "billing.payments" is written as two nested blocks, each under the
		// container's own key, which is the shape the locator has to name.
		//
		// This document is also the one that imports the module the fixture
		// enumerates as "billing.ledger", and the contrast with the scope above is
		// what the pair is here to show. A nested SCOPE spells both its levels in
		// this one file; a nested MODULE spells only its own level here, because
		// the outer level was spelled in the document that imported this one. So
		// the module's key is a bare `ledger` and a locator naming
		// ".spec.aggregates.billing.aggregates.ledger" would resolve in no file at
		// all.
		"billing/aggregate.yaml": `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: billing
spec:
  parameters:
    region:
      type: string
  aggregates:
    ledger:
      type: modules/ledger:1.0.0
  subsystems:
    web: {}
    billing:
      components:
        api:
          type: core/service:1.0.0
          uses:
            db:
              - store
      subsystems:
        payments:
          features:
            # A scope-wide attachment that wires a socket of its own. A
            # connection it draws is authored HERE and reaches every component
            # the scope holds, so its locator names this block and never the
            # consuming component's dependency block — which is what the
            # carried-connection coordinates are walked against.
            tls:
              uses:
                issuer:
                  ca:
                    target: ca
            audit: {}
`,
		// A Release whose boundaries nest. The fixture enumerates the inner one as
		// "prod/edge", and the document spells it as "edge" inside "prod" under the
		// container's own key — the two levels a single spliced segment would lose.
		//
		// The inner scope carries both path fields, because the path-template axis
		// positions a finding at the scope's own key inside this document and a
		// scope writing neither would leave that locator unresolved against any
		// real file.
		//
		// It is the boundaried one of the deployment's two component-routing forms.
		// The other is below, and the two are separate documents because the schema
		// refuses one document carrying both: a deployment either names boundaries
		// or lists components at its own spec level, never both.
		"release.yaml": `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: storefront-prod
spec:
  aggregate: storefront
  boundaries:
    prod:
      boundaries:
        edge:
          directory: "regions/{{ .boundary_name }}"
          subDirectory: "{{ .component_id }}"
          features:
            shared-db: {}
            audit: {}
          # The routed one of a boundary's two component-naming axes. A whole
          # sub-module is placed here, and a component inside it is named under
          # the LOCAL name it carries within that module — so the id it addresses,
          # "billing.api", appears nowhere in this file and the keys a locator
          # names are these.
          aggregates:
            billing:
              components:
                api: {}
          components:
            # The deployment's own edge surface, wiring a socket the application
            # declared and left unbound. A connection drawn here is authored on
            # the deployment, so its locator names this entry.
            web:
              uses:
                zebra:
                  spare:
                    target: cache-standby
            # The direct axis naming a component a sub-module contributed, which
            # this document has to spell with that module's dotted prefix. The
            # key therefore carries the locator dialect's separator, so a locator
            # over it names this block and stops — which is what the walk
            # confirms, and why the entry sits here rather than being implied.
            data.reader: {}
    dev:
      components:
        web: {}
`,
		// A deployment written in the other component-routing form: components at
		// the spec level, for a single-scope deployment naming no boundaries. It is
		// what the attachment-pillar locator is walked through, because that
		// locator names the component's own site and this is the form that spells
		// one there.
		//
		// The attachment it carries holds an infra pillar and no values pillar,
		// which is not an omission but the authoring rule: the infra pillar is the
		// operator's and this is the only surface whose attachment block admits
		// one, while the values beside it are the developer's and are written in
		// the application document above. One attachment, two pillars, two
		// documents — which is what the pillar axis carries a file for.
		"release-flat.yaml": `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: storefront-single-scope
spec:
  aggregate: storefront
  components:
    web:
      features:
        tls:
          infra:
            pool_size: 20
`,
		// It carries a values mapping and a step input as well as the blocks the
		// other node-rooted axes walk, because the template-field axis reaches every
		// inline template a Recipe writes: a top-level field, a field inside a keyed
		// block whose key travels in the label, and an artifacts entry whose key is an
		// output filename and therefore carries the locator dialect's separator.
		"recipes/web.yaml": `
apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: web
spec:
  valuesMapping: "replicaCount: 2"
  locals:
    fullname: "{{ .context.instance_id }}"
  provides:
    host: "{{ .locals.fullname }}"
  pipeline:
    - id: render
      plugin: core/renderer:1.0.0
      function: mutate
      with: "marker: web"
  artifacts:
    values-yaml: "replicas: 2"
`,
		"recipes/api.yaml": `
apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: api
spec:
  locals:
    prefix: "{{ .context.instance_id }}"
`,
		// Its artifacts block carries an entry whose authored key holds the locator
		// dialect's separator, which is the ordinary shape on this axis: an inline
		// entry's key is its output filename. So a locator over the entry names this
		// block and stops, and that is what the walk below confirms resolves.
		//
		// Its locals block is written in the order the fixture enumerates it and not
		// alphabetically, because author order is the contract on that axis: the
		// second entry reads the first, which is legal precisely because the first
		// stands above it in this document.
		"featurerecipes/tls.yaml": `
apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: tls
spec:
  locals:
    issuer: "{{ .values.app.issuer }}"
    fullname: "{{ .attachment_name }}-{{ .locals.issuer }}"
  pipeline:
    - id: stamp
      plugin: core/stamper:1.0.0
      function: mutate
  artifacts:
    values.yaml: "replicas: 2"
`,
		// The second attachment on the same node, carrying an artifacts entry that
		// resolves to the SAME output name as the first one's, and a local under the
		// SAME key. Nothing declares either unique across documents, so this is
		// ordinary authoring rather than a contrived collision — and it is what makes
		// the attachment coordinate load-bearing on both axes.
		"featurerecipes/logging.yaml": `
apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: logging
spec:
  locals:
    fullname: "{{ .context.instance_id }}-logs"
  pipeline:
    - id: stamp
      plugin: core/stamper:1.0.0
      function: mutate
  artifacts:
    values.yaml: "level: info"
`,
		// The boundary-scope attachment. Its locals block is authored here, in the
		// FeatureRecipe, which is why a locator over one of its entries names this
		// document's own spec path and carries no boundary key: the boundary is where
		// the attachment was declared, not where the block was written.
		//
		// It carries a template-bearing field at the spec's top level as well as one
		// inside a keyed block, because the template-field axis positions at both
		// shapes and a document holding only one of them would leave the other's
		// locator unresolved against any real file.
		"featurerecipes/shared-db.yaml": `
apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: shared-db
spec:
  valuesMapping: |
    name: {{ .boundary.boundary_name }}
  locals:
    endpoint: "{{ .boundary.path }}-db"
  provides:
    dsn: "postgres://{{ .locals.endpoint }}"
  pipeline:
    - id: annotate
      plugin: core/annotator:1.0.0
      function: mutate
  artifacts:
    dashboard.json: "{}"
`,
	}
}

// locatorNamesAnUnwrittenSite names the kinds whose traversal contract is to point
// at a site the author has not necessarily written yet, together with the reason.
//
// It is a named list rather than a tolerance in the walk, because the difference
// between "this kind may name an absent key" and "this locator happens to be
// absent" is the difference between a contract and a defect. A kind earns an entry
// only when its own traversal documentation says the locator names where the
// author would write the thing, and the entry records that.
var locatorNamesAnUnwrittenSite = map[SubjectKind]string{
	// A slot is declared by the component's type and is named whether or not it is
	// filled, so an unfilled slot's locator points at the attachment entry that
	// would fill it. That is the diagnostic's purpose: the reader is being told
	// where to write something that is not there.
	SubjectNodeSlot: "a slot's locator names the attachment entry that would fill it, filled or not",
}

// TestEveryProducedLocatorResolvesInItsAuthoredDocument is the assertion that no
// locator names a key its document does not contain.
//
// It drives the same table the provenance transcript drives, so it covers every
// enumerated kind by construction and gains each new kind without being edited —
// a hand-listed subset here would read as exhaustive while omitting exactly the
// kind a change broke.
func TestEveryProducedLocatorResolvesInItsAuthoredDocument(t *testing.T) {
	t.Parallel()

	documents := parseAuthoredDocuments(t)
	fixture := newTraversalFixture()

	// The subtests are sequential on purpose. The coverage count below is read
	// after the loop, and a parallel subtest is deferred until the parent returns —
	// so a parallel table would report a zero taken before anything ran, which is
	// the failure this count exists to catch reported against itself.
	var walked int
	exercised := map[SubjectKind]bool{}
	for _, tc := range traversalCases() {
		t.Run(string(tc.kind), func(t *testing.T) {
			objections, err := tc.run(nil, fixture, traversalView{})
			if err != nil {
				t.Fatalf("traversal returned %v; the uncancelled walk returns no error", err)
			}
			for _, o := range objections {
				if o.Locator() == "" {
					continue
				}
				document, ok := documents[o.SourceFile()]
				if !ok {
					t.Fatalf("locator %q is attributed to %q, which no authored document stands for.\n"+
						"INVARIANT: every locator is walked through the document it names.\n"+
						"CONSEQUENCE: a file with no document here is skipped, and a walk that skips is "+
						"a clean result over a corpus it never read.",
						o.Locator(), o.SourceFile())
				}
				walked++
				err := resolveAuthoredLocator(document, o.Locator())
				if err == nil {
					continue
				}
				if reason, exempt := locatorNamesAnUnwrittenSite[tc.kind]; exempt {
					exercised[tc.kind] = true
					t.Logf("locator %q does not resolve, which this kind's contract allows: %s",
						o.Locator(), reason)
					continue
				}
				t.Errorf("locator %q does not resolve in %q: %v.\n"+
					"INVARIANT: a locator names a key the author's own document contains.\n"+
					"CONSEQUENCE: a diagnostic carrying it points a reader, or an editor, at a "+
					"position that is not in the file.",
					o.Locator(), o.SourceFile(), err)
			}
		})
	}

	if walked == 0 {
		t.Error("no locator was walked; the assertion passed over an empty corpus, which certifies nothing")
	}
	// A tolerance nothing needs is one nobody notices going wrong. Where a kind's
	// entry stops being exercised — because the fixture filled the site, or because
	// the traversal stopped naming it — the entry has to go rather than sit as a
	// standing permission for a defect this walk would otherwise catch.
	for kind, reason := range locatorNamesAnUnwrittenSite {
		if !exercised[kind] {
			t.Errorf("kind %s is allowed to name an unwritten site (%s), and every locator it produced "+
				"over this fixture resolved. The entry no longer stands for anything and permits a real "+
				"miss to pass unread; remove it, or give the fixture a case that needs it.",
				kind, reason)
		}
	}
	t.Logf("walked %d locators through %d authored documents across %d kinds",
		walked, len(documents), len(traversalCases()))
}

// TestTheAuthoredSiteWalkRejectsAFlattenedScopePath is the calibration: it plants
// the defect the walk exists to catch and requires the walk to fail on it.
//
// Without it a walk that resolved everything — because it descended nothing, or
// because a document happened to carry the key by another route — would report the
// same clean result as a correct one.
func TestTheAuthoredSiteWalkRejectsAFlattenedScopePath(t *testing.T) {
	t.Parallel()

	documents := parseAuthoredDocuments(t)

	cases := []struct {
		name      string
		file      string
		flattened string
		expressed string
	}{
		{
			name:      "a boundary path spliced as one segment",
			file:      "release.yaml",
			flattened: ".spec.boundaries.prod/edge.features.shared-db",
			expressed: ".spec.boundaries.prod.boundaries.edge.features.shared-db",
		},
		{
			name:      "a subsystem path spliced as one segment",
			file:      "billing/aggregate.yaml",
			flattened: ".spec.subsystems.billing.payments.features.tls",
			expressed: ".spec.subsystems.billing.subsystems.payments.features.tls",
		},
		{
			name:      "a boundary path split on the other axis's separator",
			file:      "release.yaml",
			flattened: ".spec.boundaries.prod.boundaries.prod/edge",
			expressed: ".spec.boundaries.prod.boundaries.edge",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := resolveAuthoredLocator(documents[tc.file], tc.flattened); err == nil {
				t.Errorf("the flattened locator %q resolves in %q; the walk cannot distinguish a "+
					"flattened scope path from an expressed one, so its clean results prove nothing",
					tc.flattened, tc.file)
			}
			if err := resolveAuthoredLocator(documents[tc.file], tc.expressed); err != nil {
				t.Errorf("the expressed locator %q does not resolve in %q: %v; the control is aimed at "+
					"a document that does not hold the site, so the failing case above is silent for "+
					"the wrong reason",
					tc.expressed, tc.file, err)
			}
		})
	}
}

func parseAuthoredDocuments(t *testing.T) map[string]any {
	t.Helper()

	out := make(map[string]any, len(authoredDocuments()))
	for file, text := range authoredDocuments() {
		var document any
		if err := yaml.Unmarshal([]byte(text), &document); err != nil {
			t.Fatalf("the authored document for %q does not parse: %v", file, err)
		}
		out[file] = document
	}
	return out
}

// resolveAuthoredLocator walks locator through document and reports the first
// segment that is not there.
//
// Two node shapes are walked, because the authoring surfaces use both. A mapping
// is descended by key. A sequence whose entries carry an identifier — a Recipe or
// FeatureRecipe pipeline — is descended by matching that identifier, which is what
// the pipeline locator names: an entry's declared id rather than its position,
// because a position moves when a step is inserted above it.
func resolveAuthoredLocator(document any, locator string) error {
	current := document
	for _, segment := range strings.Split(strings.TrimPrefix(locator, "."), ".") {
		next, err := descendAuthoredSite(current, segment)
		if err != nil {
			return err
		}
		current = next
	}
	return nil
}

func descendAuthoredSite(current any, segment string) (any, error) {
	switch node := current.(type) {
	case map[string]any:
		value, ok := node[segment]
		if !ok {
			return nil, &authoredSiteMiss{segment: segment, present: mappingKeys(node)}
		}
		return value, nil
	case []any:
		for _, entry := range node {
			mapping, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			if id, ok := mapping["id"].(string); ok && id == segment {
				return mapping, nil
			}
		}
		return nil, &authoredSiteMiss{segment: segment, present: sequenceIDs(node)}
	default:
		return nil, &authoredSiteMiss{segment: segment}
	}
}

type authoredSiteMiss struct {
	segment string
	present []string
}

func (e *authoredSiteMiss) Error() string {
	if len(e.present) == 0 {
		return "no key " + e.segment + " — the site above it holds no keys at all"
	}
	return "no key " + e.segment + " beside " + strings.Join(e.present, ", ")
}

// mappingKeys sorts, because the only consumer is a failure message and a message
// whose key order changes per run is one a reader cannot diff against the last.
func mappingKeys(node map[string]any) []string {
	out := make([]string, 0, len(node))
	for key := range node {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func sequenceIDs(node []any) []string {
	out := make([]string, 0, len(node))
	for _, entry := range node {
		if mapping, ok := entry.(map[string]any); ok {
			if id, ok := mapping["id"].(string); ok {
				out = append(out, id)
			}
		}
	}
	return out
}
