// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build integration

package main

// Every Aggregate a pull fetches from a registry is held to its signature, the
// Release's own included. Aggregates are published to a real in-process registry,
// signed and unsigned, and consumers pull them under a signing requirement in both
// layouts — a top-level Aggregate beside the Release importing published
// sub-aggregates, and a top-level Aggregate the Release names by identity, itself
// importing published sub-aggregates, directly and through a further level. A
// signed Aggregate records its fact and the build holds its row to it; an unsigned
// one halts the pull.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// releaseAggregateDocument renders an Aggregate with the named components (each an
// instance of the published service) and the named sub-aggregates (each imported
// under its own name).
func releaseAggregateDocument(name string, components, subs []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\nmetadata:\n  name: %s\n  version: \"1.0.0\"\nspec:\n", name)
	if len(components) > 0 {
		b.WriteString("  components:\n")
		for _, c := range components {
			fmt.Fprintf(&b, "    %s:\n      type: test/svc\n", c)
		}
	}
	if len(subs) > 0 {
		b.WriteString("  aggregates:\n")
		for _, s := range subs {
			fmt.Fprintf(&b, "    %s:\n      type: %s\n", s, s)
		}
	}
	return b.String()
}

// publishAggregates pushes each Aggregate to host in order, signed with signer where
// the table says so, so every Aggregate a document imports is published before it.
func publishAggregates(t *testing.T, host string, signer signing.Signer, aggregates []publishedAggregate) {
	t.Helper()
	pubws := t.TempDir()
	writeWorkspaceConfig(t, pubws, "workspace:\n  catalog_dirs: []\noffline: false\ndefault_registry: \""+host+"\"\n")
	sign := true
	for _, a := range aggregates {
		path := filepath.Join(pubws, "aggregate", a.name, "1.0.0", "aggregate.yaml")
		writeFixture(t, path, releaseAggregateDocument(a.name, a.components, a.subs))
		cfg := pushConfig{Workspace: resolveTempWorkspace(t, tempWorkspaceOptions{Root: pubws}), EntityPath: path, Registry: host}
		if a.signed {
			cfg.Sign, cfg.Signer = &sign, signer
		}
		if err := pushRun(t.Context(), cfg); err != nil {
			t.Fatalf("publish Aggregate %s (signed=%t): %v", a.name, a.signed, err)
		}
	}
}

type publishedAggregate struct {
	name       string
	signed     bool
	components []string
	subs       []string
}

