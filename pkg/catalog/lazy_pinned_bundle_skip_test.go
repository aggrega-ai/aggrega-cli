// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// bundleWith builds a real published archive over a version directory holding
// the named entity documents, every one of them carried, through the production
// bundler. Building the fixture with the bundler rather than by hand is what
// makes the archive under test the shape a publish actually produces; carrying
// every document is what an archive another client packed from the whole
// directory holds, which is the shape the ambiguous rows need.
func bundleWith(t *testing.T, entities map[string]string) []byte {
	t.Helper()
	ver := filepath.Join(t.TempDir(), "1.0.0")
	if err := os.MkdirAll(ver, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var anchorPath string
	carried := make([]string, 0, len(entities))
	for name, body := range entities {
		p := filepath.Join(ver, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		carried = append(carried, name)
		if anchorPath == "" || p < anchorPath {
			anchorPath = p
		}
	}
	if anchorPath == "" {
		t.Fatal("bundleWith needs at least one document to bundle")
	}
	blob, _, err := oci.Bundle("Recipe", anchorPath, carried)
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	return blob
}

// pinnedRecipeYAML is a schema-valid Recipe: `spec` must carry at least one of
// the four output-bearing blocks, so the workload is load-bearing rather than
// decoration. A fixture without it is refused by envelope validation, which
// would record a skip of its own and make the silence assertions below read as
// the product's answer when they were the fixture's.
func pinnedRecipeYAML(name string) string {
	return `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: ` + name + `
  version: 1.0.0
spec:
  capability: helm
  implements: core/` + name + `
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
`
}

// captureNotices runs fn with view's notices directed at a buffer, and returns
// what view wrote there — what a reader at a terminal is shown, on the writer
// the surface hands the view. It also holds the view to that writer: the
// process's standard error is redirected for the duration and must stay empty,
// because the view writes its notices where its surface says and nowhere else.
func captureNotices(t *testing.T, view *LazyLookup, fn func()) string {
	t.Helper()
	var notices bytes.Buffer
	view.WriteNoticesTo(&notices)
	stray := captureStderr(t, fn)
	if stray != "" {
		t.Errorf("the view wrote to the process's standard error rather than to the writer it was handed:\n%s", stray)
	}
	return notices.String()
}

// captureStderr runs fn with os.Stderr redirected, and returns what was written.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	saved := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	os.Stderr = saved
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// TestPinnedBundleThatCannotBeUnwrappedIsReportedNotSwallowed covers the half of
// this path that used to have no channel at all.
//
// A pinned cache slot that is present and verifies against its pin, but whose
// archive yields no entity, was skipped exactly as a pin with no slot at all is
// skipped — so "this artifact is unreadable" and "this entity resolved
// elsewhere" arrived downstream as the same silence. They are different facts
// and only one of them is a defect, so they are now told apart.
//
// The two-entity fixture is the one worth driving: it is the artifact a sibling
// collision publishes, and before the anchor refused it the slot resolved to
// whichever entity sorted first, under the other one's pinned name.
func TestPinnedBundleThatCannotBeUnwrappedIsReportedNotSwallowed(t *testing.T) {
	ws := t.TempDir()
	blob := bundleWith(t, map[string]string{
		"frontend.yaml": pinnedRecipeYAML("frontend"),
		"database.yaml": pinnedRecipeYAML("database"),
	})
	pin := writePinnedSlot(t, ws, cacheUnderCatalogDir, "helm/shared:1.0.0", oci.MediaTypeRecipe, blob)

	subject := NewLazyLookup(ws, cacheUnderCatalogDir, []string{ws}, nil)
	subject.SetPins(Pins{{Kind: "Recipe", URI: "helm/shared"}: pin})

	var skips map[string]error
	warning := captureNotices(t, subject, func() { skips = subject.Skips() })

	wantKey := pinnedSlotLabel(PinKey{Kind: "Recipe", URI: "helm/shared"})
	reason, recorded := skips[wantKey]
	if !recorded {
		t.Fatalf("a pinned artifact that could not be unwrapped was skipped with no record; keys present: %v", skipKeysOf(skips))
	}
	if !errors.Is(reason, oci.ErrBundleAmbiguousEntityDocument()) {
		t.Errorf("the recorded reason must name the condition that refused the artifact; got %v", reason)
	}
	// What a user sees. The line names the pinned reference — the term they
	// wrote in the lockfile — rather than a cache path nobody authored.
	if !strings.Contains(warning, wantKey) {
		t.Errorf("the warning does not name the pinned reference, so a reader cannot tell which pin failed:\n%s", warning)
	}
	t.Logf("what a user sees on stderr: %s", strings.TrimRight(warning, "\n"))
	t.Logf("what the closure-level refusal record carries: %s -> %v", wantKey, reason)
}

// TestPinnedBundleSkipIsDistinctFromAPinWithNoSlot is the calibration that gives
// the assertion above its meaning, in both directions.
//
// A pin whose slot is absent must stay silent: that is the ordinary state of an
// entity resolved from a catalog directory, and reporting it would turn every
// Tier-1 resolution into a warning. A pinned slot that unwraps cleanly must also
// stay silent. Only the unreadable artifact speaks — so the two bare skips that
// sat four lines apart are now telling a reader different things.
func TestPinnedBundleSkipIsDistinctFromAPinWithNoSlot(t *testing.T) {
	t.Run("pin with no cache slot stays silent", func(t *testing.T) {
		ws := t.TempDir()
		subject := NewLazyLookup(ws, cacheUnderCatalogDir, []string{ws}, nil)
		subject.SetPins(Pins{
			{Kind: "Recipe", URI: "helm/absent"}: {ResolvedVersion: "1.0.0", Digest: "sha256:" + strings.Repeat("0", 64)},
		})
		var skips map[string]error
		warning := captureNotices(t, subject, func() { skips = subject.Skips() })
		if len(skips) != 0 {
			t.Errorf("a pin with no cache slot must not be reported — it is how a catalog-resolved entity looks; got %v", skipKeysOf(skips))
		}
		if warning != "" {
			t.Errorf("a pin with no cache slot produced a warning:\n%s", warning)
		}
	})

	t.Run("a well-formed pinned bundle resolves and stays silent", func(t *testing.T) {
		ws := t.TempDir()
		blob := bundleWith(t, map[string]string{"anything.yaml": pinnedRecipeYAML("frontend")})
		pin := writePinnedSlot(t, ws, cacheUnderCatalogDir, "helm/frontend:1.0.0", oci.MediaTypeRecipe, blob)

		subject := NewLazyLookup(ws, cacheUnderCatalogDir, []string{ws}, nil)
		subject.SetPins(Pins{{Kind: "Recipe", URI: "helm/frontend"}: pin})
		var skips map[string]error
		warning := captureNotices(t, subject, func() { skips = subject.Skips() })
		if len(skips) != 0 {
			t.Errorf("a pinned bundle that unwraps cleanly must not be reported; got %v", skipKeysOf(skips))
		}
		if warning != "" {
			t.Errorf("a pinned bundle that unwraps cleanly produced a warning:\n%s", warning)
		}
		// And it actually resolved — otherwise the silence above would be the
		// silence this test exists to forbid, wearing the right answer's clothes.
		if _, ok := subject.recipes["helm/frontend:1.0.0"]; !ok {
			t.Errorf("the pinned Recipe did not resolve, so the silence proves nothing; enumerated: %v", keysOf(subject.recipes))
		}
	})
}

// skipKeysOf names the refusal record's keys for a failure message. The sibling
// helper over the enumerated recipes already exists in this package and is
// reused rather than re-rolled.
func skipKeysOf(m map[string]error) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
