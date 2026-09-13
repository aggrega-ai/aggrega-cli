// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// pluginFixtureBytes returns the YAML bytes of one of the plugin
// lint-matrix fixtures under internal/lintops/testdata/lint-matrix/plugin/.
func pluginFixtureBytes(t *testing.T, changeDir, file string) []byte {
	t.Helper()
	path := filepath.Join("testdata", "lint-matrix", "plugin", changeDir, file)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return b
}

// TestLintPrePublishPluginEvolution drives the real `aggrega lint
// --pre-publish` path for a Plugin entity through the embedded classifier
// wasm, seeding the prior version into an in-memory registry exactly as the
// other integration tests do. It proves the describe()-surface classification
// gates the proposed version bump: a removed export demands MAJOR, an added
// export demands at least MINOR, and a metadata-only change accepts PATCH.
func TestLintPrePublishPluginEvolution(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm")

	type cell struct {
		proposedVer string
		wantHalt    bool
	}
	cases := []struct {
		changeDir string
		cells     []cell
		// wantSurface is the substring naming the changed surface. On a halt it
		// is read off the run's error: a halting finding becomes an occurrence
		// of the condition that names its rule and travels up as that error,
		// carrying the rule's own text, while the diagnostic stream carries the
		// advisories.
		wantSurface string
		wantVerdict string // required-bump substring the halt error must carry
	}{
		{
			changeDir: "removed-export",
			cells: []cell{
				{"1.0.1", true},  // PATCH rejected
				{"1.1.0", true},  // MINOR rejected
				{"2.0.0", false}, // MAJOR accepted
			},
			wantSurface: "spec.exports removed export",
			wantVerdict: "MAJOR",
		},
		{
			changeDir: "added-export",
			cells: []cell{
				{"1.0.1", true},  // PATCH rejected
				{"1.1.0", false}, // MINOR accepted
			},
			wantSurface: "spec.exports added export",
			wantVerdict: "MINOR",
		},
		{
			changeDir: "metadata-only",
			cells: []cell{
				{"1.0.1", false}, // PATCH accepted
			},
		},
	}

	for _, tc := range cases {
		for _, c := range tc.cells {
			t.Run(tc.changeDir+"/"+c.proposedVer, func(t *testing.T) {
				target := &memoryRegistryTarget{Store: memory.New()}
				previousYAML := pluginFixtureBytes(t, tc.changeDir, "previous.yaml")
				pushFixtureToTarget(t, target, "plugin", "partner/custom-renderer", "1.0.0", previousYAML)

				proposedYAML := pluginFixtureBytes(t, tc.changeDir, "proposed.yaml")
				proposedPath := writeEntityFile(t, proposedYAML, c.proposedVer)

				var diag bytes.Buffer
				cfg := Config{
					EntityPath:     proposedPath,
					PrePublish:     true,
					PluginPath:     pluginPath,
					PolicyResolver: func(_ string, ws bool) bool { return ws },
					FetchTarget:    target,
					Workspace:      resolveTempWorkspace(t),
					DiagOut:        &diag,
				}
				err := RunEntity(t.Context(), cfg)
				if c.wantHalt {
					if err == nil {
						t.Fatalf("expected HALT for %s; got PASS (diag: %s)", c.proposedVer, diag.String())
					}
					if tc.wantVerdict != "" && !strings.Contains(err.Error(), tc.wantVerdict) {
						t.Errorf("halt error should demand a %s bump; got: %v", tc.wantVerdict, err)
					}
					if tc.wantSurface != "" && !strings.Contains(err.Error(), tc.wantSurface) {
						t.Errorf("halt error should name the changed surface %q; got: %v (diag: %s)",
							tc.wantSurface, err, diag.String())
					}
				} else if err != nil {
					t.Fatalf("expected PASS for %s; got HALT: %v (diag: %s)", c.proposedVer, err, diag.String())
				}
			})
		}
	}
}

