// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// refusalComponentURI is the reference the refusal fixture's Aggregate names. The
// Component it resolves to and the Recipe the Manifest maps it to carry distinct
// references, so each is pinned in a row of its own and a fault in one of them
// cannot resolve to the other.
const refusalComponentURI = "test/app"

const refusalComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/app
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`

// refusalFixture is a workspace whose Release reaches a Component and a Recipe
// authored in the catalog directory it reads, pulled once.
type refusalFixture struct {
	root          string
	catalogDir    string
	componentPath string
	// releasePath is the Release's absolute path, so nothing that reads it
	// depends on the working directory.
	releasePath string
	// recorded is the digest the pull pinned for the Component.
	recorded string
}

// writeRefusalFixture lays the workspace down and runs the pull that writes its
// lockfile.
//
// The workspace is offline. Everything it reaches is in the catalog directory, so
// a pull and a verify complete without a registry, and a reference that stops
// resolving there resolves to nothing rather than being asked of a registry.
func writeRefusalFixture(t *testing.T) refusalFixture {
	t.Helper()
	root := t.TempDir()
	catalogDir := filepath.Join(root, "catalog")
	componentPath := filepath.Join(catalogDir, "component", "test", "app", "1.0.0", "component.yaml")
	writeFixture(t, componentPath, refusalComponent)
	writeFixture(t, filepath.Join(catalogDir, "recipe", "helm", "app", "1.0.0", "recipe.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: app
  version: "1.0.0"
spec:
  capability: helm
  implements: test/app
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      app:
        image: test-app:latest
        ports:
          web: 8080
`)
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    api:
      type: test/app
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/app: helm/app:1.0.0
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          infra:
            replicas: 1
