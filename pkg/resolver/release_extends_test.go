// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// The tests in this file hold the properties a Release extends chain is walked
// under. Each asserts what the diagnostic SAYS rather than that some error
// matched, because the three conditions a chain can report are told apart only
// by their content: a test satisfied by any one of them keeps passing while a
// chain reports the wrong one, and an author reading the wrong one is told to
// shorten a chain whose length was never the problem.
//
//   - a chain that closes on itself reports the cycle condition at every length,
//     and the diagnostic names the EDGE that closes the loop — the Release
//     carrying the offending extends and the reference it declares — rather than
//     the Release the resolution started from.
//   - a chain that ends cleanly and runs past the bound reports the over-deep
//     condition, carrying both the bound and the length reached.
//   - a reference that resolves to nothing keeps the advice a Release author can
//     act on: the relative-path form, resolved from the directory holding the
//     Release, and never the catalog. It carries the failure that looking there
//     produced.
//   - chain membership is keyed on each link's resolved file path, so two Release
//     files declaring one metadata.name are two links and a chain passing through
//     both has not met itself.
//
// Each test names again, at its own site, which property it holds and what
// breaking it would look like.

// writeReleaseFile writes a Release YAML fixture into dir.
func writeReleaseFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// releaseDoc renders one Release: its own metadata.name and the extends
// reference it declares, omitted when empty.
//
// A Release that declares extends names no Aggregate, and one that declares no
// extends names both an Aggregate and a Manifest. That split is the schema's
// rather than a habit of these fixtures — an inheriting Release is forbidden an
// Aggregate of its own, and a Release inheriting nothing is required to name
// both siblings — so a terminal link written any other way is refused by the
// envelope before the chain reaches it, and the walk then reports an
// unresolvable parent for a file that is sitting right there. Every test below
// that expects a condition other than that one depends on this.
func releaseDoc(name, extends string) string {
	var b strings.Builder
	b.WriteString("apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: ")
	b.WriteString(name)
	b.WriteString("\n  environment: dev\n  version: \"1.0.0\"\nspec:\n")
	if extends != "" {
		b.WriteString("  extends: " + extends + "\n")
		return b.String()
	}
	b.WriteString("  aggregate: chain-app\n  manifest: chain-manifest\n")
	return b.String()
}

// releaseChainName names the i-th Release of a generated chain. The width is
// fixed so the names sort in chain order wherever a failure prints them.
func releaseChainName(i int) string { return fmt.Sprintf("r%02d", i) }

// loadReleaseLink loads one fixture the way a caller holding a Release path
// holds it: the parsed document together with the file it was read from, which
// is the link's identity along the chain.
func loadReleaseLink(t *testing.T, dir, name string) ResolvedRelease {
	t.Helper()
	path := filepath.Join(dir, name)
	rel, err := LoadRelease(path)
	if err != nil {
		t.Fatalf("test setup: load %s: %v", name, err)
	}
	return ResolvedRelease{Release: rel, Path: path}
}

// writeReleaseChain writes n Releases into dir, each extending the next. When
// cyclic the last extends the first; otherwise the last ends the chain. It
// returns the head, loaded.
func writeReleaseChain(t *testing.T, dir string, n int, cyclic bool) ResolvedRelease {
	t.Helper()
	for i := range n {
		extends := ""
		switch {
		case i < n-1:
			extends = "./" + releaseChainName(i+1) + ".yaml"
		case cyclic:
			extends = "./" + releaseChainName(0) + ".yaml"
		}
		writeReleaseFile(t, dir, releaseChainName(i)+".yaml", releaseDoc(releaseChainName(i), extends))
	}
	return loadReleaseLink(t, dir, releaseChainName(0)+".yaml")
}