// TestLintPrePublishPluginCommandEvolution drives the same real `aggrega
// lint --pre-publish` path as the export matrix, but exercises the
// command-surface classifier: a Plugin's declared invocation contract
// (spec.commands and each command's flags). A removed command demands
// MAJOR, an added command demands at least MINOR, and a narrowed flag
// field (here a flag's type) demands MAJOR. Each pair's only delta is the
// command surface — exports, capabilities, provider, and abiVersion are
// identical across previous and proposed — so the command classifier is
// unambiguously what drives the verdict.
func TestLintPrePublishPluginCommandEvolution(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm")

	type cell struct {
		proposedVer string
		wantHalt    bool
	}
	cases := []struct {
		changeDir string
		cells     []cell
		// wantSurface is the substring naming the changed surface. On a halt it
		// is read off the run's error, for the reason recorded on the export
		// matrix's own table.
		wantSurface string
		wantVerdict string // required-bump substring the halt error must carry
	}{
		{
			changeDir: "removed-command",
			cells: []cell{
				{"1.0.1", true},  // PATCH rejected
				{"1.1.0", true},  // MINOR rejected
				{"2.0.0", false}, // MAJOR accepted
			},
			wantSurface: "spec.commands removed command",
			wantVerdict: "MAJOR",
		},
		{
			changeDir: "added-command",
			cells: []cell{
				{"1.0.1", true},  // PATCH rejected
				{"1.1.0", false}, // MINOR accepted
			},
			wantSurface: "spec.commands added command",
			wantVerdict: "MINOR",
		},
		{
			changeDir: "changed-flag-type",
			cells: []cell{
				{"1.1.0", true},  // MINOR rejected
				{"2.0.0", false}, // MAJOR accepted
			},
			wantSurface: "changed type (breaking)",
			wantVerdict: "MAJOR",
		},
	}

	for _, tc := range cases {
		for _, c := range tc.cells {
			t.Run(tc.changeDir+"/"+c.proposedVer, func(t *testing.T) {
				target := &memoryRegistryTarget{Store: memory.New()}
				previousYAML := pluginFixtureBytes(t, tc.changeDir, "previous.yaml")
				pushFixtureToTarget(t, target, "plugin", "partner/custom-renderer", "1.0.0", previousYAML)

				proposedYAML := pluginFixtureBytes(t, tc.changeDir, "proposed.yaml")
				proposedPath := writeEntityFile(t, proposedYAML, c.proposedVer)

				var diag bytes.Buffer
				cfg := Config{
					EntityPath:     proposedPath,
					PrePublish:     true,
					PluginPath:     pluginPath,
					PolicyResolver: func(_ string, ws bool) bool { return ws },
					FetchTarget:    target,
					Workspace:      resolveTempWorkspace(t),
					DiagOut:        &diag,
				}
				err := RunEntity(t.Context(), cfg)
				if c.wantHalt {
					if err == nil {
						t.Fatalf("expected HALT for %s; got PASS (diag: %s)", c.proposedVer, diag.String())
					}
					if tc.wantVerdict != "" && !strings.Contains(err.Error(), tc.wantVerdict) {
						t.Errorf("halt error should demand a %s bump; got: %v", tc.wantVerdict, err)
					}
					if tc.wantSurface != "" && !strings.Contains(err.Error(), tc.wantSurface) {
						t.Errorf("halt error should name the changed surface %q; got: %v (diag: %s)",
							tc.wantSurface, err, diag.String())
					}
				} else if err != nil {
					t.Fatalf("expected PASS for %s; got HALT: %v (diag: %s)", c.proposedVer, err, diag.String())
				}
			})
		}
	}
}

