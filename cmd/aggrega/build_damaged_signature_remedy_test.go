// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// The command line's build names the pull that repairs a recorded signature fact it
// refuses, over the hermetic cache-served sub-aggregate: its row records a fact
// whose cached bundle is then removed, or altered, after the fact was recorded.

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestTheCommandLineNamesThePullThatRepairsADamagedSignatureBundle is the refusal as
// a reader meets it: the condition, the row, and the remedy naming the Release and
// its lockfile, in the error and in what the command prints.
func TestTheCommandLineNamesThePullThatRepairsADamagedSignatureBundle(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name   string
		damage func(t *testing.T, bundlePath string)
	}{
		{name: "the cached bundle removed", damage: func(t *testing.T, bundlePath string) {
			if err := os.Remove(bundlePath); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "the cached bundle altered", damage: func(t *testing.T, bundlePath string) {
			if err := os.WriteFile(bundlePath, []byte("rewritten after the pull"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root, releasePath, catalogDir := cacheServedSubAggregate(t)
			row.damage(t, recordSubAggregateFact(t, root, releasePath))

			err := buildRecordedSignature(t, root, releasePath, catalogDir, "auto_pull: false\n")
			if !errors.Is(err, oci.ErrSignatureBundleDigestMismatch()) {
				t.Fatalf("want the cached-bundle mismatch, got %v", err)
			}
			remedy := "remediation: run `aggrega pull --release " + releasePath + "` to verify the artifact's signature at " +
				"its registry again, record it in " + lockfile.PathFor(releasePath) + " and cache the bundle it names"
			for _, want := range []string{`artifact "` + recordedSignatureSub + `"`, remedy} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not carry %q:\n%v", want, err)
				}
			}
			if printed := formatCompilerError(err); !strings.Contains(printed, remedy) {
				t.Errorf("what the command prints does not name the pull:\n%s", printed)
			}
		})
	}
}
