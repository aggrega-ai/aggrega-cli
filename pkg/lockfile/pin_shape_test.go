// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// pulledLockfile is a lockfile in the shape a pull writes, carrying every
// spelling a writer produces for a version or a digest: the "(devel)" marker a
// compiler built from source records, a core-plugin version in the form a build
// between tags reports, an empty version on a top-level entity resolved by path,
// a git-repository source pinned to the full commit, a plugin row whose version is
// its content digest, and a content digest on every digest field.
func pulledLockfile() *lockfile.Lockfile {
	pluginPin := fixtureDigest("wasm")
	return &lockfile.Lockfile{
		Version:     1,
		Release:     "release.yaml",
		GeneratedAt: "2026-09-26T00:00:00Z",
		Toolchain: &lockfile.ToolchainStamp{
			CompilerVersion: "(devel)",
			CorePlugins:     []lockfile.CorePluginVersion{{Plugin: "provider/helm", Version: "0.7.3-70-g4422bcf2"}},
		},
		ReleaseDocuments: []lockfile.ReleaseDocument{{Path: "release.yaml", Digest: fixtureDigest("release")}},
		Entities: []lockfile.Entity{
			{Kind: "aggregate", URI: "./aggregate.yaml", ResolvedVersion: "", Digest: fixtureDigest("aggregate")},
			{
				Kind: "component", URI: "demo/app/api",
				ResolvedVersion: "1.0.0",
				Digest:          fixtureDigest("api"),
				ManifestDigest:  fixtureDigest("api-manifest"),
				Signature:       &lockfile.VerifiedSignature{Mode: "keyed", BundleDigest: fixtureDigest("api-bundle")},
			},
		},
		MappingLibraries: []lockfile.MappingLibrary{{
			URI:             "DemoMappings",
			ResolvedVersion: "1.0.0",
			Digest:          fixtureDigest("mappings"),
			Consumers:       []lockfile.LibraryConsumer{{ConsumerURI: "helm/api:1.0.0", Fragments: []string{"#/mappings/Values"}}},
		}},
		Plugins: []lockfile.Plugin{{URI: "governance/demo-labeler", ResolvedVersion: pluginPin, Digest: pluginPin}},
		SourceArtifacts: []lockfile.SourceArtifact{
			{
				Kind:          "git-repository",
				URL:           "https://example.com/modules.git",
				PinnedVersion: "0123456789abcdef0123456789abcdef01234567",
				Digest:        fixtureDigest("git-archive"),
				Mediatype:     "application/gzip",
				ReferencedBy:  []lockfile.SourceArtifactReference{{Entity: "recipe", URI: "terraform/db:1.0.0", Field: "spec.source"}},
			},
			{
				Kind:          "helm-https-chart",
				URL:           "https://charts.example.com/api-25.0.8.tgz",
				PinnedVersion: "25.0.8",
				Digest:        fixtureDigest("chart-archive"),
				Mediatype:     "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
				ReferencedBy:  []lockfile.SourceArtifactReference{{Entity: "recipe", URI: "helm/api:1.0.0", Field: "spec.source"}},
			},
		},
		ExternalInputs: []lockfile.ExternalInput{{
			RequiredBy: []string{"recipe.helm.api.spec.pipeline"},
			Sha256:     strings.TrimPrefix(fixtureDigest("values"), "sha256:"),
			Size:       2048,
			URL:        "https://example.com/values.yaml",
		}},
	}
}

// writePulledLockfile writes lf through the product's own writer, which checks
// nothing, so a row the loader refuses reaches the loader exactly as written.
func writePulledLockfile(t *testing.T, lf *lockfile.Lockfile) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "release.lock")
	if err := lockfile.WriteLockfile(path, lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}
	return path
}

// TestLoadLockfileAdmitsEveryFormAPullWrites is the positive control for the
// refusals below, and the claim in its own right that the shapes the loader holds
// a record to are the shapes the product writes. A pattern stricter than a writer
// would refuse the product's own lockfile, which is a regression no refusal row can
// see.
func TestLoadLockfileAdmitsEveryFormAPullWrites(t *testing.T) {
	t.Parallel()
	lf, _, err := lockfile.LoadLockfile(writePulledLockfile(t, pulledLockfile()))
	if err != nil {
		t.Fatalf("the loader refused a lockfile carrying only the forms a pull writes: %v", err)
	}
	if got := lf.Toolchain.CompilerVersion; got != "(devel)" {
		t.Errorf("compiler version read back as %q; want the (devel) marker", got)
	}
	if got := lf.SourceArtifacts[0].PinnedVersion; got != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("the git-repository pin read back as %q; want the full commit", got)
	}
	if got := lf.Entities[0].ResolvedVersion; got != "" {
		t.Errorf("the top-level entity's version read back as %q; want empty", got)
	}
	if got, want := lf.Plugins[0].ResolvedVersion, lf.Plugins[0].Digest; got != want {
		t.Errorf("the plugin row's version read back as %q; want its content digest %q", got, want)
	}
}