// TestLintPrePublishPluginCapabilityEvolution drives the same real `aggrega
// lint --pre-publish` path as the export and command matrices, but exercises
// the capability classifier: the list a plugin declares inside the per-type
// block that gives it meaning. Dropping a capability withdraws something a
// consumer may be resolving on, so it demands MAJOR; adding one widens the
// surface and demands no more than MINOR. Each pair's only delta is that
// list — exports, commands and abiVersion are identical across previous and
// proposed — so the capability classifier is unambiguously what drives the
// verdict.
//
// Both directions are covered on purpose. A classifier reading a capability
// list that is not where it looks reports no capability change at all, and a
// removal can still land on MAJOR through an unrelated arm — so the removal
// case alone can pass while the surface is entirely unscored. The added case
// is what distinguishes the two, because nothing else in the fold yields
// MINOR here.
func TestLintPrePublishPluginCapabilityEvolution(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm")

	type cell struct {
		proposedVer string
		wantHalt    bool
	}
	cases := []struct {
		changeDir string
		cells     []cell
		// wantSurface is the substring naming the changed surface, required on
		// EVERY cell. Which element carries it follows the consequence, not the
		// cell: a halting finding becomes the run's error, and a finding the
		// satisfied bump re-severitized to an advisory is written to the
		// diagnostic stream.
		wantSurface string
		wantVerdict string // required-bump substring the halt error must carry
	}{
		{
			changeDir: "removed-capability",
			cells: []cell{
				{"1.0.1", true},  // PATCH rejected
				{"1.1.0", true},  // MINOR rejected
				{"2.0.0", false}, // MAJOR accepted
			},
			wantSurface: "spec.provider.capabilities removed capability(ies): compose",
			wantVerdict: "MAJOR",
		},
		{
			changeDir: "added-capability",
			cells: []cell{
				{"1.0.1", true},  // PATCH rejected
				{"1.1.0", false}, // MINOR accepted — a widened list is not breaking
				{"2.0.0", false}, // MAJOR accepted: a bump beyond the minimum is allowed
			},
			wantSurface: "spec.provider.capabilities added capability(ies): compose",
			wantVerdict: "MINOR",
		},
	}

	for _, tc := range cases {
		for _, c := range tc.cells {
			t.Run(tc.changeDir+"/"+c.proposedVer, func(t *testing.T) {
				target := &memoryRegistryTarget{Store: memory.New()}
				previousYAML := pluginFixtureBytes(t, tc.changeDir, "previous.yaml")
				pushFixtureToTarget(t, target, "plugin", "partner/custom-renderer", "1.0.0", previousYAML)

				proposedYAML := pluginFixtureBytes(t, tc.changeDir, "proposed.yaml")
				proposedPath := writeEntityFile(t, proposedYAML, c.proposedVer)

				var diag bytes.Buffer
				cfg := Config{
					EntityPath:     proposedPath,
					PrePublish:     true,
					PluginPath:     pluginPath,
					PolicyResolver: func(_ string, ws bool) bool { return ws },
					FetchTarget:    target,
					Workspace:      resolveTempWorkspace(t),
					DiagOut:        &diag,
				}
				err := RunEntity(t.Context(), cfg)
				if c.wantHalt {
					if err == nil {
						t.Fatalf("expected HALT for %s; got PASS (diag: %s)", c.proposedVer, diag.String())
					}
					if !strings.Contains(err.Error(), tc.wantVerdict) {
						t.Errorf("halt error should demand a %s bump; got: %v", tc.wantVerdict, err)
					}
				} else if err != nil {
					t.Fatalf("expected PASS for %s; got HALT: %v (diag: %s)", c.proposedVer, err, diag.String())
				}
				// The block whose list changed must be named on every cell,
				// halt or pass. A verdict alone cannot tell an author which of
				// a plugin's capability senses moved, and naming a key the
				// entity does not carry sends them looking in the wrong place.
				// The element read follows the consequence: a halting finding
				// travels up as the run's error, an advisory to the diagnostic
				// stream.
				carrier, where := diag.String(), "diagnostic"
				if c.wantHalt {
					carrier, where = err.Error(), "halt error"
				}
				if !strings.Contains(carrier, tc.wantSurface) {
					t.Errorf("%s should name the changed surface %q; got: %s", where, tc.wantSurface, carrier)
				}
			})
		}
	}
}

