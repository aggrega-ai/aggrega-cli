// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package readscope_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
)

// TestCheckCacheReadIsTheOneJudgementWithItsOwnRemedy drives the cache's read entry
// over one root, one fault per row beside controls: a file in the cache, a link to a
// file elsewhere inside the root, a cache directory that is a link leading out, and a
// file of the cache that is a link leading out. Its verdict must be CheckRead's on
// every row — it is the one judgement, not a second one — and a refusal names the
// link, the root, and the remedy the cache has rather than a catalog directory's.
func TestCheckCacheReadIsTheOneJudgementWithItsOwnRemedy(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(root, "cache", "entities", "a", "1.0.0", "blob"))
	mustWrite(t, filepath.Join(root, "kept", "blob"))
	mustWrite(t, filepath.Join(outside, "blob"))
	mustWrite(t, filepath.Join(outside, "cache", "entities", "b", "1.0.0", "blob"))
	mustLinkBeneath(t, filepath.Join(root, "kept", "blob"), filepath.Join(root, "cache", "entities", "inside", "1.0.0", "blob"))
	mustLinkBeneath(t, filepath.Join(outside, "blob"), filepath.Join(root, "cache", "entities", "out", "1.0.0", "blob"))
	mustLinkBeneath(t, filepath.Join(outside, "cache"), filepath.Join(root, "linked-cache"))
	for _, row := range []struct {
		name string
		path string
		// link and leadsTo are what a refusal names, both empty for a row that reads.
		link, leadsTo string
	}{
		{name: "control: a file in the cache", path: filepath.Join(root, "cache", "entities", "a", "1.0.0", "blob")},
		{name: "control: a link to a file elsewhere inside the root", path: filepath.Join(root, "cache", "entities", "inside", "1.0.0", "blob")},
		{name: "control: a file not there yet", path: filepath.Join(root, "cache", "entities", "c", "1.0.0", "blob")},
		{name: "a file of the cache that is a link leading out",
			path: filepath.Join(root, "cache", "entities", "out", "1.0.0", "blob"),
			link: filepath.Join(root, "cache", "entities", "out", "1.0.0", "blob"), leadsTo: filepath.Join(outside, "blob")},
		{name: "a cache directory that is a link leading out",
			path: filepath.Join(root, "linked-cache", "entities", "b", "1.0.0", "blob"),
			link: filepath.Join(root, "linked-cache"), leadsTo: filepath.Join(outside, "cache")},
	} {
		t.Run(row.name, func(t *testing.T) {
			err := readscope.CheckCacheRead(root, row.path)
			if general := readscope.CheckRead(root, row.path); (general == nil) != (err == nil) {
				t.Fatalf("the verdict must be CheckRead's; CheckRead %v, CheckCacheRead %v", general, err)
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
				"the artifact cache is read only from inside the directory it is kept in",
				"a pull fills it again",
				"artifact_cache_dir",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal must carry %q:\n%v", want, err)
				}
			}
			if strings.Contains(err.Error(), "as a catalog directory") {
				t.Errorf("listing a catalog directory reaches nothing in the cache, so the remedy must not offer one:\n%v", err)
			}
		})
	}
	if err := readscope.CheckCacheRead("", filepath.Join(root, "linked-cache", "entities")); err != nil {
		t.Errorf("with no root nothing is held; got %v", err)
	}
}

// mustLinkBeneath makes link a symbolic link to target, creating the directory
// holding it first.
func mustLinkBeneath(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(link), err)
	}
	mustLink(t, target, link)
}
