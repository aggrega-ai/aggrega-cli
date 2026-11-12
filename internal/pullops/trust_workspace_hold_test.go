// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pullops

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// TestThePluginTrustRecordIsHeldToTheWorkspace drives the write of the record
// beside the lockfile, and its report, over one resolution that moves the record,
// with the record's place inside the workspace being, one row at a time, empty, a
// link to a file outside, and a link leading nowhere outside.
//
// A refused row is held to the file the link leads to as well as to the
// condition: the file outside must hold what it held, and a file that was never
// there must still not be there.
func TestThePluginTrustRecordIsHeldToTheWorkspace(t *testing.T) {
	const acquired = "linter/acquired-probe"
	resolutions := []verdictPluginResolution{
		{BareURI: acquired, Coordinate: acquired, Version: "2.0.0", Digest: "sha256:" + strings.Repeat("b", 64)},
	}
	rows := []struct {
		name    string
		layout  func(t *testing.T, record, outside string) string
		refused bool
	}{
		{name: "control: no record yet", layout: func(*testing.T, string, string) string { return "" }},
		{name: "a record that is a link to a record outside", refused: true, layout: func(t *testing.T, record, outside string) string {
			target := filepath.Join(outside, "trust.yaml")
			// A well-formed record holding nothing, so the run reads it, finds the
			// resolution new, and reaches the write.
			empty, err := pluginstrust.CanonicalBytes(&pluginstrust.Trust{Version: pluginstrust.CurrentVersion})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, empty, 0o644); err != nil {
				t.Fatal(err)
			}
			symlinkOrSkipTrust(t, target, record)
			return target
		}},
		{name: "a record that is a link leading nowhere outside", refused: true, layout: func(t *testing.T, record, outside string) string {
			target := filepath.Join(outside, "absent.yaml")
			symlinkOrSkipTrust(t, target, record)
			return target
		}},
	}
	for _, row := range rows {
		for _, report := range []bool{false, true} {
			name := row.name + " (write)"
			if report {
				name = row.name + " (report)"
			}
			t.Run(name, func(t *testing.T) {
				root, outside := t.TempDir(), t.TempDir()
				releasePath := filepath.Join(root, "release.yaml")
				record := pluginstrust.PathFor(releasePath)
				target := row.layout(t, record, outside)
				var before []byte
				if target != "" {
					before, _ = os.ReadFile(target)
				}

				var err error
				if report {
					_, _, err = reportVerdictPluginTrust(root, releasePath, attachingVerdictPlugins(acquired), resolutions, catalog.PinReuseMode{})
				} else {
					_, err = recordVerdictPluginTrust(root, releasePath, attachingVerdictPlugins(acquired), resolutions, catalog.PinReuseMode{}, io.Discard)
				}

				if !row.refused {
					if err != nil {
						t.Fatalf("refused a record the workspace holds: %v", err)
					}
					return
				}
				if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
					t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
				}
				after, aerr := os.ReadFile(target)
				switch {
				case before == nil && !os.IsNotExist(aerr):
					t.Errorf("a record was created outside the workspace at %s", target)
				case before != nil && string(after) != string(before):
					t.Errorf("the file outside the workspace was rewritten:\n%s", after)
				}
			})
		}
	}
}

func symlinkOrSkipTrust(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}
