// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestTheWalkRecordsATopLevelComponentAtThePathItsAuthorWrote holds the walk to
// the path an author wrote for a component written at an Aggregate's top-level
// `.spec.components` surface: the walk reaches it inside the subsystem
// normalization synthesized to hold it, and that subsystem is no key the
// document holds, so the reference sites it records — and the lockfile carries —
// name the top-level path. A subsystem an author declares under the same name is
// a real level and keeps it: the walk keys on the provenance flag, not on the name.
//
// The last row is the refusal an author reads when such a component names an
// entity no catalog holds: it names the same path.
func TestTheWalkRecordsATopLevelComponentAtThePathItsAuthorWrote(t *testing.T) {
	t.Parallel()
	const head = "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: a\n  version: \"1.0.0\"\nspec:\n"
	type pin struct {
		kind           string
		referencedFrom []string
	}
	for _, tc := range []struct {
		name string
		spec string
		want map[string]pin
	}{
		{
			name: "a component written at the top level",
			spec: `  components:
    db:
      type: app/model
      features:
        pw: { type: sec/token }
`,
			want: map[string]pin{
				"app/model": {"component", []string{"aggregate.spec.components.db.type"}},
				"sec/token": {"feature", []string{"aggregate.spec.components.db.features.pw.type"}},
			},
		},
		{
			name: "a subsystem an author declares under the synthesized scope's name",
			spec: `  subsystems:
    default:
      components:
        db:
          type: app/model
          features:
            pw: { type: sec/token }
`,
			want: map[string]pin{
				"app/model": {"component", []string{"aggregate.spec.subsystems.default.components.db.type"}},
				"sec/token": {"feature", []string{"aggregate.spec.subsystems.default.components.db.features.pw.type"}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, err := closure.ComputeClosure(context.Background(), &types.Release{}, nil,
				types.Normalize(loadAggregate(t, head+tc.spec)), workspaceconfig.Default(), connectionLookup())
			if err != nil {
				t.Fatalf("ComputeClosure: %v", err)
			}
			got := map[string]pin{}
			for _, e := range c.Entities {
				got[e.URI] = pin{kind: e.KindHint, referencedFrom: e.ReferencedFrom}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("the walk recorded\n  %v\nwant\n  %v", got, tc.want)
			}
		})
	}

	t.Run("the refusal of an entity no catalog holds names the path the author wrote", func(t *testing.T) {
		t.Parallel()
		agg := loadAggregate(t, head+`  components:
    db:
      type: app/absent
`)
		_, err := closure.ComputeClosure(context.Background(), &types.Release{}, nil,
			types.Normalize(agg), workspaceconfig.Default(), connectionLookup())
		if !errors.Is(err, closure.ErrUnresolvableURI()) {
			t.Fatalf("want the walk to refuse an entity no catalog holds; got %v", err)
		}
		if want := "(referenced from aggregate.spec.components.db.type)"; !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name the path the author wrote %q:\n%v", want, err)
		}
		if strings.Contains(err.Error(), "subsystems.default") {
			t.Errorf("the refusal names a scope the author did not write:\n%v", err)
		}
	})
}
