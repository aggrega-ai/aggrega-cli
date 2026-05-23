// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

func TestVerifyAcceptsMatchingClosure(t *testing.T) {
	t.Parallel()
	lf := &lockfile.Lockfile{
		Entities: []lockfile.Entity{
			{URI: "core/api-server", ResolvedVersion: "1.2.3", Digest: "sha256:aaa"},
			{URI: "core/database", ResolvedVersion: "2.0.0", Digest: "sha256:bbb"},
		},
		SchemaLibraries: []lockfile.SchemaLibrary{
			{URI: "core/Probes", ResolvedVersion: "1.0.0", Digest: "sha256:ccc"},
		},
		MappingLibraries: []lockfile.MappingLibrary{
			{URI: "core/BaseMappings", ResolvedVersion: "1.0.0", Digest: "sha256:ddd"},
		},
		WasmPlugins: []lockfile.WasmPlugin{
			{URI: "core/mutator", ResolvedVersion: "0.1.0", Digest: "sha256:eee"},
		},
	}
	expected := closure.Closure{
		Entities: []closure.ResolvedRef{
			{URI: "core/api-server", ResolvedVersion: "1.2.3", Digest: "sha256:aaa"},
			{URI: "core/database", ResolvedVersion: "2.0.0", Digest: "sha256:bbb"},
		},
		SchemaLibraries: []closure.ResolvedLibrary{
			{URI: "core/Probes", ResolvedVersion: "1.0.0", Digest: "sha256:ccc"},
		},
		MappingLibraries: []closure.ResolvedLibrary{
			{URI: "core/BaseMappings", ResolvedVersion: "1.0.0", Digest: "sha256:ddd"},
		},
		WasmPlugins: []closure.ResolvedRef{
			{URI: "core/mutator", ResolvedVersion: "0.1.0", Digest: "sha256:eee"},
		},
	}
	if err := lockfile.Verify(lf, expected); err != nil {
		t.Errorf("Verify on matching closure should return nil; got %v", err)
	}
}

func TestVerifyDetectsEntityDigestMismatch(t *testing.T) {
	t.Parallel()
	lf := &lockfile.Lockfile{
		Entities: []lockfile.Entity{
			{URI: "core/api-server", ResolvedVersion: "1.2.3", Digest: "sha256:aaa"},
		},
	}
	expected := closure.Closure{
		Entities: []closure.ResolvedRef{
			{URI: "core/api-server", ResolvedVersion: "1.2.3", Digest: "sha256:differs"},
		},
	}
	err := lockfile.Verify(lf, expected)
	if err == nil {
		t.Fatal("expected ErrLockfileDrift for digest mismatch; got nil")
	}
	if !errors.Is(err, lockfile.ErrLockfileDrift) {
		t.Errorf("error should wrap ErrLockfileDrift; got %v", err)
	}
}

func TestVerifyDetectsMissingEntityInLockfile(t *testing.T) {
	t.Parallel()
	lf := &lockfile.Lockfile{Entities: []lockfile.Entity{}}
	expected := closure.Closure{
		Entities: []closure.ResolvedRef{
			{URI: "core/api-server", ResolvedVersion: "1.2.3", Digest: "sha256:aaa"},
		},
	}
	err := lockfile.Verify(lf, expected)
	if !errors.Is(err, lockfile.ErrLockfileDrift) {
		t.Errorf("missing-entity error should wrap ErrLockfileDrift; got %v", err)
	}
}

func TestVerifyDetectsExtraEntityInLockfile(t *testing.T) {
	t.Parallel()
	lf := &lockfile.Lockfile{
		Entities: []lockfile.Entity{
			{URI: "core/api-server", ResolvedVersion: "1.2.3", Digest: "sha256:aaa"},
			{URI: "core/orphan", ResolvedVersion: "9.9.9", Digest: "sha256:orphan"},
		},
	}
	expected := closure.Closure{
		Entities: []closure.ResolvedRef{
			{URI: "core/api-server", ResolvedVersion: "1.2.3", Digest: "sha256:aaa"},
		},
	}
	err := lockfile.Verify(lf, expected)
	if !errors.Is(err, lockfile.ErrLockfileDrift) {
		t.Errorf("extra-lockfile-entry error should wrap ErrLockfileDrift; got %v", err)
	}
}

// TestVerifyReportsAllDriftEntriesAcrossAxes pins that when multiple
// drifts exist (within an axis AND across axes), Verify surfaces
// every offending URI in one joined diagnostic rather than reporting
// only the first match. Operators reading the error see the full
// picture in a single output.
func TestVerifyReportsAllDriftEntriesAcrossAxes(t *testing.T) {
	t.Parallel()
	lf := &lockfile.Lockfile{
		Entities: []lockfile.Entity{
			{URI: "core/orphan-1", ResolvedVersion: "1.0.0", Digest: "sha256:o1"},
			{URI: "core/orphan-2", ResolvedVersion: "1.0.0", Digest: "sha256:o2"},
		},
		WasmPlugins: []lockfile.WasmPlugin{
			{URI: "core/plugin-stale", ResolvedVersion: "0.1.0", Digest: "sha256:stale"},
		},
	}
	expected := closure.Closure{
		Entities: []closure.ResolvedRef{
			{URI: "core/new-entity", ResolvedVersion: "1.0.0", Digest: "sha256:new"},
		},
		WasmPlugins: []closure.ResolvedRef{
			{URI: "core/plugin-stale", ResolvedVersion: "0.2.0", Digest: "sha256:new-plugin"},
		},
	}
	err := lockfile.Verify(lf, expected)
	if !errors.Is(err, lockfile.ErrLockfileDrift) {
		t.Fatalf("expected ErrLockfileDrift; got %v", err)
	}
	msg := err.Error()
	for _, expectedURI := range []string{"core/orphan-1", "core/orphan-2", "core/new-entity", "core/plugin-stale"} {
		if !strings.Contains(msg, expectedURI) {
			t.Errorf("expected error to name URI %q; got:\n%s", expectedURI, msg)
		}
	}
}
