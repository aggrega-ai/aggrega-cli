// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasm

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/sourcewalk"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestTheLoadComparesRecordedIgnoreRulesOnlyWhereTheEntityRecordsThem drives the
// contract cross-check over the source_ignore declaration: an entity recording
// none makes no claim, one recording the plugin's own rules agrees, and one whose
// record differs — in a pattern, in the order of two patterns, or in the ignore
// files — is refused under the contract-mismatch condition. A cross-check that
// compared nothing would pass the three refusing rows; one that required the
// record would refuse the first.
func TestTheLoadComparesRecordedIgnoreRulesOnlyWhereTheEntityRecordsThem(t *testing.T) {
	reported := &SourceIgnore{Patterns: []string{".git/", "templates/.?*"}, Files: []string{".helmignore"}}
	rows := []struct {
		name    string
		claim   *SourceIgnore
		refused bool
	}{
		{name: "an entity recording no rules makes no claim", claim: nil},
		{name: "an entity recording the plugin's own rules agrees",
			claim: &SourceIgnore{Patterns: []string{".git/", "templates/.?*"}, Files: []string{".helmignore"}}},
		{name: "a record naming another pattern is refused", refused: true,
			claim: &SourceIgnore{Patterns: []string{".git/"}, Files: []string{".helmignore"}}},
		{name: "a record listing the patterns in another order is refused", refused: true,
			claim: &SourceIgnore{Patterns: []string{"templates/.?*", ".git/"}, Files: []string{".helmignore"}}},
		{name: "a record naming other ignore files is refused", refused: true,
			claim: &SourceIgnore{Patterns: []string{".git/", "templates/.?*"}}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			want := ProviderContract{BoundaryAssembly: "none", SourceIgnore: row.claim}
			got := ProviderContract{BoundaryAssembly: "none", SourceIgnore: reported}
			err := crossCheckProviderContract("provider/example:1.0.0", want, got)
			switch {
			case row.refused && !errors.Is(err, ErrWasmContractMismatch()):
				t.Fatalf("cross-check = %v; want the contract-mismatch refusal", err)
			case row.refused && !strings.Contains(err.Error(), "source_ignore"):
				t.Errorf("the refusal does not name the declaration: %v", err)
			case !row.refused && err != nil:
				t.Fatalf("cross-check refused an agreeing or absent claim: %v", err)
			}
		})
	}
}

// TestAGuestIsPackedOnlyWhatTheRecordedRulesKeep holds the pack and read host
// functions to the source scope a render sets: a tree omits what the rules leave
// out, a directory's files likewise, a file the rules leave out answers as not
// there and names itself, and a link is refused. Each row plants one fault; a pack
// that ignored the scope would return .git/HEAD in the tree row and the excluded
// file in the file row.
func TestAGuestIsPackedOnlyWhatTheRecordedRulesKeep(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "chart")
	for rel, body := range map[string]string{
		"Chart.yaml": "apiVersion: v2\n", "notes.bak": "x\n", ".git/HEAD": "ref\n", "templates/cm.yaml": "kind: ConfigMap\n",
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rules := []types.IgnoreRule{
		{Pattern: ".git/", Within: "chart", From: types.IgnoreRuleFromPlugin},
		{Pattern: "*.bak", Within: ".", From: sourcewalk.AuthorIgnoreFile},
	}
	p := &Plugin{}
	p.SetSource(root, sourcewalk.NewScope(base, rules))

	tree, _, err := p.packSourceSelection("tree", "")
	if err != nil {
		t.Fatalf("tree pack: %v", err)
	}
	var paths []string
	for _, f := range tree.Files {
		paths = append(paths, f.Path)
	}
	if want := []string{"Chart.yaml", "templates/cm.yaml"}; !reflect.DeepEqual(paths, want) {
		t.Errorf("tree pack = %q; want %q", paths, want)
	}

	dir, _, err := p.packSourceSelection("dir", "")
	if err != nil {
		t.Fatalf("dir pack: %v", err)
	}
	paths = nil
	for _, f := range dir.Files {
		paths = append(paths, f.Path)
	}
	if want := []string{"Chart.yaml"}; !reflect.DeepEqual(paths, want) {
		t.Errorf("dir pack = %q; want %q", paths, want)
	}

	_, code, err := p.packSourceSelection("file", "notes.bak")
	if err == nil || code != abi.CodeSourceReadFailed || !strings.Contains(err.Error(), `"notes.bak"`) {
		t.Errorf("file pack of an excluded file = %v (code %q); want a read failure naming notes.bak", err, code)
	}

	if err := os.Symlink(filepath.Join(root, "Chart.yaml"), filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.packSourceSelection("tree", ""); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("tree pack over a link = %v; want the link refused", err)
	}
	if _, _, err := p.packSourceSelection("file", "linked"); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("file pack of a link = %v; want the link refused", err)
	}

	// SetSourceRoot clears the scope, so a rule list never outlives its node.
	p.SetSourceRoot(root)
	if err := os.Remove(filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	tree, _, err = p.packSourceSelection("tree", "")
	if err != nil {
		t.Fatalf("tree pack after SetSourceRoot: %v", err)
	}
	if len(tree.Files) != 4 {
		t.Errorf("tree pack after SetSourceRoot = %d files; want all 4 with no rule in force", len(tree.Files))
	}
}
