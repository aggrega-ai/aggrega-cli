// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package readscope_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
)

// TestCheckOverlayReadIsTheOneJudgementWithItsOwnRemedy drives the overlay read entry
// over one root, one fault per row beside controls: an overlay in the overlay
// directory, a link to a file elsewhere inside the root, an overlay that is a link
// leading out, and a directory link leading out. Its verdict must be CheckRead's on
// every row — it is the one judgement, not a second one — and a refusal names the
// link, the root, and the remedy an overlay has rather than a catalog directory's.
func TestCheckOverlayReadIsTheOneJudgementWithItsOwnRemedy(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(root, "overlays", "cel.yaml"))
	mustWrite(t, filepath.Join(root, "kept", "rego.yaml"))
	mustWrite(t, filepath.Join(outside, "helm.yaml"))
	mustWrite(t, filepath.Join(outside, "more", "hcl.yaml"))
	mustLinkBeneath(t, filepath.Join(root, "kept", "rego.yaml"), filepath.Join(root, "overlays", "rego.yaml"))
	mustLinkBeneath(t, filepath.Join(outside, "helm.yaml"), filepath.Join(root, "overlays", "helm.yaml"))
	mustLinkBeneath(t, filepath.Join(outside, "more"), filepath.Join(root, "overlays", "more"))
	for _, row := range []struct {
		name string
		path string
		// link and leadsTo are what a refusal names, both empty for a row that reads.
		link, leadsTo string
	}{
		{name: "control: an overlay in the overlay directory", path: filepath.Join(root, "overlays", "cel.yaml")},
		{name: "control: a link to an overlay elsewhere inside the root", path: filepath.Join(root, "overlays", "rego.yaml")},
		{name: "an overlay that is a link leading out",
			path: filepath.Join(root, "overlays", "helm.yaml"),
			link: filepath.Join(root, "overlays", "helm.yaml"), leadsTo: filepath.Join(outside, "helm.yaml")},
		{name: "a directory link leading out",
			path: filepath.Join(root, "overlays", "more"),
			link: filepath.Join(root, "overlays", "more"), leadsTo: filepath.Join(outside, "more")},
	} {
		t.Run(row.name, func(t *testing.T) {
			err := readscope.CheckOverlayRead(root, row.path)
			if general := readscope.CheckRead(root, row.path); (general == nil) != (err == nil) {
				t.Fatalf("the verdict must be CheckRead's; CheckRead %v, CheckOverlayRead %v", general, err)
			}
			if row.link == "" {
				if err != nil {
					t.Fatalf("refused a read the root holds: %v", err)
				}
				return
			}
			if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
				t.Fatalf("want the read-leads-out-of-workspace halt, got %v", err)
			}
			if code, _ := cerrors.ShortCodeOf(err); code != "E0321" {
				t.Errorf("short code = %q, want E0321", code)
			}
			for _, want := range []string{
				"the link " + row.link + " leads to " + resolved(t, row.leadsTo),
				"which must stay inside " + root,
				"Overlays are read from beneath the overlay directory",
				"keep the overlay itself there",
				"name a directory holding every overlay as the overlay directory",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal must carry %q:\n%v", want, err)
				}
			}
			if strings.Contains(err.Error(), "as a catalog directory") {
				t.Errorf("listing a catalog directory reaches no overlay, so the remedy must not offer one:\n%v", err)
			}
		})
	}
	if err := readscope.CheckOverlayRead("", filepath.Join(root, "overlays", "helm.yaml")); err != nil {
		t.Errorf("with no root nothing is held; got %v", err)
	}
}
