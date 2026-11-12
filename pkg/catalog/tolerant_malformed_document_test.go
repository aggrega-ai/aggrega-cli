// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// malformedAggregaDocument declares the Aggrega envelope on a line of its own
// and then fails to parse, so its header cannot be unmarshalled at all.
const malformedAggregaDocument = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata: [
  name: acc/broken
  version: "1.0.0"
`

// malformedForeignDocument fails to parse just as badly, and declares an
// apiVersion that is not Aggrega's: it is no catalog entity, malformed or not.
const malformedForeignDocument = `apiVersion: v1
kind: ConfigMap
metadata: [
  name: not-an-aggrega-document
`

// cleanComponentDocument is the positive control: a document the walk indexes,
// so a walk that recorded nothing because it read nothing cannot pass.
const cleanComponentDocument = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: acc/clean
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  inject: ["env"]
  schema:
    provides:
      host: { type: string, required: true }
`

// TestTolerantLoad_RecordsAMalformedAggregaDocumentAndNoForeignYAML proves that
// both tolerant catalog backings record a document too malformed for its header
// to unmarshal when its text declares the Aggrega envelope, and record nothing
// for a malformed document declaring some other apiVersion beside it.
//
// The record is what a resolution failure's note lists, so without it the note
// never names the file an author has to open. The recorded skip carries no
// identity, because none can be read from such a document, so it is listed by
// path and never singled out as the cause of a failure. The strict load is held
// to passing over the same directory, as it does for every input it accepts.
func TestTolerantLoad_RecordsAMalformedAggregaDocumentAndNoForeignYAML(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, "catalog")
	broken := filepath.Join(dir, "component", "broken", "component.yaml")
	stray := filepath.Join(dir, "component", "broken", "stray-values.yaml")
	clean := filepath.Join(dir, "component", "clean", "component.yaml")
	for path, body := range map[string]string{
		broken: malformedAggregaDocument,
		stray:  malformedForeignDocument,
		clean:  cleanComponentDocument,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name string
		load func(t *testing.T) []catalog.SkippedEntity
	}{
		{
			name: "eager tolerant load",
			load: func(t *testing.T) []catalog.SkippedEntity {
				c, skipped, err := catalog.LoadPassOneTolerant(ws, "", dir)
				if err != nil {
					t.Fatalf("the tolerant load must not halt: %v", err)
				}
				if _, ok := c.Components["acc/clean:1.0.0"]; !ok {
					t.Fatal("positive control: the clean Component must be indexed")
				}
				return skipped
			},
		},
		{
			name: "lazy lookup",
			load: func(t *testing.T) []catalog.SkippedEntity {
				ll := catalog.NewLazyLookup(ws, filepath.Join(ws, "cache"), []string{dir}, nil)
				if _, ok := ll.Component("acc/clean:1.0.0"); !ok {
					t.Fatal("positive control: the clean Component must resolve")
				}
				return ll.SkippedEntities()
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			skipped := tc.load(t)
			for _, s := range skipped {
				if s.Path == stray {
					t.Errorf("a malformed document declaring no Aggrega apiVersion must not be recorded; got %+v", s)
				}
			}
			if len(skipped) != 1 || skipped[0].Path != broken {
				t.Fatalf("want exactly one skip, for %s; got %+v", broken, skipped)
			}
			s := skipped[0]
			if !errors.Is(s.Err, resolver.ErrYAMLLoad()) {
				t.Errorf("the skip must carry the unloadable-document condition; got %v", s.Err)
			}
			if !strings.Contains(s.Err.Error(), "did not find expected") {
				t.Errorf("the skip must carry the parser's own message; got %q", s.Err.Error())
			}
			if s.Kind != "" || s.Name != "" || s.Version != "" || s.Capability != "" {
				t.Errorf("no identity can be read from the document, so none may be recorded; got %+v", s)
			}
		})
	}

	t.Run("strict load", func(t *testing.T) {
		c, err := catalog.LoadAll(ws, dir)
		if err != nil {
			t.Fatalf("the strict load passes over a document whose header does not unmarshal, and must go on doing so: %v", err)
		}
		if _, ok := c.Components["acc/clean:1.0.0"]; !ok {
			t.Fatal("positive control: the clean Component must be indexed")
		}
	})
}
