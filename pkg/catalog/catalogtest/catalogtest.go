// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package catalogtest provides fixtures for tests that hold a catalog view to a
// lockfile's or a closure's pins, and for tests that load a catalog directory
// without its Plugin entities. It is a test helper, not a way to build pins:
// every pin set it returns comes from catalog.PinsFromLockfile or
// catalog.PinsFromClosure, and a refusal of either stops the test at the line
// that asked, rather than handing back an empty pin set a later assertion would
// misread as a view held to nothing.
//
// It depends on the catalog, closure and lockfile packages only, so a test
// anywhere above them can use it.
package catalogtest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// pluginEntityDir is the top-level directory the standard-library catalog keeps
// its Plugin entities in. It is that catalog's layout convention rather than
// anything the compiler reads: a catalog directory may hold its entities under
// any layout, and the loader finds each by the kind its document declares.
const pluginEntityDir = "plugin"

// DirsWithoutPluginEntities returns the top-level directories of a catalog laid
// out the way the standard-library catalog is, in lexical order, leaving out the
// one that layout keeps its Plugin entities in, so a test loads the rest of that
// catalog by passing them to catalog.LoadAll in its place. It fails t where root
// cannot be read, holds no such directory — so the layout is not the one this
// leaves a directory out of, and the catalog would load whole — or holds no
// directory beside it, rather than handing back a list that loads something other
// than what the caller asked for.
//
// A Plugin entity describes a plugin build published beside it, so it moves to a
// new document version or plugin ABI version only when that build is published
// again, while every other entity in a catalog checkout moves with its own edit.
// A checkout can therefore hold Plugin entities the compiler under test refuses
// while everything else in it loads. A test whose subject reads no Plugin entity
// from the catalog at root — every plugin it resolves comes from the binary, from a
// fixture handed to it directly, or from a catalog directory or content cache
// stacked beside the returned directories — loads the catalog through this, so it
// measures its subject rather than whether the plugins have been published again.
// A test whose subject does read a Plugin entity from that catalog loads it whole
// and is not a caller.
func DirsWithoutPluginEntities(t testing.TB, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read the catalog at %s: %v", root, err)
	}
	var dirs []string
	setAside := false
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if e.Name() == pluginEntityDir {
			setAside = true
			continue
		}
		dirs = append(dirs, filepath.Join(root, e.Name()))
	}
	if !setAside {
		t.Fatalf("the catalog at %s holds no %s/ directory, so it is not laid out the way the standard-library "+
			"catalog is and setting its Plugin entities aside would leave nothing out", root, pluginEntityDir)
	}
	if len(dirs) == 0 {
		t.Fatalf("the catalog at %s holds no directory beside %s/", root, pluginEntityDir)
	}
	return dirs
}

// MustPinsFromLockfile returns the pins catalog.PinsFromLockfile builds from lf,
// failing t where it refuses: a fixture lockfile records a declared kind on every
// entity row, as a pull does.
func MustPinsFromLockfile(t testing.TB, lf *lockfile.Lockfile) catalog.Pins {
	t.Helper()
	pins, err := catalog.PinsFromLockfile(lf)
	if err != nil {
		t.Fatalf("pin the fixture view to its lockfile: %v", err)
	}
	return pins
}

// MustPinsFromClosure returns the pins catalog.PinsFromClosure builds from cl,
// failing t where it refuses.
func MustPinsFromClosure(t testing.TB, cl closure.Closure) catalog.Pins {
	t.Helper()
	pins, err := catalog.PinsFromClosure(cl)
	if err != nil {
		t.Fatalf("pin the fixture view to its closure: %v", err)
	}
	return pins
}
