// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package lockfiletest provides fixtures for tests that need a lockfile
// describing exactly a closure they computed. It is a test helper, not a way to
// produce a lockfile: a resolution records facts beside each pin — signatures,
// deprecation markers — that this package does not.
//
// It depends on the lockfile and closure packages only, so a test anywhere above
// them can use it, the tests of the packages that compute a closure included.
package lockfiletest

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// Mirroring returns a copy of base carrying every pin the drift comparison
// (lockfile.Verify) reads off cl, appended after any base already carries: the
// Release files, the entities, the plugins and each library axis, each at the
// version and digest cl records. A comparison of the result with cl is clean, and
// one with a different closure names each difference.
func Mirroring(base *lockfile.Lockfile, cl closure.Closure) *lockfile.Lockfile {
	lf := *base
	for _, d := range cl.ReleaseDocuments {
		lf.ReleaseDocuments = append(lf.ReleaseDocuments, lockfile.ReleaseDocument{Path: d.Path, Digest: d.Digest})
	}
	for _, e := range cl.Entities {
		lf.Entities = append(lf.Entities, lockfile.Entity{Kind: e.Kind(), URI: e.URI, ResolvedVersion: e.ResolvedVersion, Digest: e.Digest})
	}
	for _, p := range cl.Plugins {
		lf.Plugins = append(lf.Plugins, lockfile.Plugin{URI: p.URI, ResolvedVersion: p.ResolvedVersion, Digest: p.Digest})
	}
	for _, l := range cl.SchemaLibraries {
		lf.SchemaLibraries = append(lf.SchemaLibraries, lockfile.SchemaLibrary{URI: l.URI, ResolvedVersion: l.ResolvedVersion, Digest: l.Digest})
	}
	for _, l := range cl.MappingLibraries {
		lf.MappingLibraries = append(lf.MappingLibraries, lockfile.MappingLibrary{URI: l.URI, ResolvedVersion: l.ResolvedVersion, Digest: l.Digest})
	}
	for _, l := range cl.WorkloadLibraries {
		lf.WorkloadLibraries = append(lf.WorkloadLibraries, lockfile.WorkloadLibrary{URI: l.URI, ResolvedVersion: l.ResolvedVersion, Digest: l.Digest})
	}
	for _, l := range cl.ArtifactLibraries {
		lf.ArtifactLibraries = append(lf.ArtifactLibraries, lockfile.ArtifactLibrary{URI: l.URI, ResolvedVersion: l.ResolvedVersion, Digest: l.Digest})
	}
	return &lf
}