// TestReleaseExtendsChain_NoExtends_ChainOfOne — a Release declaring no extends
// reference yields a chain of one: itself, as loaded.
//
// It is the control every refusal below needs. Without it each of them could be
// satisfied by a walk that refused every chain it was handed.
func TestReleaseExtendsChain_NoExtends_ChainOfOne(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeReleaseFile(t, dir, "solo.yaml", releaseDoc("solo-release", ""))
	head := loadReleaseLink(t, dir, "solo.yaml")

	chain, err := ReleaseExtendsChain("", dir, head)
	if err != nil {
		t.Fatalf("a Release with no extends must resolve; got: %v", err)
	}
	if len(chain) != 1 {
		t.Fatalf("chain length = %d; want 1", len(chain))
	}
	if chain[0].Release != head.Release {
		t.Errorf("the single link must be the Release as loaded (same pointer)")
	}
	if chain[0].Path != head.Path {
		t.Errorf("link path = %q; want the head's own %q", chain[0].Path, head.Path)
	}
}

// TestReleaseExtendsChain_DepthBoundAdmitsTenAndRefusesEleven pins the depth
// bound a Release extends chain is held to.
//
// The accepted row is the control: without it the refusal could be satisfied by
// a walk that refused everything. The bound is spelled out here rather than read
// back from the walk, deliberately — the number is part of what an author is
// told, so moving it must surface as a failing expectation rather than as an
// expression that quietly follows it.
//
// 🔴 Both halves of the refusal are asserted: the over-deep condition IS
// reported and the absent-parent condition is NOT. A terminal link the envelope
// refuses loads as an unresolvable parent, so a chain whose last Release is not
// schema-valid halts under the wrong condition — which a test asserting only
// that something failed would accept, and which is exactly the shape a reader
// building this fixture by hand produces first.
func TestReleaseExtendsChain_DepthBoundAdmitsTenAndRefusesEleven(t *testing.T) {
	t.Parallel()

	t.Run("accepted at ten", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		head := writeReleaseChain(t, dir, 10, false)

		chain, err := ReleaseExtendsChain("", dir, head)
		if err != nil {
			t.Fatalf("a chain of ten Releases must be accepted; got: %v", err)
		}
		if len(chain) != 10 {
			t.Fatalf("chain length = %d; want 10", len(chain))
		}
		// The root's own file and name prove the whole chain was followed, rather
		// than the walk declining to fail partway along it.
		wantRoot := filepath.Join(dir, releaseChainName(9)+".yaml")
		if chain[9].Path != wantRoot {
			t.Errorf("last link = %q; want the root %q", chain[9].Path, wantRoot)
		}
		if got := chain[9].Release.Metadata.Name; got != releaseChainName(9) {
			t.Errorf("last link names %q; want %q", got, releaseChainName(9))
		}
	})

	t.Run("refused at eleven", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		head := writeReleaseChain(t, dir, 11, false)

		_, err := ReleaseExtendsChain("", dir, head)
		if err == nil {
			t.Fatal("a chain of eleven Releases must halt")
		}
		if !errors.Is(err, validator.ErrExtendsChainTooDeep()) {
			t.Errorf("depth refusal = %v; want the over-deep-chain condition", err)
		}
		if errors.Is(err, validator.ErrInheritanceMerge()) {
			t.Errorf("depth refusal = %v; it must not also report the condition an absent parent reports", err)
		}
		if errors.Is(err, validator.ErrCyclicExtension()) {
			t.Errorf("depth refusal = %v; a chain that ends cleanly is not a loop", err)
		}
		want := fmt.Sprintf(
			"%s (Release %s, rule extends). "+
				"the bound is 10 and this chain reaches 11; shorten the chain, or extend a "+
				"base that sits closer to its root",
			validator.ErrExtendsChainTooDeep().Error(), releaseChainName(0))
		if err.Error() != want {
			t.Errorf("depth refusal message =\n\t%q\nwant\n\t%q", err.Error(), want)
		}
	})
}

