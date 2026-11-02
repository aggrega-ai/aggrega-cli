// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// resolveOrderRuns is how many times each row below drives the walk, and it is
// chosen so that a walk resolving in map order cannot pass by luck.
//
// A false pass needs every run to land on the order the sorted walk produces. Each
// row records exactly two references, and records the one that sorts first SECOND.
// The Go runtime holds a map of at most eight entries, none deleted, in one group of
// eight slots filled in insertion order, and starts a range over it at a uniformly
// random slot and wraps, so the second key comes first only when that start is
// slot 1: one run in eight. Twenty runs then pass a map-ordered walk with
// probability 8^-20, about 10^-18, per row. Were the runtime to shuffle two keys
// uniformly instead, a map-ordered walk would still pass with probability 2^-20,
// about 10^-6, per row. The order the fixture records its references in is
// deliberate: reversed, a map-ordered walk would agree with the sorted one seven
// runs in eight, and twenty runs would pass it about seven times in a hundred.
const resolveOrderRuns = 20

// shadowNoticeMarker is the text the catalog's shadow notice carries beside the
// file it names, so a line counted below is that notice and not some other line
// naming the same file.
const shadowNoticeMarker = "is shadowed (first-win precedence)"

const orderComponentDoc = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/zeta
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`

const orderFeatureDoc = `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: test/alpha
  version: "1.0.0"
spec: {}
`

// TestTheWalkResolvesReferencesInAnOrderTheInputFixes holds what a resolution
// shows its reader to the input alone: driven again and again over one catalog, by
// one binary, it prints one sequence of catalog notices and refuses naming one
// reference.
//
// Both depend on which reference is resolved first. The catalog reads its
// Components and its Features the first time a reference of each kind is looked
// up, and announces a file an earlier catalog directory shadows as it reads it, so
// the kind resolved first is the kind whose notices come first. And the walk stops
// at the first reference that does not resolve, so with two missing, the one it
// tries first is the one the refusal names. The walk resolves in URI order, so the
// Feature (test/alpha) is announced before the Component (test/zeta), and of the two
// missing Features the refusal names test/aa-absent.
//
// Each run opens a fresh catalog view, as each command does: a view announces a
// file once for its life, so a view kept across runs would print nothing after the
// first. The runs are sequential; what varies between them is only the order the
// runtime would range a map in.
func TestTheWalkResolvesReferencesInAnOrderTheInputFixes(t *testing.T) {
	ws := t.TempDir()
	for rel, body := range map[string]string{
		"a/component.yaml": orderComponentDoc,
		"b/component.yaml": orderComponentDoc,
		"a/feature.yaml":   orderFeatureDoc,
		"b/feature.yaml":   orderFeatureDoc,
	} {
		path := filepath.Join(ws, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	dirs := []string{filepath.Join(ws, "a"), filepath.Join(ws, "b")}
	shadowed := []struct{ path, label string }{
		{filepath.Join(ws, "b", "feature.yaml"), "Feature test/alpha"},
		{filepath.Join(ws, "b", "component.yaml"), "Component test/zeta"},
	}

	cases := []struct {
		name string
		// release and aggregate are the documents walked, the aggregate left empty
		// where a row walks none. Each row records two references, the one that sorts
		// first recorded second (see resolveOrderRuns).
		release   string
		aggregate string
		// observe reads what one run shows its reader that the resolve order decides.
		observe func(t *testing.T, notices string, err error) string
		want    string
	}{
		{
			name: "the shadow notices a resolution prints",
			// The Aggregate is walked before the Release's attachments, so the
			// Component is recorded first and the Feature second.
			aggregate: `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: app
  version: 1.0.0
spec:
  components:
    api:
      type: test/zeta:1.0.0
`,
			release: `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: prod
  version: 1.0.0
spec:
  boundaries:
    primary:
      components:
        api:
          features:
            edge:
              type: test/alpha:1.0.0
`,
			observe: func(t *testing.T, notices string, err error) string {
				t.Helper()
				if err != nil {
					t.Fatalf("both references are in the catalog, so the walk resolves them; got: %v", err)
				}
				var seen []string
				for _, line := range strings.Split(notices, "\n") {
					if !strings.Contains(line, shadowNoticeMarker) {
						continue
					}
					for _, s := range shadowed {
						if strings.Contains(line, s.path) {
							seen = append(seen, s.label)
						}
					}
				}
				return strings.Join(seen, ", then ")
			},
			want: "Feature test/alpha, then Component test/zeta",
		},
		{
			name: "the reference a refusal names when two are missing",
			// Attachments are recorded in the order they are written, so
			// test/zz-absent is recorded first and test/aa-absent second.
			release: `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: prod
  version: 1.0.0
spec:
  boundaries:
    primary:
      components:
        api:
          features:
            first:
              type: test/zz-absent:1.0.0
            second:
              type: test/aa-absent:1.0.0
`,
			observe: func(t *testing.T, _ string, err error) string {
				t.Helper()
				if !errors.Is(err, closure.ErrUnresolvableURI()) {
					t.Fatalf("neither reference is in the catalog, so the walk refuses as unresolvable; got: %v", err)
				}
				var unresolved *closure.UnresolvedEntityError
				if !errors.As(err, &unresolved) {
					t.Fatalf("the refusal carries the reference it names; got: %v", err)
				}
				return unresolved.URI
			},
			want: "test/aa-absent:1.0.0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rel := loadRelease(t, tc.release)
			agg := types.NormalizedAggregate{}
			if tc.aggregate != "" {
				agg = types.Normalize(loadAggregate(t, tc.aggregate))
			}
			for run := 1; run <= resolveOrderRuns; run++ {
				// The view writes its notices where the surface directs them, as a
				// command directs them to its error stream.
				var notices strings.Builder
				view := catalog.NewLazyLookup(ws, ".aggrega/cache", dirs, nil)
				view.WriteNoticesTo(&notices)
				lookup := view.ClosureLookup(nil, ws, ".aggrega/cache", nil, nil, nil)
				_, err := closure.ComputeClosure(context.Background(), rel, nil, agg, workspaceconfig.Config{}, lookup)
				if got := tc.observe(t, notices.String(), err); got != tc.want {
					t.Fatalf("run %d of %d: got %q; want %q on every run\nnotices:\n%s", run, resolveOrderRuns, got, tc.want, notices.String())
				}
			}
		})
	}
}
