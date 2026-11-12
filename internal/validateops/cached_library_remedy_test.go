// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validateops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// remedyCacheDir is the artifact cache directory the fixtures below lay a cached
// library into, relative to the workspace root, as the workspace configuration
// resolves it by default.
const remedyCacheDir = ".aggrega/cache"

// remedyConsumer is a Component whose infra pillar composes a schema from the
// Sizing library, so it validates only where that library resolves.
const remedyConsumer = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: example/needs-sizing
  version: 1.0.0
  labels:
    component.aggrega.ai/type: stateful-server
spec:
  schema:
    infra:
      storage:
        $ref: "Sizing#/schemas/Storage"
`

// remedyLibrary is the Sizing library remedyConsumer refers to.
const remedyLibrary = `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: Sizing
  version: 1.0.0
spec:
  schemas:
    Storage:
      type: object
      properties:
        size:
          type: string
          default: 10Gi
`

// cachedLibrarySlot is the cache slot of the Sizing library at 1.0.0 in the
// workspace at ws, as the one builder of a slot's place (oci.CachePath) spells it.
func cachedLibrarySlot(t *testing.T, ws string) string {
	t.Helper()
	slot, err := oci.CachePath(ws, remedyCacheDir, closure.LibraryAxisNamed("SchemaLibraries").Kind, "Sizing:1.0.0", "1.0.0")
	if err != nil {
		t.Fatalf("the fixture library slot: %v", err)
	}
	return slot
}

// remedyWorkspace lays out a workspace whose one catalog directory holds
// remedyConsumer, and, as asked, the Sizing library authored in that directory
// and a copy of it sitting in the artifact cache with no lockfile entry pinning
// it. It returns the workspace root, the catalog directory and the consumer's
// path.
func remedyWorkspace(t *testing.T, authored, cached bool) (ws, catalogDir, consumer string) {
	t.Helper()
	ws = t.TempDir()
	catalogDir = filepath.Join(ws, "catalog")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	consumer = filepath.Join(catalogDir, "component.yaml")
	write(consumer, remedyConsumer)
	if authored {
		write(filepath.Join(catalogDir, "sizing.yaml"), remedyLibrary)
	}
	if cached {
		write(filepath.Join(cachedLibrarySlot(t, ws), "library.yaml"), remedyLibrary)
	}
	return ws, catalogDir, consumer
}

// The words the catalog-directories-only line carries: the library it could not
// use, why, and the two actions that work for a check reading libraries through no
// lockfile entry.
var catalogDirectoriesRemedyWords = []string{
	`SchemaLibrary "Sizing"`,
	"this check reads libraries from catalog directories only, never through a lockfile entry",
	"aggrega pull --release <release>",
	"aggrega validate --release <release>",
	"Author the library under a catalog directory",
}

// TestValidateCatalog_UnpinnedCachedLibraryCarriesRemedy drives the whole-catalog
// check over a catalog whose Component refers to a library only the artifact
// cache holds. The check reads catalog directories only, so it cannot use the
// cached copy; it must name that library, say why it was not used, and point to
// the actions that work for a check that reads no library through a lockfile — and nothing else
// about the failure may move: its condition still matches, and a catalog with
// nothing cached reports exactly as it did.
//
// The line must also END the report the terminal prints, which is the failure
// flattened into its cause chain: a line placed in an outer frame would be
// followed on the same line by the finding it explains.
func TestValidateCatalog_UnpinnedCachedLibraryCarriesRemedy(t *testing.T) {
	tests := []struct {
		name            string
		authored        bool
		cached          bool
		wantErr         bool
		wantRemediation bool
	}{
		{name: "a library only the cache holds is named, with the actions that work here", cached: true, wantErr: true, wantRemediation: true},
		{name: "a library held nowhere reports the failure alone", wantErr: true},
		{name: "a library the catalog directory supplies validates, a cached copy notwithstanding", authored: true, cached: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ws, catalogDir, _ := remedyWorkspace(t, tc.authored, tc.cached)
			_, err := ValidateCatalog(t.Context(), ws, remedyCacheDir, []string{catalogDir}, nil, nil)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("ValidateCatalog: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("a Component referring to a library no catalog directory supplies must fail the whole-catalog check")
			}
			if !errors.Is(err, compactform.ErrUnresolvedSchemaLibraryRef()) {
				t.Errorf("the failure no longer matches the unresolved-library condition: %v", err)
			}
			printed := cerrors.CauseChain(fmt.Errorf("catalog validation failed: %w", err))
			if !strings.Contains(printed, "SchemaLibrary Sizing not found in catalog") {
				t.Errorf("the finding is missing from the report:\n%s", printed)
			}
			if !tc.wantRemediation {
				if strings.Contains(printed, "remediation:") {
					t.Errorf("a remediation line was printed with nothing cached:\n%s", printed)
				}
				return
			}
			for _, w := range catalogDirectoriesRemedyWords {
				if !strings.Contains(printed, w) {
					t.Errorf("the report lacks %q:\n%s", w, printed)
				}
			}
			if strings.Contains(printed, "counts as catalog content only where") {
				t.Errorf("the report says a lockfile entry would make the cached copy count, which this check never reads:\n%s", printed)
			}
			lines := strings.Split(printed, "\n")
			if last := strings.TrimSpace(lines[len(lines)-1]); !strings.HasPrefix(last, "remediation:") ||
				!strings.HasSuffix(last, "which checks the Release against the libraries its lockfile pins.") {
				t.Errorf("the report does not end with the remediation line, so the finding it explains runs on past it:\n%s", printed)
			}
		})
	}
}

// TestValidateEntity_UnpinnedCachedLibraryCarriesRemedy is the same check for
// one entity file, which reads catalog directories only whether or not a Release
// is named. The line must sit in the message of the diagnostic the failure leads
// with — the terminal renders a failure from that diagnostic and nothing above
// it — at the source the finding names, with the failure's own text intact.
func TestValidateEntity_UnpinnedCachedLibraryCarriesRemedy(t *testing.T) {
	tests := []struct {
		name            string
		authored        bool
		cached          bool
		wantErr         bool
		wantRemediation bool
	}{
		{name: "a library only the cache holds is named, with the actions that work here", cached: true, wantErr: true, wantRemediation: true},
		{name: "a library held nowhere reports the failure alone", wantErr: true},
		{name: "a library the catalog directory supplies validates, a cached copy notwithstanding", authored: true, cached: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ws, catalogDir, consumer := remedyWorkspace(t, tc.authored, tc.cached)
			res, err := ValidateEntity(context.Background(), ws, remedyCacheDir, []string{catalogDir}, nil, consumer, false)
			if !tc.wantErr {
				if err != nil || !res.Valid {
					t.Fatalf("ValidateEntity: valid=%v err=%v", res.Valid, err)
				}
				return
			}
			if err == nil {
				t.Fatal("a Component referring to a library no catalog directory supplies must fail its pillar check")
			}
			if !errors.Is(err, compactform.ErrUnresolvedSchemaLibraryRef()) {
				t.Errorf("the failure no longer matches the unresolved-library condition: %v", err)
			}
			var head *cerrors.CompilerError
			if !errors.As(err, &head) || head == nil {
				t.Fatalf("the failure carries no diagnostic to lead with: %v", err)
			}
			if head.Source().File != consumer {
				t.Errorf("the leading diagnostic points at %q, want the entity file %q", head.Source().File, consumer)
			}
			msg := head.Message()
			if !strings.Contains(msg, "SchemaLibrary Sizing not found in catalog") {
				t.Errorf("the leading diagnostic lost the finding:\n%s", msg)
			}
			if !tc.wantRemediation {
				if strings.Contains(msg, "remediation:") {
					t.Errorf("a remediation line was attached with nothing cached:\n%s", msg)
				}
				return
			}
			for _, w := range catalogDirectoriesRemedyWords {
				if !strings.Contains(msg, w) {
					t.Errorf("the leading diagnostic lacks %q:\n%s", w, msg)
				}
			}
		})
	}
}
