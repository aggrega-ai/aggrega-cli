// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package topology

import (
	"fmt"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestFlattenAggregate_ModuleExportsAtEveryDepth pins what flattening records
// about the export lists of a composition two modules deep: the root imports
// `outer`, which imports `inner`; `inner` exports `store` as `db`, and `outer`
// re-exports `inner.db` as `reexported`.
//
// Two properties are held. Every module's declared aliases are recorded on its
// declaration, which is what a rule about the export list reads. And the alias
// table the composition ends with holds the aliases of the nested module beside
// those of the module the root imports, so a connection `outer`'s own document
// draws to `inner.db` resolves rather than naming nothing.
func TestFlattenAggregate_ModuleExportsAtEveryDepth(t *testing.T) {
	t.Parallel()

	innerComponents := types.OrderedMap[types.ComponentInstance]{}
	innerComponents.Set("store", types.ComponentInstance{Type: "core/db"})
	innerExports := types.OrderedMap[string]{}
	innerExports.Set("db", "store")
	inner := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "inner-module"},
		Spec:     types.AggregateSpec{Components: innerComponents, ExportedComponents: innerExports},
	}

	outerComponents := types.OrderedMap[types.ComponentInstance]{}
	outerComponents.Set("user", types.ComponentInstance{Type: "core/consumer"})
	outerImports := types.OrderedMap[types.SubAggregate]{}
	outerImports.Set("inner", types.SubAggregate{Type: "inner-module"})
	outerExports := types.OrderedMap[string]{}
	outerExports.Set("reexported", "inner.db")
	outer := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "outer-module"},
		Spec: types.AggregateSpec{
			Components: outerComponents, Aggregates: outerImports, ExportedComponents: outerExports,
		},
	}

	rootComponents := types.OrderedMap[types.ComponentInstance]{}
	rootComponents.Set("app", types.ComponentInstance{Type: "core/consumer"})
	rootImports := types.OrderedMap[types.SubAggregate]{}
	rootImports.Set("outer", types.SubAggregate{Type: "outer-module"})
	root := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "root"},
		Spec:     types.AggregateSpec{Components: rootComponents, Aggregates: rootImports},
	}

	loader := func(typeName string, _ map[string]any) (*types.Aggregate, string, map[string]any, error) {
		switch typeName {
		case "outer-module":
			return outer, "", nil, nil
		case "inner-module":
			return inner, "", nil, nil
		}
		return nil, "", nil, fmt.Errorf("unknown: %s", typeName)
	}

	result, err := flattenAggregate(types.Normalize(root), "", "", "", loader, map[string]bool{}, 0, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for alias, want := range map[string]string{
		"outer.reexported": "outer.inner.store",
		"outer.inner.db":   "outer.inner.store",
	} {
		if got, ok := result.ExportedComponents.Get(alias); !ok || got != want {
			t.Errorf("alias %q resolves to %q (present %v); want %q — the table holds %v",
				alias, got, ok, want, result.ExportedComponents.Keys())
		}
	}

	declared := map[string][]string{}
	for _, m := range result.Modules {
		declared[m.Path] = m.Exports
	}
	for path, want := range map[string][]string{
		"outer":       {"reexported"},
		"outer.inner": {"db"},
	} {
		if got, ok := declared[path]; !ok || !slices.Equal(got, want) {
			t.Errorf("module %q records exports %v (recorded %v); want %v", path, got, ok, want)
		}
	}
}
