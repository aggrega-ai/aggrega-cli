// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

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
// lint-matrix fixtures under cmd/aggrega/testdata/lint-matrix/plugin/.
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
		changeDir   string
		cells       []cell
		wantSurface string // substring the diagnostic must name on a halt
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
				cfg := lintConfig{
					EntityPath:     proposedPath,
					PrePublish:     true,
					PluginPath:     pluginPath,
					PolicyResolver: func(_ string, ws bool) bool { return ws },
					FetchTarget:    target,
					ws:             resolveTempWorkspace(t),
					DiagOut:        &diag,
				}
				err := lintRun(cfg)
				if c.wantHalt {
					if err == nil {
						t.Fatalf("expected HALT for %s; got PASS (diag: %s)", c.proposedVer, diag.String())
					}
					if tc.wantVerdict != "" && !strings.Contains(err.Error(), tc.wantVerdict) {
						t.Errorf("halt error should demand a %s bump; got: %v", tc.wantVerdict, err)
					}
					if tc.wantSurface != "" && !strings.Contains(diag.String(), tc.wantSurface) {
						t.Errorf("diagnostic should name the changed surface %q; got: %s", tc.wantSurface, diag.String())
					}
				} else if err != nil {
					t.Fatalf("expected PASS for %s; got HALT: %v (diag: %s)", c.proposedVer, err, diag.String())
				}
			})
		}
	}
}

// failingFetchTarget is an oras.ReadOnlyTarget (plus TagLister) whose every
// method errors, so a test proves the registry is never consulted: any
// consultation turns into a lintRun error rather than a silent pass.
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
	cfg := lintConfig{
		EntityPath:     entityPath,
		PrePublish:     true,
		PluginPath:     pluginPath,
		PolicyResolver: func(_ string, ws bool) bool { return ws },
		FetchTarget:    failingFetchTarget{},
		ws:             resolveTempWorkspace(t),
		DiagOut:        &diag,
	}
	err := lintRun(cfg)
	if err != nil {
		t.Fatalf("core plugin should be exempt from the evolution gate; got HALT: %v (diag: %s)", err, diag.String())
	}
	if !strings.Contains(diag.String(), "core plugin: publish evolution gate not applicable") {
		t.Errorf("expected the core-exemption diagnostic; got: %s", diag.String())
	}
}