`)
	writeWorkspaceConfig(t, root, "offline: true\n")
	releasePath := filepath.Join(root, "release.yaml")

	var out, diag bytes.Buffer
	if err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{catalogDir},
		Out:         &out,
		DiagOut:     &diag,
	}); err != nil {
		t.Fatalf("seed pull: %v\n%s", err, diag.String())
	}
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load the lockfile the pull wrote: %v", err)
	}
	var recorded string
	for _, e := range lf.Entities {
		if e.URI == refusalComponentURI {
			if e.ResolvedVersion != "1.0.0" {
				t.Fatalf("the pull pinned %q at %q; want 1.0.0", e.URI, e.ResolvedVersion)
			}
			recorded = e.Digest
		}
	}
	if recorded == "" {
		t.Fatalf("the pull pinned no digest for %q; entities=%+v", refusalComponentURI, lf.Entities)
	}
	return refusalFixture{root: root, catalogDir: catalogDir, componentPath: componentPath, releasePath: releasePath, recorded: recorded}
}

// verify runs the verifying pull over the fixture and returns what it printed on
// its standard output together with its error.
func (f refusalFixture) verify(t *testing.T) (string, error) {
	t.Helper()
	var out, diag bytes.Buffer
	err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: f.root}),
		ReleasePath: f.releasePath,
		CatalogDirs: []string{f.catalogDir},
		Verify:      true,
		Out:         &out,
		DiagOut:     &diag,
	})
	return out.String(), err
}

// replaceOnce rewrites the one occurrence of old in the file at path, failing the
// test when old does not occur exactly once, so the fault a row plants is the
// fault it names and no other.
func replaceOnce(t *testing.T, path, old, replacement string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if n := bytes.Count(data, []byte(old)); n != 1 {
		t.Fatalf("%s holds %q %d times; the fault needs exactly one", path, old, n)
	}
	if err := os.WriteFile(path, bytes.Replace(data, []byte(old), []byte(replacement), 1), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestPullVerifyStillRefusesEachFaultItExistsToRefuse holds the verifying pull to
// the four faults it exists to refuse, one per fixture, each asserted against the
// condition that fault raises: a lockfile row edited by hand, an entity removed from
// the catalog directory the Release reads, an edit to the Release itself, and an
// entity authored in that catalog directory whose content changed since the pull.
//
// The verifying pull honours the pins the lockfile records, and the last row is
// where that could hide a fault: a catalog-directory entity resolves from the
// directory before any pin is consulted, so an edit to it is drift. The row lays the
// Component's pulled bytes, with the manifest a registry fetch writes beside them,
// into the cache slot its pin addresses before editing it — a complete entry, since
// a slot holding the content alone is read by no reader — so a verify that consulted
// the pin first would find those bytes, match them to the pin and report no drift:
// the row fails there rather than passing by accident.
//
// The first row is the positive control: without it a fixture that could not be
// verified at all would pass every refusal below.
func TestPullVerifyStillRefusesEachFaultItExistsToRefuse(t *testing.T) {
	t.Parallel()
	const zeroDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	cases := []struct {
		name string
		// fault plants the row's one fault and returns what the refusal must say.
		fault func(t *testing.T, f refusalFixture) (check func(t *testing.T, err error))
	}{
		{
			name: "no fault: the verify reports a match",
			fault: func(t *testing.T, f refusalFixture) func(*testing.T, error) {
				return func(t *testing.T, err error) {
					if err != nil {
						t.Fatalf("the verify refused a workspace nothing changed in: %v", err)
					}
				}
			},
		},
		{
			name: "a lockfile row edited by hand",
			fault: func(t *testing.T, f refusalFixture) func(*testing.T, error) {
				replaceOnce(t, filepath.Join(f.root, "release.lock"), f.recorded, zeroDigest)
				want := `entity "` + refusalComponentURI + `" changed: lockfile=(1.0.0,` + zeroDigest + `) current=(1.0.0,` + f.recorded + `)`
				return func(t *testing.T, err error) {
					if !errors.Is(err, lockfile.ErrLockfileDrift()) {
						t.Fatalf("want the lockfile-drift condition; got %v", err)
					}
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the drift report does not name the edited row as edited; want %q in:\n%v", want, err)
					}
				}
			},
		},
		{
			name: "an entity removed from the catalog directory the Release reads",
			fault: func(t *testing.T, f refusalFixture) func(*testing.T, error) {
				if err := os.Remove(f.componentPath); err != nil {
					t.Fatalf("remove the Component: %v", err)
				}
				return func(t *testing.T, err error) {
					if !errors.Is(err, closure.ErrUnresolvableURI()) {
						t.Fatalf("want the unresolvable-reference condition; got %v", err)
					}
					var unresolved *closure.UnresolvedEntityError
					if !errors.As(err, &unresolved) || unresolved.URI != refusalComponentURI {
						t.Errorf("the refusal does not carry the removed reference %q: %v", refusalComponentURI, err)
					}
				}
			},
		},
		{
			name: "an edit to the Release itself",
			fault: func(t *testing.T, f refusalFixture) func(*testing.T, error) {
				replaceOnce(t, filepath.Join(f.root, "release.yaml"), "replicas: 1\n", "replicas: 2\n")
				return func(t *testing.T, err error) {
					if !errors.Is(err, lockfile.ErrLockfileDrift()) {
						t.Fatalf("want the lockfile-drift condition; got %v", err)
					}
					if !strings.Contains(err.Error(), `release document "release.yaml" was edited after the lockfile was written`) {
						t.Errorf("the drift report does not name the edited Release: %v", err)
					}
					if strings.Contains(err.Error(), `entity "`) {
						t.Errorf("an edit to the Release alone moved an entity row as well, so the row no longer plants one fault: %v", err)
					}
				}
			},
		},
		{
			name: "an entity authored in the catalog directory, changed since the pull, its pulled bytes in the slot its pin addresses",
			fault: func(t *testing.T, f refusalFixture) func(*testing.T, error) {
				pulled, err := os.ReadFile(f.componentPath)
				if err != nil {
					t.Fatalf("read the Component: %v", err)
				}
				// The pulled bytes are what the pin names: laid in the slot, they would
				// verify against it, which is what makes a pin-first read pass wrongly.
				if got := publishedRowDigest(t, pulled); got != f.recorded {
					t.Fatalf("the pulled Component row-digests to %s, not the pinned %s, so the slot would not hold the pin", got, f.recorded)
				}
				layFetchedSlot(t, f.root, refusalComponentURI, "1.0.0", pulled, true)
				replaceOnce(t, f.componentPath, "default: 8080 }", "default: 8081 }")
				want := `entity "` + refusalComponentURI + `" changed: lockfile=(1.0.0,` + f.recorded + `) current=(1.0.0,` + rowDigestAt(t, f.componentPath) + `)`
				return func(t *testing.T, err error) {
					if !errors.Is(err, lockfile.ErrLockfileDrift()) {
						t.Fatalf("want the lockfile-drift condition; got %v", err)
					}
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the drift report does not hold the pinned digest against the edited file's; want %q in:\n%v", want, err)
					}
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := writeRefusalFixture(t)
			check := tc.fault(t, f)
			lockPath := filepath.Join(f.root, "release.lock")
			before, err := os.ReadFile(lockPath)
			if err != nil {
				t.Fatalf("read the lockfile before the verify: %v", err)
			}
			out, verr := f.verify(t)
			check(t, verr)
			if verr == nil && !strings.Contains(out, "Verify: lockfile") {
				t.Errorf("the verify returned no error but printed no verdict: %q", out)
			}
			after, err := os.ReadFile(lockPath)
			if err != nil {
				t.Fatalf("read the lockfile after the verify: %v", err)
			}
			if !bytes.Equal(before, after) {
				t.Errorf("the verify rewrote the lockfile")
			}
		})
	}
}