// TestReleaseExtendsChain_ALoopIsReportedAsALoopAtEveryLength pins that a cyclic
// Release chain reports the cycle condition whatever its length.
//
// The lengths cover both sides of the depth bound. A loop that closes before the
// bound is reached is the easy case; one longer than the bound is the case a
// bound tested inside the walk would claim first, handing the author advice —
// shorten the chain — that cannot remove a loop. The bound is judged once the
// walk has ended, so a chain that closes on itself is a loop at any length.
func TestReleaseExtendsChain_ALoopIsReportedAsALoopAtEveryLength(t *testing.T) {
	t.Parallel()
	for _, length := range []int{2, 3, 9, 10, 12} {
		t.Run(fmt.Sprintf("loop of %d", length), func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			head := writeReleaseChain(t, dir, length, true)

			_, err := ReleaseExtendsChain("", dir, head)
			if err == nil {
				t.Fatalf("a loop of %d must halt", length)
			}
			if !errors.Is(err, validator.ErrCyclicExtension()) {
				t.Errorf("a loop of %d must report the cycle condition; got: %v", length, err)
			}
			if errors.Is(err, validator.ErrExtendsChainTooDeep()) {
				t.Errorf("a loop of %d reports the over-deep-chain condition; shortening a loop does not remove it: %v", length, err)
			}
			// Matched without regard to case, so the assertion keeps biting if the
			// advice is ever reworded to open a sentence differently: a phrase this
			// looks for in one casing alone would pass trivially the moment that
			// casing moved, and nothing would say so.
			if strings.Contains(strings.ToLower(err.Error()), "shorten the chain") {
				t.Errorf("a loop of %d is answered with advice to shorten it: %v", length, err)
			}
		})
	}
}

// TestReleaseExtendsChain_CycleNamesTheEdgeThatClosesIt pins which edge a cycle
// diagnostic sends the author to.
//
// A two-link loop cannot tell the edge that closes the loop from the one the
// walk started on, because they are the same edge; three links is the shortest
// loop that can, which is why the three-link row is the discriminating one. The
// two-link row sits beside it as the degenerate case, so a failure separates
// "names the wrong edge" from "names no edge at all".
//
// Naming the edge rather than one endpoint is the whole of what makes the remedy
// actionable: the Release a resolution was requested for is rarely the one
// carrying the link that closes the loop, and an author handed only that one
// opens a file whose extends declaration is legal.
func TestReleaseExtendsChain_CycleNamesTheEdgeThatClosesIt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		// links is how many Releases the loop runs through; from indexes the one
		// whose extends declaration closes it.
		links, from int
	}{
		{"three links name the third, not the head", 3, 2},
		{"two links name the second", 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			head := writeReleaseChain(t, dir, tc.links, true)

			_, err := ReleaseExtendsChain("", dir, head)
			if err == nil {
				t.Fatalf("a loop of %d must halt", tc.links)
			}
			if !errors.Is(err, validator.ErrCyclicExtension()) {
				t.Fatalf("a loop of %d must report the cycle condition; got: %v", tc.links, err)
			}
			from := releaseChainName(tc.from)
			want := fmt.Sprintf(
				"%s (Release %s, rule extends). "+
					"%s extends ./%s.yaml, which is already in the chain; remove the extends on %s, "+
					"or point it at an entity outside the chain",
				validator.ErrCyclicExtension().Error(), releaseChainName(0),
				from, releaseChainName(0), from)
			if err.Error() != want {
				t.Errorf("cycle diagnostic =\n\t%q\nwant\n\t%q", err.Error(), want)
			}
		})
	}
}

