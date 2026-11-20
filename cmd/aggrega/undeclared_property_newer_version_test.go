// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// An authored property the selected version of a Component does not declare halts
// with the schema-conformance condition. Where a held version the instance's
// constraints exclude declares the property, the halt's detail also names that
// version and the constraints that hold the instance below it, so the author sees
// that a newer version would accept the property and what to change to reach it.

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

const heldBackCaption = `held at "1.0.0"`

// TestUndeclaredPropertyNamesNewerVersion holds the schema-conformance halt over
// an undeclared infra property to naming the newer version that declares it.
func TestUndeclaredPropertyNamesNewerVersion(t *testing.T) {
	t.Parallel()

	// conformanceHalt validates the quickstart with the Release's web entry
	// carrying property under infra and returns the text of the
	// schema-conformance findings.
	conformanceHalt := func(t *testing.T, property string) string {
		t.Helper()
		root := instanceVersionsWorkspace(t, "quickstart-web")
		replaceInFixture(t, filepath.Join(root, "release.yaml"),
			"        port: 8080\n", "        port: 8080\n        "+property+": 18080\n")

		resolved, vr := instanceVersionsCoreValidate(t, root)
		var halts []string
		for _, f := range coreFindings(resolved, vr) {
			if errors.Is(f, resolver.ErrSchemaConformance()) {
				halts = append(halts, f.Error())
			}
		}
		if len(halts) == 0 {
			t.Fatal("the validation reports no schema-conformance finding")
		}
		return strings.Join(halts, "\n")
	}

	t.Run("a property only a newer held version declares names that version and the constraints", func(t *testing.T) {
		t.Parallel()
		halt := conformanceHalt(t, "published_port")

		for _, want := range []string{
			`unexpected property: \"published_port\"`,
			`declared by "core/compute/server:1.1.0"`,
			heldBackCaption,
			`Manifest mapping key "core/compute/server:1.0.0"`,
			`written in "manifest.yaml"`,
			`mapped to recipe "compose/server:1.0.0"`,
			`whose spec.implements is "core/compute/server:1.0.0"`,
		} {
			if !strings.Contains(halt, want) {
				t.Errorf("the halt does not contain %q:\n%s", want, halt)
			}
		}
		if strings.Count(halt, "core/compute/server:1.1.0") != 1 {
			t.Errorf("the halt names the newer version more than once:\n%s", halt)
		}
	})

	t.Run("a property no held version declares keeps the halt's own detail", func(t *testing.T) {
		t.Parallel()
		halt := conformanceHalt(t, "no_such_property")

		if !strings.Contains(halt, `unexpected property: \"no_such_property\"`) {
			t.Errorf("the halt does not name the undeclared property:\n%s", halt)
		}
		for _, absent := range []string{"declared by", heldBackCaption, "core/compute/server:1.1.0"} {
			if strings.Contains(halt, absent) {
				t.Errorf("the halt carries %q though no held version declares the property:\n%s", absent, halt)
			}
		}
	})
}