// TestPullVerifyRefusesPinSourcesWhicheverValueItIsGiven holds --verify beside
// --pin-sources to a refusal, for each value the flag can be given. That flag
// decides whether a pull records the pins an entity.lock is missing or halts on
// them, and a verify does neither, so accepting the pair would leave the flag doing
// nothing while reading as though it had done something.
//
// It runs the real command over a workspace the verify can answer, so a pair that
// is accepted shows up as a verify that returned without an error rather than as a
// refusal. The first row is the positive control: the same command with the flag
// absent answers, so a refusal below is the pair's and not the workspace's.
func TestPullVerifyRefusesPinSourcesWhicheverValueItIsGiven(t *testing.T) {
	// Not parallel: its rows set the process environment, and the command tree's pre-run writes the process-global display decision.
	cases := []struct {
		name    string
		extra   []string
		refused bool
	}{
		{name: "no --pin-sources: the verify answers", extra: nil, refused: false},
		{name: "--pin-sources", extra: []string{"--pin-sources"}, refused: true},
		{name: "--pin-sources=false", extra: []string{"--pin-sources=false"}, refused: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The configuration layer above the workspace is emptied before the
			// fixture's pull, so the pull and the command below resolve the same
			// workspace whatever the machine running this carries.
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", home)
			f := writeRefusalFixture(t)
			lockPath := filepath.Join(f.root, "release.lock")
			before, err := os.ReadFile(lockPath)
			if err != nil {
				t.Fatalf("read the lockfile before the command: %v", err)
			}

			root := newRootCmd()
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs(append([]string{"--workspace-root", f.root,
				"pull", "--release", f.releasePath, "--catalog", f.catalogDir, "--verify", "--quiet"}, tc.extra...))
			err = root.ExecuteContext(context.Background())

			if !tc.refused {
				if err != nil {
					t.Fatalf("the verify refused a workspace nothing changed in: %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("pull --verify %s was accepted; the pair must be refused, because the flag does nothing under a verify",
						strings.Join(tc.extra, " "))
				}
				const want = "--verify and --pin-sources are mutually exclusive"
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name the pair as mutually exclusive; want %q in: %v", want, err)
				}
			}

			after, err := os.ReadFile(lockPath)
			if err != nil {
				t.Fatalf("read the lockfile after the command: %v", err)
			}
			if !bytes.Equal(before, after) {
				t.Errorf("the command rewrote the lockfile")
			}
		})
	}
}
