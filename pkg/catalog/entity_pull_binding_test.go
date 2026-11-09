// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"context"
	"os"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestEntityReference_BindsLocationPerKind asserts the bound OCI reference for
// every core catalog kind equals `<registry>/<OCILocation(kind, …)>` — the
// pull-side mirror of the push location. It fails if the kind segment, the
// recipe provider-scoping, or the lowercasing regresses. This is the binding the
// generic Pull path lacked: a fake puller never opens a remote repository, so a
// missing name→location binding was invisible until exercised against a real
// registry; this pins it at the unit level.
func TestEntityReference_BindsLocationPerKind(t *testing.T) {
	const reg = "127.0.0.1:5000"
	cases := []struct {
		name, kind, identity, want string
	}{
		{"component", "Component", "core/datastore/mariadb", reg + "/component/core/datastore/mariadb"},
		// A secret-type reference resolves to a Feature entity; it binds at feature/<name>.
		{"feature (secret-origin)", "Feature", "core/secret-value", reg + "/feature/core/secret-value"},
		{"recipe (provider-scoped)", "Recipe", "compose/mariadb", reg + "/recipe/compose/mariadb"},
		{"feature_recipe (provider-scoped)", "FeatureRecipe", "compose/dotenv-secret", reg + "/featurerecipe/compose/dotenv-secret"},
		{"provider", "Provider", "compose", reg + "/provider/compose"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := entityReference(reg, c.kind, c.identity)
			if err != nil {
				t.Fatalf("entityReference(%q, %q, %q) unexpected error: %v", reg, c.kind, c.identity, err)
			}
			if got != c.want {
				t.Errorf("entityReference(%q, %q, %q) = %q; want %q", reg, c.kind, c.identity, got, c.want)
			}
		})
	}
}

// recordingEntityPuller is a Pull-and-EntityPull puller that records the
// (identity, kind, version) tier3PullCatalogEntity binds, so a test can assert
// the entity Tier-3 path routes through the name→location binding rather than
// the bare-reference legacy path.
type recordingEntityPuller struct {
	identity, kind, version string
	usedLegacy              bool
}

// The bodies are real entity documents of the kind each path fetches, because the
// registry tier now puts a fetched body through its kind's schema before caching
// it. The test asserts only which method bound the reference, so the documents are
// the minimum each kind's schema accepts.
const recordingLegacyComponent = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/x
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec: {}
`

const recordingBoundFeatureRecipe = `apiVersion: aggrega.ai/v1beta1
kind: FeatureRecipe
metadata:
  name: dotenv-secret
  version: "1.0.0"
spec:
  capability: compose
  implements: compose/dotenv-secret:1.0.0
  workload: |
    text: secret