// TestReleaseExtendsChain_MissingParentCarriesDirectoryAdviceAndResolveError
// pins the whole diagnostic a Release whose parent cannot be resolved receives,
// for both forms an author reaches for.
//
// Three properties are load-bearing here and none is reachable from the
// condition's identity alone, which is why the assertion is on the rendered
// message.
//
// The advice names where a Release parent actually resolves — a relative path,
// read from the directory holding the Release — and never the catalog. A Release
// is operator configuration rather than catalog content, so the wording a kind
// whose extends addresses a catalog entity receives would send this author
// somewhere their reference does not name. The catalog's absence is asserted as
// the bare word, which is available here precisely because a Release parent is a
// file: the failure underneath names a file it could not open and never a place
// it looked the reference up.
//
// The underlying resolve failure travels in the text, so the author reads which
// file could not be opened, or which reference form was refused, rather than
// only that something did not resolve. It travels as text and not as a wrapped
// chain, so it is unreachable by errors.Is and the rendered message is the only
// thing that can hold it.
//
// The registry-style row is the other form an author reaches for. It is refused
// rather than searched for, because the same-directory scan that lets an
// Aggregate or Manifest reference name an entity by identity does not index
// Releases; what the row pins is that the refusal says which form does resolve.
func TestReleaseExtendsChain_MissingParentCarriesDirectoryAdviceAndResolveError(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		ref  string
		// wantUnderlying is a fragment of the resolve failure that must survive
		// into the composed message. The relative-path row's full text names a
		// temporary directory and the operating system's own wording, so only the
		// part this repository controls is spelled out.
		wantUnderlying string
	}{
		{
			name:           "a relative path naming no file",
			ref:            "./does-not-exist.yaml",
			wantUnderlying: "failed to read file: open ",
		},
		{
			name: "a registry-style token, which addresses no file beside the Release",
			ref:  "hub.aggrega.ai/release/prod-release:1.0.0",
			wantUnderlying: "a Release extends reference addresses a file beside the Release, " +
				"so it is written as a relative path beginning ./ or ../",
		},
		// A Release extends names a file, so a colon in it is not a version pin and a
		// malformed-looking one is not judged as one: the refusal is the file-path
		// refusal, never the malformed-pin condition a catalog reference reports.
		{
			name: "a token whose text after the colon would be a malformed pin",
			ref:  "prod-release:^garbage",
			wantUnderlying: "a Release extends reference addresses a file beside the Release, " +
				"so it is written as a relative path beginning ./ or ../",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeReleaseFile(t, dir, "child.yaml", releaseDoc("child-release", tc.ref))
			child := loadReleaseLink(t, dir, "child.yaml")
			// The same resolution the walk performs, taken independently, so the
			// expectation below holds the walk carrying that failure through rather
			// than restating the walk's own output. The literal fragment asserted
			// afterwards is what keeps this from being circular.
			_, resolveErr := resolveReleaseExtendsRef("", dir, child, tc.ref)
			if resolveErr == nil {
				t.Fatalf("test setup: %q must not resolve", tc.ref)
			}

			_, err := ReleaseExtendsChain("", dir, child)
			if err == nil {
				t.Fatal("a parent that cannot be resolved must halt")
			}
			if !errors.Is(err, validator.ErrInheritanceMerge()) {
				t.Errorf("missing-parent refusal = %v; want the inheritance-merge condition", err)
			}
			if errors.Is(err, validator.ErrCyclicExtension()) || errors.Is(err, validator.ErrExtendsChainTooDeep()) {
				t.Errorf("missing-parent refusal = %v; a parent that is not there is neither a loop nor an over-deep chain", err)
			}
			if errors.Is(err, types.ErrMalformedVersionPin()) {
				t.Errorf("missing-parent refusal = %v; a Release extends names a file and carries no version pin", err)
			}
			want := fmt.Sprintf(
				"%s (Release child-release, at .spec.extends, child-release extends %s, "+
					"which could not be resolved: %v). "+
					"Verify the extends reference is a relative path to a Release file, such as "+
					"./base-release.yaml, resolved from the directory holding this Release",
				validator.ErrInheritanceMerge().Error(), tc.ref, resolveErr)
			if err.Error() != want {
				t.Errorf("missing-parent message =\n\t%q\nwant\n\t%q", err.Error(), want)
			}
			if !strings.Contains(err.Error(), tc.wantUnderlying) {
				t.Errorf("missing-parent message = %q; want it to carry the resolve failure %q", err.Error(), tc.wantUnderlying)
			}
			// The catalog is looked for in what the WALK supplies, with the resolve
			// failure it carries taken back out first. The failure's own text names
			// the file it could not open, and that path is a temporary directory
			// built from this test's name — so searching the composed message whole
			// would make the assertion turn on what the test is called.
			walkWording := strings.ReplaceAll(err.Error(), resolveErr.Error(), "")
			if strings.Contains(strings.ToLower(walkWording), "catalog") {
				t.Errorf("missing-parent message = %q; a Release parent does not resolve from the catalog, so the advice must not name it", err.Error())
			}
			// The shared wording a kind whose extends addresses a catalog entity
			// receives, asserted absent by its own sentences as well: it is what
			// this diagnostic renders the moment the Release stops supplying its
			// own, and naming it here is what makes such a failure readable.
			if strings.Contains(err.Error(), "Verify the extends reference names an entity present in the catalog") {
				t.Errorf("missing-parent message = %q; it renders the wording for a kind whose extends names a catalog entity", err.Error())
			}
		})
	}
}

