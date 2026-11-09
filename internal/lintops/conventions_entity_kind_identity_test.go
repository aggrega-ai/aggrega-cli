// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// TestTheConventionsWalkLintsAComponentOnlyForAnEntityCollectedAsOne holds the
// conventions walk to the kind each closure entity was collected as. The catalog
// holds a Component core/web; the closure reaches core/web:1.0.0 once, collected as
// the kind each row names. Collected as a Component, or with no kind, the walk hands
// the Component's file to the rules. Collected as the Release's Manifest or as a
// plugin, the closure reached another entity that shares the name, so the Component
// — which the Release never used — is not linted on its behalf.
func TestTheConventionsWalkLintsAComponentOnlyForAnEntityCollectedAsOne(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		hint   string
		linted bool
	}{
		{hint: closure.KindComponent, linted: true},
		{hint: "", linted: true},
		{hint: closure.KindManifest, linted: false},
		{hint: closure.KindPlugin, linted: false},
	} {
		t.Run("collected as "+tc.hint, func(t *testing.T) {
			t.Parallel()
			ws := t.TempDir()
			path := filepath.Join(ws, "web.yaml")
			mustWriteFile(t, path, `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/web
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port: { type: integer }
`)
			index := map[string]string{"Component|core/web": path}
			cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: "core/web:1.0.0", KindHint: tc.hint}}}

			got := closureEntityFiles(catalog.NewLazyLookup(ws, "", []string{ws}, nil), cl, index)
			want := []string{}
			if tc.linted {
				want = []string{path}
			}
			if !reflect.DeepEqual(append([]string{}, got...), want) {
				t.Errorf("files handed to the conventions rules = %v; want %v", got, want)
			}
		})
	}
}
