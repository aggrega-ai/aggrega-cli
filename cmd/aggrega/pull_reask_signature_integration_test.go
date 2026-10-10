// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

// A re-pull over a lockfile that has lost a required signature fact. The Component
// and its Recipe are published signed to a real in-process registry and pulled
// under a requirement, so every row records its verified fact and the content
// cache holds every artifact. Each case then edits the lockfile the pull wrote, or
// the configuration, in one place and runs the pull — or a build that fills its
// lockfile — again over that warm cache. The registry counts the signature lookups
// it is asked for, so a case that must ask nothing is held to that as well as to
// what it writes.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// isSignatureLookup reports whether a registry request lists an artifact's
// referrers: the referrers endpoint, or the tag a registry without one keeps the
// list under. Every signature and deprecation-marker lookup a pull makes is one of
// the two.
func isSignatureLookup(r *http.Request) bool {
	return strings.Contains(r.URL.Path, "/referrers/") || strings.Contains(r.URL.Path, "/manifests/sha256-")
}

// TestIntegration_ARePullRecordsAgainASignatureFactTheLockfileLost is the matrix.
func TestIntegration_ARePullRecordsAgainASignatureFactTheLockfileLost(t *testing.T) {
	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	var lookups atomic.Int64
	host := startRegistry(t, func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isSignatureLookup(r) {
				lookups.Add(1)
			}
			h.ServeHTTP(w, r)
		})
	})
	publishSignedService(t, host, signer)

	ws, releasePath := writeSignedServiceConsumer(t)
	lockPath := lockfile.PathFor(releasePath)
	writeWorkspaceConfig(t, ws, recordedSignatureStance{topRequired: true}.config(host, pubPEM))
	var pullOut, pullDiag bytes.Buffer
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}),
		ReleasePath: releasePath, Out: &pullOut, DiagOut: &pullDiag}); err != nil {
		t.Fatalf("pull the signed service under a requirement: %v\n%s", err, pullDiag.String())
	}
	pristine, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read the lockfile the pull wrote: %v", err)
	}
	if componentFact(t, lockPath) == nil {
		t.Fatalf("the pull recorded no signature for %q, so no case below starts from a recorded fact:\n%s",
			recordedSignatureComponent, pristine)
	}

	// stripped writes the pristine lockfile with the Component's row edited by edit,
	// and returns the bytes written.
	stripped := func(t *testing.T, edit func(*lockfile.Entity)) []byte {
		t.Helper()
		lf, _, err := lockfile.LoadLockfile(lockPath)
		if err != nil {
			t.Fatalf("load the lockfile: %v", err)
		}
		for i := range lf.Entities {
			if lf.Entities[i].URI == recordedSignatureComponent {
				edit(&lf.Entities[i])
			}
		}
		if err := lockfile.WriteLockfile(lockPath, lf); err != nil {
			t.Fatalf("write the edited lockfile: %v", err)
		}
		written, err := os.ReadFile(lockPath)
		if err != nil {
			t.Fatalf("read the edited lockfile: %v", err)
		}
		return written
	}
	stripSignature := func(e *lockfile.Entity) { e.Signature = nil }
	stripSignatureAndManifest := func(e *lockfile.Entity) {
		e.Signature = nil
		e.ManifestDigest = ""
	}

	type outcome int
	const (
		// repaired: the run completes, writes back the lockfile the first pull
		// wrote — every byte but the time it was written, which a run that changes
		// the file stamps afresh — and asked the registry.
		repaired outcome = iota
		// untouched: the run completes, asks the registry nothing, and leaves the
		// lockfile as the case wrote it.
		untouched
		// refused: the run halts with the case's condition and leaves the lockfile
		// as the case wrote it.
		refused
	)
	rows := []struct {
		name   string
		stance recordedSignatureStance
		edit   func(*lockfile.Entity)
		// build runs a build — which fills its lockfile where the stance asks for
		// one — instead of a pull.
		build bool
		want  outcome
		cond  error
	}{
		{name: "a re-pull over the lockfile as the pull wrote it asks nothing",
			stance: recordedSignatureStance{topRequired: true}, want: untouched},
		{name: "a re-pull over the Component's removed signature asks and writes it back",
			stance: recordedSignatureStance{topRequired: true}, edit: stripSignature, want: repaired},
		{name: "removing the manifest digest too, the re-pull still writes both back",
			stance: recordedSignatureStance{topRequired: true}, edit: stripSignatureAndManifest, want: repaired},
		{name: "under the registry's own requirement the re-pull writes it back",
			stance: recordedSignatureStance{registryRequired: true}, edit: stripSignature, want: repaired},
		{name: "an advisory registry leaves the row as it is",
			stance: recordedSignatureStance{registryRequired: true, advisory: true}, edit: stripSignature, want: untouched},
		{name: "with no requirement the row is left as it is",
			stance: recordedSignatureStance{}, edit: stripSignature, want: untouched},
		{name: "offline, the re-pull refuses and writes nothing",
			stance: recordedSignatureStance{topRequired: true, offline: true}, edit: stripSignature,
			want: refused, cond: egress.ErrOfflineEgress()},
		{name: "a build that fills its lockfile writes it back and builds",
			stance: recordedSignatureStance{topRequired: true, autoPull: true}, edit: stripSignature,
			build: true, want: repaired},
		{name: "offline, a build that fills its lockfile refuses at the fill",
			stance: recordedSignatureStance{topRequired: true, autoPull: true, offline: true}, edit: stripSignature,
			build: true, want: refused, cond: egress.ErrOfflineEgress()},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if err := os.WriteFile(lockPath, pristine, 0o644); err != nil {
				t.Fatalf("restore the pristine lockfile: %v", err)
			}
			before := pristine
			if row.edit != nil {
				before = stripped(t, row.edit)
			}
			writeWorkspaceConfig(t, ws, row.stance.config(host, pubPEM))
			lookups.Store(0)

			var out, diag bytes.Buffer
			rw := resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws})
			if row.build {
				err = dispatchBuildInRoot(t, rw, releasePath, nil, false, &diag, &out)
			} else {
				err = pullRun(context.Background(), pullConfig{ws: rw, ReleasePath: releasePath, Out: &out, DiagOut: &diag})
			}
			after, rerr := os.ReadFile(lockPath)
			if rerr != nil {
				t.Fatalf("read the lockfile after the run: %v", rerr)
			}

			switch row.want {
			case repaired:
				if err != nil {
					t.Fatalf("the run must complete: %v\n%s", err, diag.String())
				}
				if lookups.Load() == 0 {
					t.Error("the run asked the registry nothing, so whatever it wrote was not verified by it")
				}
				if !bytes.Equal(unstamped(t, after), unstamped(t, pristine)) {
					t.Errorf("the run did not write back the lockfile the first pull wrote.\nwant:\n%s\ngot:\n%s", pristine, after)
				}
				if componentFact(t, lockPath) == nil {
					t.Error("the Component's row records no signature after the run")
				}
			case untouched:
				if err != nil {
					t.Fatalf("the run must complete: %v\n%s", err, diag.String())
				}
				if n := lookups.Load(); n != 0 {
					t.Errorf("the run made %d signature lookups for rows it must leave as they are", n)
				}
				if !bytes.Equal(after, before) {
					t.Errorf("the run changed the lockfile it must leave as it is.\nwant:\n%s\ngot:\n%s", before, after)
				}
			case refused:
				if !errors.Is(err, row.cond) {
					t.Fatalf("want %v, got %v", row.cond, err)
				}
				if !bytes.Equal(after, before) {
					t.Errorf("a refused run changed the lockfile.\nwant:\n%s\ngot:\n%s", before, after)
				}
			}
		})
	}
}

// unstamped returns the canonical bytes of the lockfile b holds with its
// generated_at stamp cleared, written through the lockfile's own serializer, so two
// lockfiles compare equal exactly when they record the same thing.
func unstamped(t *testing.T, b []byte) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "release.lock")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("stage the lockfile: %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(path)
	if err != nil {
		t.Fatalf("load the lockfile: %v", err)
	}
	lf.GeneratedAt = ""
	if err := lockfile.WriteLockfile(path, lf); err != nil {
		t.Fatalf("write the unstamped lockfile: %v", err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the unstamped lockfile: %v", err)
	}
	return out
}

// componentFact returns the signature fact the lockfile at lockPath records on the
// Component's row, or nil.
func componentFact(t *testing.T, lockPath string) *lockfile.VerifiedSignature {
	t.Helper()
	lf, _, err := lockfile.LoadLockfile(lockPath)
	if err != nil {
		t.Fatalf("load the lockfile: %v", err)
	}
	for _, e := range lf.Entities {
		if e.URI == recordedSignatureComponent {
			return e.Signature
		}
	}
	return nil
}
