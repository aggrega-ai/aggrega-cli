// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestBoundaryScopeGuestContextCarriesTheBoundarySetInTheReleaseBucket pins the
// context a boundary-scope FeatureRecipe is handed on both sockets: the
// boundary's own annotation set is the release bucket and the boundary's
// annotations alike, the aggregate and catalog buckets are empty because no
// component or catalog entity stands behind the render, and the parameters and
// boundary identity are the boundary's. A guest reading the well-known keys from
// the release bucket therefore places the render by the boundary's values.
//
// 🔴 PLANTED DEFECT: in buildBoundaryFRRenderCtx (boundary_fr.go) drop the line
// setting the release bucket: the release row below fails, and a boundary-scope
// render loses the boundary's namespace.
func TestBoundaryScopeGuestContextCarriesTheBoundarySetInTheReleaseBucket(t *testing.T) {
	t.Parallel()
	boundaryAnnotations := map[string]types.OrderedMap[string]{
		"platform/zone":  orderedStrings("aggrega.ai/namespace", "boundary-ns", "aggrega.ai/image-registry", "boundary.registry.example"),
		"platform/other": orderedStrings("aggrega.ai/namespace", "other-ns"),
	}
	parameters := map[string]any{"zeta": "last", "alpha": "first"}

	wire, err := json.Marshal(buildBoundaryFRRenderCtx("platform/zone", parameters, boundaryAnnotations))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got struct {
		Parameters  map[string]any               `json:"parameters"`
		Annotations map[string]map[string]string `json:"annotations"`
		Boundary    struct {
			Name        string            `json:"name"`
			Path        []string          `json:"path"`
			Annotations map[string]string `json:"annotations"`
		} `json:"boundary"`
	}
	if err := json.Unmarshal(wire, &got); err != nil {
		t.Fatalf("unmarshal %s: %v", wire, err)
	}

	boundarySet := map[string]string{"aggrega.ai/namespace": "boundary-ns", "aggrega.ai/image-registry": "boundary.registry.example"}
	rows := []struct {
		name string
		got  any
		want any
	}{
		{"release bucket is the boundary's set", got.Annotations["release"], boundarySet},
		{"boundary annotations are the same set", got.Boundary.Annotations, boundarySet},
		{"no aggregate bucket", got.Annotations["aggregate"], map[string]string(nil)},
		{"no catalog bucket", got.Annotations["catalog"], map[string]string(nil)},
		{"boundary name", got.Boundary.Name, "zone"},
		{"boundary path", got.Boundary.Path, []string{"platform", "zone"}},
		{"parameters", got.Parameters, map[string]any{"alpha": "first", "zeta": "last"}},
	}
	for _, row := range rows {
		if !reflect.DeepEqual(row.got, row.want) {
			t.Errorf("%s: got %v, want %v (wire %s)", row.name, row.got, row.want, wire)
		}
	}
	if strings.Contains(string(wire), "yaml_indent") {
		t.Errorf("the builder set the indentation width, which only the provider engine adds: %s", wire)
	}
}

// TestBoundaryScopeGuestContextWithoutAnnotations pins the empty end: a
// boundary that declares no annotation hands both sockets no annotation at all —
// neither bucket nor boundary set — rather than an empty member.
func TestBoundaryScopeGuestContextWithoutAnnotations(t *testing.T) {
	t.Parallel()
	wire, err := json.Marshal(buildBoundaryFRRenderCtx("zone", nil, map[string]types.OrderedMap[string]{}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(wire, []byte(`"annotations":{}`)) || bytes.Contains(wire, []byte(`"release"`)) {
		t.Errorf("an empty boundary produced annotations: %s", wire)
	}
	if bytes.Contains(wire, []byte(`"parameters"`)) {
		t.Errorf("no parameters were fulfilled, yet the context carries some: %s", wire)
	}
}

// TestBoundaryScopeGuestContextIsDeterministic builds the context many times
// from a parameter map Go iterates in a different order each time and requires
// one byte sequence.
func TestBoundaryScopeGuestContextIsDeterministic(t *testing.T) {
	t.Parallel()
	boundaryAnnotations := map[string]types.OrderedMap[string]{
		"zone": orderedStrings("aggrega.ai/namespace", "boundary-ns", "example.org/tier", "gold"),
	}
	parameters := map[string]any{"c": 3, "a": 1, "b": 2, "d": 4}
	first, err := json.Marshal(buildBoundaryFRRenderCtx("zone", parameters, boundaryAnnotations))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for i := 0; i < 50; i++ {
		again, err := json.Marshal(buildBoundaryFRRenderCtx("zone", parameters, boundaryAnnotations))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !bytes.Equal(first, again) {
			t.Fatalf("build %d produced different bytes:\n first: %s\n again: %s", i, first, again)
		}
	}
}
