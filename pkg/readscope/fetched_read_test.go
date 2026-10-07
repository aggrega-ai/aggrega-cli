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

// TestCheckFetchedReadIsTheOneJudgementWithItsOwnRemedy drives the read entry a
// fetched repository's archive asks, over one checkout, one fault per row beside
// controls: a file in the checkout, a link to a file elsewhere inside it, a file
// link leading out, and a directory link leading out. Its verdict must be
// CheckRead's on every row — it is the one judgement, not a second one — and a
// refusal names the link, the checkout, and the remedy a link committed in a
// repository has, rather than a catalog directory's.
func TestCheckFetchedReadIsTheOneJudgementWithItsOwnRemedy(t *testing.T) {
	checkout := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(checkout, "chart", "Chart.yaml"))
	mustWrite(t, filepath.Join(checkout, "shared", "values.yaml"))
	mustWrite(t, filepath.Join(outside, "secret.txt"))
	mustWrite(t, filepath.Join(outside, "dir", "Chart.yaml"))
	mustLinkBeneath(t, filepath.Join(checkout, "shared", "values.yaml"), filepath.Join(checkout, "chart", "values.yaml"))
	mustLinkBeneath(t, filepath.Join(outside, "secret.txt"), filepath.Join(checkout, "chart", "secret.txt"))
	mustLinkBeneath(t, filepath.Join(outside, "dir"), filepath.Join(checkout, "chart", "linked"))
	for _, row := range []struct {
		name string
		path string
		// link and leadsTo are what a refusal names, both empty for a row that reads.
		link, leadsTo string
	}{
		{name: "control: a file in the checkout", path: filepath.Join(checkout, "chart", "Chart.yaml")},
		{name: "control: a link to a file elsewhere inside the checkout", path: filepath.Join(checkout, "chart", "values.yaml")},
		{name: "a file link leading out of the checkout",
			path: filepath.Join(checkout, "chart", "secret.txt"),
			link: filepath.Join(checkout, "chart", "secret.txt"), leadsTo: filepath.Join(outside, "secret.txt")},
		{name: "a directory link leading out of the checkout",
			path: filepath.Join(checkout, "chart", "linked"),
			link: filepath.Join(checkout, "chart", "linked"), leadsTo: filepath.Join(outside, "dir")},
	} {
		t.Run(row.name, func(t *testing.T) {
			err := readscope.CheckFetchedRead(checkout, row.path)
			if general := readscope.CheckRead(checkout, row.path); (general == nil) != (err == nil) {
				t.Fatalf("the verdict must be CheckRead's; CheckRead %v, CheckFetchedRead %v", general, err)
			}
			if row.link == "" {
				if err != nil {
					t.Fatalf("refused a read the checkout holds: %v", err)
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
				"which must stay inside " + checkout,
				"nothing was cached",
				"committed in the repository the source names",
				"name the revision that carries the change",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal must carry %q:\n%v", want, err)
				}
			}
			if strings.Contains(err.Error(), "as a catalog directory") {
				t.Errorf("listing a catalog directory changes nothing in a fetched repository, so the remedy must not offer one:\n%v", err)
			}
		})
	}
	if err := readscope.CheckFetchedRead("", filepath.Join(checkout, "chart", "secret.txt")); err != nil {
		t.Errorf("with no root nothing is held; got %v", err)
	}
}