// TestLintPrePublishPluginCommandFirstPublish proves a command-bearing
// Plugin published for the first time (no prior version in the registry)
// passes cleanly: with nothing to diff against, the command-surface
// classifier records no violation and the run reports the
// first-publication path instead of enforcing a version bump.
func TestLintPrePublishPluginCommandFirstPublish(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm")

	// No prior version seeded: an empty in-memory registry, so the
	// pre-publish path takes the first-publication branch.
	target := &memoryRegistryTarget{Store: memory.New()}

	proposedYAML := pluginFixtureBytes(t, "added-command", "proposed.yaml")
	proposedPath := writeEntityFile(t, proposedYAML, "1.0.0")

	var diag bytes.Buffer
	cfg := Config{
		EntityPath:     proposedPath,
		PrePublish:     true,
		PluginPath:     pluginPath,
		PolicyResolver: func(_ string, ws bool) bool { return ws },
		FetchTarget:    target,
		Workspace:      resolveTempWorkspace(t),
		DiagOut:        &diag,
	}
	err := RunEntity(t.Context(), cfg)
	if err != nil {
		t.Fatalf("first publication of a command-bearing plugin should PASS; got HALT: %v (diag: %s)", err, diag.String())
	}
	if !strings.Contains(diag.String(), "first publication of") ||
		!strings.Contains(diag.String(), "no version-bump verification") {
		t.Errorf("expected the first-publication diagnostic; got: %s", diag.String())
	}
}

// failingFetchTarget is an oras.ReadOnlyTarget (plus TagLister) whose every
// method errors, so a test proves the registry is never consulted: any
// consultation turns into a RunEntity error rather than a silent pass.
type failingFetchTarget struct{}

func (failingFetchTarget) Fetch(context.Context, ocispec.Descriptor) (io.ReadCloser, error) {
	return nil, fmt.Errorf("registry fetch must not be consulted for a core plugin")
}

func (failingFetchTarget) Exists(context.Context, ocispec.Descriptor) (bool, error) {
	return false, fmt.Errorf("registry must not be consulted for a core plugin")
}

func (failingFetchTarget) Resolve(context.Context, string) (ocispec.Descriptor, error) {
	return ocispec.Descriptor{}, fmt.Errorf("registry must not be consulted for a core plugin")
}

func (failingFetchTarget) Tags(context.Context, string, func([]string) error) error {
	return fmt.Errorf("registry tag listing must not be consulted for a core plugin")
}

// TestLintPrePublishCoreExempt proves a core plugin (spec.plugin is a built-in
// embedded reference) is exempt from the publish-time evolution gate: the run
// short-circuits before any registry fetch (a failing FetchTarget that would
// error if consulted stays silent), emits the exemption diagnostic, and passes.
func TestLintPrePublishCoreExempt(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm")

	entityPath := filepath.Join("testdata", "lint-matrix", "plugin", "core-exempt", "plugin.yaml")

	var diag bytes.Buffer
	cfg := Config{
		EntityPath:     entityPath,
		PrePublish:     true,
		PluginPath:     pluginPath,
		PolicyResolver: func(_ string, ws bool) bool { return ws },
		FetchTarget:    failingFetchTarget{},
		Workspace:      resolveTempWorkspace(t),
		DiagOut:        &diag,
	}
	err := RunEntity(t.Context(), cfg)
	if err != nil {
		t.Fatalf("core plugin should be exempt from the evolution gate; got HALT: %v (diag: %s)", err, diag.String())
	}
	if !strings.Contains(diag.String(), "core plugin: publish evolution gate not applicable") {
		t.Errorf("expected the core-exemption diagnostic; got: %s", diag.String())
	}
}
