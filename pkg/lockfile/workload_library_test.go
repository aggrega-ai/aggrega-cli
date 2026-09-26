// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

func workloadSampleLockfile(digest string) *lockfile.Lockfile {
	return &lockfile.Lockfile{
		Version:     1,
		Release:     "app",
		GeneratedAt: "2026-07-24T00:00:00Z",
		WorkloadLibraries: []lockfile.WorkloadLibrary{{
			URI:             "BaseWorkloads",
			ResolvedVersion: "1.0.0",
			Digest:          digest,
			Consumers: []lockfile.LibraryConsumer{{
				ConsumerURI: "compose/mariadb:1.0.0",
				Fragments:   []string{"#/workloads/SimpleService"},
			}},
		}},
	}
}

// TestVerifyWorkloadLibraryDrift proves the workload-library drift axis: a digest
// mismatch between the lockfile pin and the freshly-computed closure surfaces as
// ErrLockfileDrift naming the offending library URI.
func TestVerifyWorkloadLibraryDrift(t *testing.T) {
	t.Parallel()
	lf := workloadSampleLockfile("sha256:pinned")
	expected := closure.Closure{
		WorkloadLibraries: []closure.ResolvedLibrary{{
			URI:             "BaseWorkloads",
			ResolvedVersion: "1.0.0",
			Digest:          "sha256:drifted",
		}},
	}
	err := lockfile.Verify(lf, expected)
	if !errors.Is(err, lockfile.ErrLockfileDrift()) {
		t.Fatalf("expected ErrLockfileDrift for workload-library digest mismatch; got %v", err)
	}
	if !strings.Contains(err.Error(), "BaseWorkloads") {
		t.Errorf("expected drift error to name the library URI; got:\n%s", err.Error())
	}
}

// TestWriteLockfileWorkloadLibrariesDeterministic proves the workload_libraries
// section is a determinism surface (Invariant 8): two independent writes of the
// same in-memory lockfile produce byte-identical output, and the section round-trips.
func TestWriteLockfileWorkloadLibrariesDeterministic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pA := filepath.Join(dir, "a.lock")
	pA2 := filepath.Join(dir, "a2.lock")
	pinned := fixtureDigest("x")
	if err := lockfile.WriteLockfile(pA, workloadSampleLockfile(pinned)); err != nil {
		t.Fatalf("write A: %v", err)
	}
	if err := lockfile.WriteLockfile(pA2, workloadSampleLockfile(pinned)); err != nil {
		t.Fatalf("write A2: %v", err)
	}
	a, _ := os.ReadFile(pA)
	a2, _ := os.ReadFile(pA2)
	if string(a) != string(a2) {
		t.Fatalf("workload_libraries must write byte-identically:\n--- a ---\n%s\n--- a2 ---\n%s", a, a2)
	}

	got, _, err := lockfile.LoadLockfile(pA)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.WorkloadLibraries) != 1 || got.WorkloadLibraries[0].URI != "BaseWorkloads" {
		t.Fatalf("workload_libraries did not round-trip; got %+v", got.WorkloadLibraries)
	}
	if got.WorkloadLibraries[0].Digest != pinned {
		t.Errorf("workload-library digest did not round-trip; got %q", got.WorkloadLibraries[0].Digest)
	}
}