// TestReleaseExtendsChain_IdentityIsTheResolvedPathNotTheName pins what makes
// two links the same link.
//
// Two Release files declaring one metadata.name are legal input — a pair naming
// the deployed system rather than the environment is the ordinary shape — so a
// walk folding them into one link would report a loop the author never wrote and
// refuse files that resolve today.
//
// 🔴 The three rows are one A/B and no row is worth anything alone. The first
// says a name collision is not a loop. The second says the walk does find a loop
// when there is one, so the first cannot be satisfied by a walk that never
// reports a cycle at all. The third closes the pair from the other side: a real
// loop whose two files ALSO share a name is still reported, so the first cannot
// be satisfied by a walk that stops looking the moment names collide. A walk
// keyed on metadata.name passes the second row and fails the first and the
// third.
func TestReleaseExtendsChain_IdentityIsTheResolvedPathNotTheName(t *testing.T) {
	t.Parallel()

	t.Run("two files sharing one name are two links", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeReleaseFile(t, dir, "a.yaml", releaseDoc("shared-release", "./b.yaml"))
		writeReleaseFile(t, dir, "b.yaml", releaseDoc("shared-release", ""))
		head := loadReleaseLink(t, dir, "a.yaml")

		chain, err := ReleaseExtendsChain("", dir, head)
		if err != nil {
			t.Fatalf("two Release files sharing one metadata.name must resolve; got: %v", err)
		}
		if len(chain) != 2 {
			t.Fatalf("chain length = %d; want 2 — both files are links", len(chain))
		}
		// A slice rather than a map, so a failure reports the links in chain order
		// rather than in whatever order a map walk produced.
		for i, want := range []string{filepath.Join(dir, "a.yaml"), filepath.Join(dir, "b.yaml")} {
			if chain[i].Path != want {
				t.Errorf("link %d = %q; want %q", i, chain[i].Path, want)
			}
		}
		// Both links carrying the one name is the input this row exists for: a
		// failure here says the fixture stopped being the case under test.
		for i, link := range chain {
			if link.Release.Metadata.Name != "shared-release" {
				t.Fatalf("test setup: link %d names %q; both files must declare one name", i, link.Release.Metadata.Name)
			}
		}
	})

	t.Run("a real loop between distinctly named files is reported", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		head := writeReleaseChain(t, dir, 2, true)

		_, err := ReleaseExtendsChain("", dir, head)
		if err == nil {
			t.Fatal("a loop between two distinctly named Releases must halt")
		}
		if !errors.Is(err, validator.ErrCyclicExtension()) {
			t.Errorf("loop refusal = %v; want the cycle condition", err)
		}
	})

	t.Run("a real loop between files sharing one name is still reported", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeReleaseFile(t, dir, "a.yaml", releaseDoc("shared-release", "./b.yaml"))
		writeReleaseFile(t, dir, "b.yaml", releaseDoc("shared-release", "./a.yaml"))
		head := loadReleaseLink(t, dir, "a.yaml")

		_, err := ReleaseExtendsChain("", dir, head)
		if err == nil {
			t.Fatal("a loop between two Releases sharing one metadata.name must halt")
		}
		if !errors.Is(err, validator.ErrCyclicExtension()) {
			t.Errorf("loop refusal = %v; want the cycle condition", err)
		}
		want := fmt.Sprintf(
			"%s (Release shared-release, rule extends). "+
				"shared-release extends ./a.yaml, which is already in the chain; remove the extends "+
				"on shared-release, or point it at an entity outside the chain",
			validator.ErrCyclicExtension().Error())
		if err.Error() != want {
			t.Errorf("cycle diagnostic =\n\t%q\nwant\n\t%q", err.Error(), want)
		}
	})
}
