// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// TestIndexedResolutionPathWithoutARecord pins the default the eager catalog and
// every other construction rely on: a catalog carrying no record of where its
// entries came from answers every entry as catalog-resolved, which is what an index
// built from catalog directories alone is.
func TestIndexedResolutionPathWithoutARecord(t *testing.T) {
	t.Parallel()
	var c Catalog
	if got := c.indexedResolutionPath("Plugin", "pipeline-step/any:1.0.0"); got != closure.ResolutionPathCatalog {
		t.Errorf("nil record: got %q; want %q", got, closure.ResolutionPathCatalog)
	}
	c.origins = map[string]entityOrigin{
		entityBytesKey("Plugin", "pipeline-step/any:1.0.0"): pinnedOrigin("lockfile-pinned:pipeline-step/any:1.0.0", "/cache/slot", ""),
	}
	if got := c.indexedResolutionPath("Plugin", "pipeline-step/any:1.0.0"); got != closure.ResolutionPathCache {
		t.Errorf("recorded entry: got %q; want %q", got, closure.ResolutionPathCache)
	}
	if got := c.indexedResolutionPath("Recipe", "pipeline-step/any:1.0.0"); got != closure.ResolutionPathCatalog {
		t.Errorf("same key under another kind: got %q; want %q — the record is per kind", got, closure.ResolutionPathCatalog)
	}
}

// TestOneRecordAnswersEveryProvenanceQuestion holds the shell's provenance answers to
// the one record they are derived from. For each shape the record can take — none at
// all, an authored document, a pinned slot with the document its parse anchored at,
// and a pinned slot with none — every answer is asked of the same identity: the tier a
// closure walk reports, the document a Recipe is reported as parsed at, the source a
// flattened Aggregate records, and whether the entity has a document an author owns.
//
// The rows with no record carry the eager catalog's own indexes instead, so the table
// also pins that an identity the record holds nothing for keeps the answer its catalog
// gives without one; an authored record beside a conflicting eager index pins that the
// record wins wherever it exists.
func TestOneRecordAnswersEveryProvenanceQuestion(t *testing.T) {
	t.Parallel()
	const (
		recipeKey    = "compose/svc:1.0.0"
		aggregateKey = "example/app"
		authoredDoc  = "/ws/catalog/recipe/compose/svc/1.0.0/recipe.yaml"
		layoutDoc    = "/ws/.aggrega/cache/entities/slot/recipe.yaml"
		slot         = "/ws/.aggrega/cache/entities/slot"
		label        = "lockfile-pinned:compose/svc:1.0.0"
		eagerPath    = "/ws/catalog/eager.yaml"
	)
	rows := []struct {
		name       string
		origin     *entityOrigin
		eagerIndex bool
		wantTier   string
		wantDoc    string // entityDocument; "" = none recorded
		wantSource string // aggregateSource; "" = none recorded
		wantHome   string // authoredDocument; "" = no writable home
	}{
		{name: "no record, no index", wantTier: closure.ResolutionPathCatalog},
		{name: "no record, the eager catalog's own index", eagerIndex: true,
			wantTier: closure.ResolutionPathCatalog, wantDoc: eagerPath, wantSource: eagerPath},
		{name: "authored document", origin: ptrOrigin(authoredOrigin(authoredDoc)),
			wantTier: closure.ResolutionPathCatalog, wantDoc: authoredDoc, wantSource: authoredDoc, wantHome: authoredDoc},
		{name: "authored document beside a conflicting index", origin: ptrOrigin(authoredOrigin(authoredDoc)), eagerIndex: true,
			wantTier: closure.ResolutionPathCatalog, wantDoc: authoredDoc, wantSource: authoredDoc, wantHome: authoredDoc},
		{name: "pinned slot with a layout document", origin: ptrOrigin(pinnedOrigin(label, slot, layoutDoc)),
			wantTier: closure.ResolutionPathCache, wantDoc: layoutDoc, wantSource: label},
		{name: "pinned slot with no layout document", origin: ptrOrigin(pinnedOrigin(label, slot, "")),
			wantTier: closure.ResolutionPathCache, wantSource: label},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			var c Catalog
			if row.origin != nil {
				c.origins = map[string]entityOrigin{
					entityBytesKey("Recipe", recipeKey):       *row.origin,
					entityBytesKey("Aggregate", aggregateKey): *row.origin,
				}
			}
			if row.eagerIndex {
				c.pathByIdentity = map[string]string{entityBytesKey("Recipe", recipeKey): eagerPath}
				c.aggregateSources = map[string]string{aggregateKey: eagerPath}
			}
			if got := c.indexedResolutionPath("Recipe", recipeKey); got != row.wantTier {
				t.Errorf("tier: got %q; want %q", got, row.wantTier)
			}
			if got := c.indexedResolutionPath("Aggregate", aggregateKey); got != row.wantTier {
				t.Errorf("aggregate tier: got %q; want %q", got, row.wantTier)
			}
			gotDoc, ok := c.entityDocument("Recipe", "compose", "svc", "1.0.0")
			if ok != (row.wantDoc != "") || gotDoc != row.wantDoc {
				t.Errorf("entityDocument: got (%q, %v); want %q", gotDoc, ok, row.wantDoc)
			}
			gotSource, ok := c.aggregateSource(aggregateKey)
			if ok != (row.wantSource != "") || gotSource != row.wantSource {
				t.Errorf("aggregateSource: got (%q, %v); want %q", gotSource, ok, row.wantSource)
			}
			if row.origin != nil {
				gotHome, ok := row.origin.authoredDocument()
				if ok != (row.wantHome != "") || gotHome != row.wantHome {
					t.Errorf("authoredDocument: got (%q, %v); want %q — a pinned slot is never a writable home", gotHome, ok, row.wantHome)
				}
			}
		})
	}
}

func ptrOrigin(o entityOrigin) *entityOrigin { return &o }