`

func (p *recordingEntityPuller) Pull(_ context.Context, reference, tag string) (*oci.PullResult, error) {
	p.usedLegacy = true
	p.identity, p.version = reference, tag
	return &oci.PullResult{Bytes: []byte(recordingLegacyComponent), MediaType: oci.MediaTypeComponent}, nil
}

func (p *recordingEntityPuller) PullEntity(_ context.Context, identity, kind, floorVersion string) (*oci.PullResult, string, error) {
	p.identity, p.kind, p.version = identity, kind, floorVersion
	mt, _ := oci.MediaTypeFor("FeatureRecipe", false)
	return &oci.PullResult{Bytes: []byte(recordingBoundFeatureRecipe), MediaType: mt}, floorVersion, nil
}

// TestTier3PullCatalogEntity_RoutesThroughEntityPuller asserts that, given a
// non-empty kind and an EntityPuller, the entity Tier-3 pull binds via PullEntity
// (carrying the bare identity + kind) rather than the legacy bare-reference path
// — and that an empty kind falls back to the legacy path.
func TestTier3PullCatalogEntity_RoutesThroughEntityPuller(t *testing.T) {
	dir := t.TempDir()

	t.Run("bound when kind known", func(t *testing.T) {
		p := &recordingEntityPuller{}
		_, _, err := tier3PullCatalogEntity(context.Background(), dir, ".aggrega/cache", p, "compose/dotenv-secret:1.0.0", "FeatureRecipe")
		if err != nil {
			t.Fatalf("tier3PullCatalogEntity: %v", err)
		}
		if p.usedLegacy {
			t.Fatalf("used legacy Pull path; want EntityPuller binding")
		}
		if p.identity != "compose/dotenv-secret" || p.kind != "FeatureRecipe" || p.version != "1.0.0" {
			t.Errorf("PullEntity got (identity=%q, kind=%q, version=%q); want (compose/dotenv-secret, FeatureRecipe, 1.0.0)",
				p.identity, p.kind, p.version)
		}
	})

	t.Run("legacy when slot kind empty", func(t *testing.T) {
		p := &recordingEntityPuller{}
		_, _, err := tier3PullCatalogEntity(context.Background(), dir, ".aggrega/cache", p, "hub.aggrega.ai/component/core/x:1.0.0", "")
		if err != nil {
			t.Fatalf("tier3PullCatalogEntity: %v", err)
		}
		if !p.usedLegacy {
			t.Fatalf("used EntityPuller binding; want legacy Pull for an empty kind / located URI")
		}
	})
}

// locatedDocumentPuller serves one document from the already-located reference
// path, whatever the reference.
type locatedDocumentPuller struct {
	body, mediaType string
}

func (p locatedDocumentPuller) Pull(context.Context, string, string) (*oci.PullResult, error) {
	return &oci.PullResult{Bytes: []byte(p.body), MediaType: p.mediaType}, nil
}

const locatedAggregate = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: core/x
  version: "1.0.0"
spec:
  components:
    app:
      type: core/web
`

// TestAFetchIsCachedOnlyUnderTheSlotKindItsReaderNamed pulls an already-located
// reference through the registry tier and reads which cache slots it wrote. The
// slot is the one the reader named — the published kind it keeps the reference
// under — and never one chosen by the kind the served document declares: an
// Aggregate read as one lands in the Aggregate slot; a reference whose kind its
// reader did not know writes no slot, though the Component served for it declares a
// kind; and a Component served for a reference read as an Aggregate is refused by
// the Aggregate schema, so nothing is cached for it under either kind.
func TestAFetchIsCachedOnlyUnderTheSlotKindItsReaderNamed(t *testing.T) {
	t.Parallel()
	const (
		cacheDir = ".aggrega/cache"
		uri      = "hub.aggrega.ai/entity/core/x:1.0.0"
	)
	for _, tc := range []struct {
		name, slotKind, body, mediaType string
		wantRefusal                     bool
		wantSlot                        string // the one kind a slot exists under, "" for none
	}{
		{"an Aggregate read as one", "Aggregate", locatedAggregate, oci.MediaTypeAggregate, false, "Aggregate"},
		{"a Component read as no kind", "", recordingLegacyComponent, oci.MediaTypeComponent, false, ""},
		{"a Component read as an Aggregate", "Aggregate", recordingLegacyComponent, oci.MediaTypeComponent, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ws := t.TempDir()
			p := locatedDocumentPuller{body: tc.body, mediaType: tc.mediaType}
			_, res, err := tier3PullCatalogEntity(t.Context(), ws, cacheDir, p, uri, tc.slotKind)
			if (err != nil) != tc.wantRefusal {
				t.Fatalf("tier3PullCatalogEntity err = %v; want refusal %v", err, tc.wantRefusal)
			}
			if err == nil && res.NotFound {
				t.Fatalf("the served document was not resolved")
			}
			for _, kind := range []string{"Aggregate", "Component"} {
				slot, perr := oci.CachePath(ws, cacheDir, kind, uri, "1.0.0")
				if perr != nil {
					t.Fatalf("CachePath(%s): %v", kind, perr)
				}
				_, serr := os.Stat(slot)
				if exists, want := serr == nil, kind == tc.wantSlot; exists != want {
					t.Errorf("a slot under %s exists = %v, want %v", kind, exists, want)
				}
			}
		})
	}
}
