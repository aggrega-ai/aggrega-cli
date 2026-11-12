// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// End-to-end proof that an omits entry built from a value an author declared
// sensitive reaches its provider marked, through the real pipeline, and that an
// entry built from plain values reaches it unmarked.
//
// The mark is read from the render input the provider RECEIVED, not from anything
// the compiler holds: the provider is a guest built from source in this module,
// whose describe() claims to honour omit entries and which, when handed entries,
// writes one more file saying, for each entry in the order it arrived, whether the
// input marked it. The file names no field of any entry, so the proof reads the
// mark without the value it protects ever being written.

package compiler_test

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmfixture"
)

// omitsMarkReport is the name the guest gives the file it writes its account of the
// received marks to, as its own source spells it. The build writes the file under
// a name ending in it, prefixed by the attachment that rendered it.
const omitsMarkReport = "received-omits.txt"

// TestOmitsSensitiveMark_ABoundaryScopeEntryReadingASensitiveValueReachesTheProviderMarked
// builds one Release whose boundary-scope FeatureRecipe declares two omits entries:
// the first's name reads an infra value its Feature declares sensitive, the second's
// an infra value it declares plainly. The guest rendering the FeatureRecipe reports
// the first entry marked and the second not — the second is the control that shows
// the mark is decided per entry rather than set on every entry a sensitive
// declaration sits near. Both entries name a unit the guest renders, so the build
// completes: a marked entry is matched by its resolved fields like any other.
func TestOmitsSensitiveMark_ABoundaryScopeEntryReadingASensitiveValueReachesTheProviderMarked(t *testing.T) {
	src, err := filepath.Abs(filepath.Join("..", "wasm", "testdata", "omits-provider"))
	if err != nil {
		t.Fatalf("resolve guest source: %v", err)
	}
	catalogPaths := stageColocatedGuests(t, []string{"testdata/omits-sensitive-mark/catalog"}, map[string]func() []byte{
		"provider/omits-example": func() []byte {
			// No skip path: the guest is plain Go targeting wasip1, which ships
			// with every Go distribution, so a skip could only hide a proof that
			// did not run.
			b, err := os.ReadFile(wasmfixture.EnsureLocalGuestBuilt(t, src, "omits-provider.wasm"))
			if err != nil {
				t.Fatalf("read the built guest: %v", err)
			}
			return b
		},
	})
	t.Parallel()

	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/omits-sensitive-mark/release.yaml",
		CatalogPaths: catalogPaths,
	})

	var reports []string
	for _, path := range slices.Sorted(maps.Keys(result.Artifacts)) {
		if strings.HasSuffix(filepath.Base(path), omitsMarkReport) {
			reports = append(reports, path)
		}
	}
	// Exactly one render was handed entries: the FeatureRecipe's. The component's
	// own Recipe declares none, and a render handed none writes no report.
	if len(reports) != 1 {
		t.Fatalf("want one render reporting the entries it received, got %d %v; the build wrote %v",
			len(reports), reports, slices.Sorted(maps.Keys(result.Artifacts)))
	}
	received := strings.Split(strings.TrimRight(string(result.Artifacts[reports[0]]), "\n"), "\n")
	want := []string{
		"omits[0] sensitive=true",
		"omits[1] sensitive=false",
	}
	if !slices.Equal(received, want) {
		t.Errorf("the provider received the marks %q, want %q", received, want)
	}
}