// TestIntegration_EveryAggregateAPullFetchesIsHeldToItsSignature is the matrix of
// both layouts, signed and unsigned, through the production pull and build.
func TestIntegration_EveryAggregateAPullFetchesIsHeldToItsSignature(t *testing.T) {
	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	host := startRegistry(t, nil)
	publishSignedService(t, host, signer)
	publishAggregates(t, host, signer, []publishedAggregate{
		{name: "shop", signed: true, components: []string{"app"}},
		{name: "shopu", components: []string{"app"}},
		{name: "inner", signed: true, components: []string{"db"}},
		{name: "inneru", components: []string{"db"}},
		{name: "outer", signed: true, subs: []string{"inner"}},
		{name: "outeru", signed: true, subs: []string{"inneru"}},
		{name: "mids", signed: true, subs: []string{"inner"}},
		{name: "mid", signed: true, subs: []string{"inneru"}},
		{name: "outermids", signed: true, subs: []string{"mids"}},
		{name: "outermid", signed: true, subs: []string{"mid"}},
	})

	rows := []struct {
		name string
		// aggregate is what the Release's spec.aggregate names; local, where set, is
		// the Aggregate document written beside the Release under that path.
		aggregate string
		local     string
		// components and routing place the composition in the Release's one boundary.
		components []string
		routing    string
		// held are the rows a signed pull must record a fact on and the build must
		// refuse once that fact is removed; an empty list means the pull must halt
		// because an Aggregate it fetched is unsigned.
		held []string
	}{
		{name: "an Aggregate the Release names by identity", aggregate: "shop", components: []string{"app"},
			held: []string{"shop"}},
		{name: "an unsigned Aggregate the Release names by identity", aggregate: "shopu", components: []string{"app"}},
		{name: "a sub-aggregate under an Aggregate beside the Release", aggregate: "./aggregate.yaml",
			local: releaseAggregateDocument("top", nil, []string{"inner"}), routing: "        inner: {}\n",
			held: []string{"inner"}},
		{name: "an unsigned sub-aggregate under an Aggregate beside the Release", aggregate: "./aggregate.yaml",
			local: releaseAggregateDocument("top", nil, []string{"inneru"}), routing: "        inneru: {}\n"},
		{name: "a sub-aggregate under an Aggregate the Release names by identity", aggregate: "outer",
			routing: "        inner: {}\n", held: []string{"outer", "inner"}},
		{name: "an unsigned sub-aggregate under an Aggregate the Release names by identity", aggregate: "outeru",
			routing: "        inneru: {}\n"},
		{name: "a sub-aggregate one level further down, beside the Release", aggregate: "./aggregate.yaml",
			local:   releaseAggregateDocument("top", nil, []string{"mids"}),
			routing: "        mids:\n          aggregates:\n            inner: {}\n", held: []string{"mids", "inner"}},
		{name: "an unsigned sub-aggregate one level further down, beside the Release", aggregate: "./aggregate.yaml",
			local:   releaseAggregateDocument("top", nil, []string{"mid"}),
			routing: "        mid:\n          aggregates:\n            inneru: {}\n"},
		{name: "a sub-aggregate one level further down, named by identity", aggregate: "outermids",
			routing: "        mids:\n          aggregates:\n            inner: {}\n", held: []string{"outermids", "mids", "inner"}},
		{name: "an unsigned sub-aggregate one level further down, named by identity", aggregate: "outermid",
			routing: "        mid:\n          aggregates:\n            inneru: {}\n"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws := t.TempDir()
			if row.local != "" {
				writeFixture(t, filepath.Join(ws, "aggregate.yaml"), row.local)
			}
			writeFixture(t, filepath.Join(ws, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/svc: helm/svc:1.0.0
`)
			var release strings.Builder
			fmt.Fprintf(&release, "apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: rel\n  environment: testing\n"+
				"  version: \"1.0.0\"\nspec:\n  aggregate: %s\n  manifest: ./manifest.yaml\n  boundaries:\n    main:\n      directory: ./dist/main\n",
				row.aggregate)
			if len(row.components) > 0 {
				release.WriteString("      components:\n")
				for _, c := range row.components {
					fmt.Fprintf(&release, "        %s:\n          infra:\n            replicas: 1\n", c)
				}
			}
			if row.routing != "" {
				release.WriteString("      aggregates:\n" + row.routing)
			}
			releasePath := filepath.Join(ws, "release.yaml")
			writeFixture(t, releasePath, release.String())
			writeWorkspaceConfig(t, ws, recordedSignatureStance{topRequired: true}.config(host, pubPEM))

			var pullOut, pullDiag bytes.Buffer
			err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}),
				ReleasePath: releasePath, Out: &pullOut, DiagOut: &pullDiag})
			if len(row.held) == 0 {
				if !errors.Is(err, signing.ErrNoSignatureFound()) {
					t.Fatalf("an unsigned Aggregate fetched under a requirement must halt the pull; got %v\n%s", err, pullDiag.String())
				}
				return
			}
			if err != nil {
				t.Fatalf("pull under a requirement: %v\n%s", err, pullDiag.String())
			}

			lockPath := lockfile.PathFor(releasePath)
			pristine, err := os.ReadFile(lockPath)
			if err != nil {
				t.Fatalf("read the lockfile the pull wrote: %v", err)
			}
			lf, _, err := lockfile.LoadLockfile(lockPath)
			if err != nil {
				t.Fatalf("load the lockfile the pull wrote: %v", err)
			}
			for _, uri := range row.held {
				var fact *lockfile.VerifiedSignature
				for _, e := range lf.Entities {
					if e.URI == uri {
						fact = e.Signature
					}
				}
				if fact == nil || fact.Mode != "keyed" || fact.BundleDigest == "" {
					t.Errorf("the pull fetched the signed Aggregate %q and recorded no verified signature for it: %+v", uri, fact)
				}
			}
			var out, diag bytes.Buffer
			if err := dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), releasePath, nil, false, &diag, &out); err != nil {
				t.Fatalf("the build over the lockfile the pull wrote must go ahead: %v", err)
			}

			// Each row the pull recorded a fact on is held to it: the same lockfile
			// with that one row's fact removed is refused, naming the row.
			for _, uri := range row.held {
				t.Run("the row "+uri+" without its signature", func(t *testing.T) {
					edited, _, err := lockfile.LoadLockfile(lockPath)
					if err != nil {
						t.Fatalf("load the lockfile: %v", err)
					}
					for i := range edited.Entities {
						if edited.Entities[i].URI == uri {
							edited.Entities[i].Signature = nil
						}
					}
					if err := lockfile.WriteLockfile(lockPath, edited); err != nil {
						t.Fatalf("write the edited lockfile: %v", err)
					}
					t.Cleanup(func() { _ = os.WriteFile(lockPath, pristine, 0o644) })
					var out, diag bytes.Buffer
					err = dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), releasePath, nil, false, &diag, &out)
					if !errors.Is(err, lockfile.ErrSignatureNotRecorded()) {
						t.Fatalf("want %v, got %v", lockfile.ErrSignatureNotRecorded(), err)
					}
					if !strings.Contains(err.Error(), `"`+uri+`"`) {
						t.Errorf("the refusal does not name %q:\n%v", uri, err)
					}
				})
			}
		})
	}
}