// TestLoadLockfileRefusesAVersionOrDigestThatIsNotOne drives one malformed value
// per constrained field class through the loader. Every row starts from the
// lockfile a pull writes and changes one field, so each refusal is attributable to
// that field alone; the control above is what shows the unchanged rows load.
//
// A version is held to a single path segment because it names a directory of the
// content cache: a value carrying a separator or a parent step would name a
// location outside it. A digest is held to the one spelling every comparison uses.
// Each refusal must name the file and the key, since the key is all a reader has to
// find the fault by.
func TestLoadLockfileRefusesAVersionOrDigestThatIsNotOne(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(lf *lockfile.Lockfile)
		key    string
	}{
		{
			name:   "an entity version that steps out of the cache",
			mutate: func(lf *lockfile.Lockfile) { lf.Entities[1].ResolvedVersion = "../../../../../etc" },
			key:    "entities[1].resolved_version",
		},
		{
			name:   "an entity version carrying a separator",
			mutate: func(lf *lockfile.Lockfile) { lf.Entities[1].ResolvedVersion = "1.0.0/x" },
			key:    "entities[1].resolved_version",
		},
		{
			name:   "an entity version that is a bare parent step",
			mutate: func(lf *lockfile.Lockfile) { lf.Entities[1].ResolvedVersion = ".." },
			key:    "entities[1].resolved_version",
		},
		{
			name:   "a library version carrying a backslash, a separator where the path is joined on Windows",
			mutate: func(lf *lockfile.Lockfile) { lf.MappingLibraries[0].ResolvedVersion = `1.0.0\x` },
			key:    "mapping_libraries[0].resolved_version",
		},
		{
			name:   "a source pin that steps out of the cache",
			mutate: func(lf *lockfile.Lockfile) { lf.SourceArtifacts[1].PinnedVersion = "../../../../x" },
			key:    "source_artifacts[1].pinned_version",
		},
		{
			name:   "a compiler version carrying a separator",
			mutate: func(lf *lockfile.Lockfile) { lf.Toolchain.CompilerVersion = "1.0.0/x" },
			key:    "toolchain.compiler_version",
		},
		{
			name:   "a core-plugin version carrying a backslash",
			mutate: func(lf *lockfile.Lockfile) { lf.Toolchain.CorePlugins[0].Version = `0.7.3\x` },
			key:    "toolchain.core_plugins[0].version",
		},
		{
			name:   "a plugin version that steps out of the cache instead of naming its content digest",
			mutate: func(lf *lockfile.Lockfile) { lf.Plugins[0].ResolvedVersion = "../../../x" },
			key:    "plugins[0].resolved_version",
		},
		{
			name:   "an entity digest shorter than a content digest",
			mutate: func(lf *lockfile.Lockfile) { lf.Entities[1].Digest = "sha256:abc" },
			key:    "entities[1].digest",
		},
		{
			name: "a manifest digest in uppercase hex",
			mutate: func(lf *lockfile.Lockfile) {
				lf.Entities[1].ManifestDigest = strings.ToUpper(fixtureDigest("api-manifest"))
			},
			key: "entities[1].manifest_digest",
		},
		{
			name: "a signature bundle digest that steps out of the cache",
			mutate: func(lf *lockfile.Lockfile) {
				lf.Entities[1].Signature.BundleDigest = "sha256:../../../../etc/passwd"
			},
			key: "entities[1].signature.bundle_digest",
		},
		{
			name:   "a negative external-input size",
			mutate: func(lf *lockfile.Lockfile) { lf.ExternalInputs[0].Size = -1 },
			key:    "external_inputs[0].size",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lf := pulledLockfile()
			tc.mutate(lf)
			path := writePulledLockfile(t, lf)
			_, _, err := lockfile.LoadLockfile(path)
			if err == nil {
				t.Fatalf("the loader admitted a lockfile with %s", tc.name)
			}
			if !errors.Is(err, lockfile.ErrLockfileInvalid()) {
				t.Errorf("refused, but not as an invalid lockfile: %v", err)
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("the refusal does not name the file %s: %v", path, err)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("the refusal does not name the key %s: %v", tc.key, err)
			}
		})
	}
}
