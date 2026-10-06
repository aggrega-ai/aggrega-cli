// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pathscope_test

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
)

// backslashModelRoot is the root the modelled Windows join is taken against. Its
// last part spells letters the generated names below never use, so a name that
// climbs out of it cannot land back inside it by naming it.
const backslashModelRoot = "/srv/dest"

// windowsJoinStaysUnder reports whether joining name onto root the way a Windows
// join does stays at or under root. It models that join on every platform, so
// the property it checks is checked on Linux too: a backslash in name is a
// separator, a separator leading name does not start it again from the top, and
// ".." removes the part before it — which is path.Clean over root, a slash, and
// name with its backslashes turned into slashes. root is slash-separated,
// absolute and clean; name carries no drive or volume.
func windowsJoinStaysUnder(root, name string) bool {
	joined := path.Clean(root + "/" + strings.ReplaceAll(name, `\`, "/"))
	return joined == root || strings.HasPrefix(joined, root+"/")
}

// hostJoinStaysUnder reports whether joining name onto root with this host's own
// join stays at or under root. On Windows it is the join the model above stands
// for; on Linux it reads a backslash as an ordinary character.
func hostJoinStaysUnder(root, name string) bool {
	joined := filepath.Join(root, filepath.FromSlash(name))
	return joined == root || strings.HasPrefix(joined, root+string(filepath.Separator))
}

// slashOnlyVerdict is the containment verdict over slash arithmetic alone, with a
// backslash read as an ordinary character. It is written out here rather than
// asked of the package, because it is the reference the package's answer is
// compared against: a name holding no backslash must get exactly this verdict,
// and every name this accepts and the package refuses must be a climb.
func slashOnlyVerdict(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") {
		return false
	}
	c := path.Clean(name)
	return c != ".." && !strings.HasPrefix(c, "../")
}

// TestIsLocalSlashReadsABackslashAsASeparatorForTheClimb pins the backslash half
// of the containment verdict, row by row.
//
// A logical name is joined onto a root in the spelling of the machine that
// writes it, and a Windows join reads a backslash as a separator, so a name that
// climbs out through backslashes would land outside the root there. Each refused
// row is such a climb, and the row asserts it: the modelled Windows join of the
// name as written, or of the name once cleaned, leaves the root. The refusal is
// the same on every platform, which is what makes this table run identically on
// Linux and on Windows.
//
// The accepted rows hold a backslash that climbs nowhere, and each is asserted to
// join beneath the root both through the modelled Windows join and through this
// host's own join, as written and cleaned. The slash-only rows are the controls:
// they carry the verdicts the slash-only reading has always given.
func TestIsLocalSlashReadsABackslashAsASeparatorForTheClimb(t *testing.T) {
	t.Parallel()
	hostRoot := filepath.FromSlash(backslashModelRoot)
	tests := []struct {
		name string
		in   string
		want bool
		// climbsThroughBackslash marks a row refused because a Windows join of it
		// leaves the root, which the row then asserts of the model.
		climbsThroughBackslash bool
	}{
		// Refused: each climbs out once a backslash is read as a separator.
		{name: "climbs one level", in: `..\x`, climbsThroughBackslash: true},
		{name: "climbs two levels", in: `..\..\x`, climbsThroughBackslash: true},
		{name: "climbs after descending", in: `a\..\..\x`, climbsThroughBackslash: true},
		{name: "climbs further after descending two", in: `a\b\..\..\..\x`, climbsThroughBackslash: true},
		{name: "a bare climb ending in a backslash", in: `..\`, climbsThroughBackslash: true},
		{name: "a climb behind a leading backslash", in: `\..`, climbsThroughBackslash: true},
		{name: "a climb and a name behind a leading backslash", in: `\..\x`, climbsThroughBackslash: true},
		{name: "slash then backslashes", in: `a/..\..\x`, climbsThroughBackslash: true},
		{name: "climbs as written though cleaning resolves it", in: `x\../..`, climbsThroughBackslash: true},
		{name: "climbs only once cleaned", in: `a\b/../..\x`, climbsThroughBackslash: true},

		// Accepted: a backslash that climbs nowhere.
		{name: "a backslash between ordinary parts", in: `a\b`, want: true},
		{name: "two backslashes between ordinary parts", in: `a\b\c.yaml`, want: true},
		{name: "an interior climb resolving inside", in: `a\..\b`, want: true},
		{name: "a climb back to the directory itself", in: `x\..`, want: true},
		{name: "a leading backslash is not a fresh start", in: `\x`, want: true},
		{name: "a lone backslash names the directory", in: `\`, want: true},
		{name: "a part beginning with two dots", in: `..config\x`, want: true},
		{name: "two dots inside a part", in: `a\..b`, want: true},
		{name: "four dots", in: `....\x`, want: true},
		{name: "a leading dot part", in: `.\x`, want: true},
		{name: "a slash climb over a backslash part, back to the directory", in: `a\b/..`, want: true},

		// Controls: no backslash, so the slash-only verdict stands.
		{name: "plain name", in: "config.yaml", want: true},
		{name: "nested", in: "a/b/c.yaml", want: true},
		{name: "dot", in: ".", want: true},
		{name: "interior climb", in: "a/../b", want: true},
		{name: "trailing climb to the root", in: "a/..", want: true},
		{name: "leading two dots then a letter", in: "..config", want: true},
		{name: "empty", in: ""},
		{name: "bare climb", in: ".."},
		{name: "climb out", in: "../x"},
		{name: "climb out after descending", in: "a/../../x"},
		{name: "absolute", in: "/etc/passwd"},
		{name: "absolute after cleaning", in: "//etc"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := pathscope.IsLocalSlash(tc.in); got != tc.want {
				t.Fatalf("IsLocalSlash(%q) = %v; want %v", tc.in, got, tc.want)
			}
			cleaned := path.Clean(tc.in)
			if tc.climbsThroughBackslash {
				if windowsJoinStaysUnder(backslashModelRoot, tc.in) && windowsJoinStaysUnder(backslashModelRoot, cleaned) {
					t.Fatalf("row %q is marked as climbing through a backslash, but a Windows join of it stays under %s both as written and cleaned (%q); the refusal it asserts would be refusing a name that does not climb",
						tc.in, backslashModelRoot, cleaned)
				}
				if !slashOnlyVerdict(tc.in) {
					t.Fatalf("row %q is refused by the slash-only reading already, so it does not exercise the backslash reading", tc.in)
				}
			}
			if !tc.want {
				return
			}
			for _, joined := range []string{tc.in, cleaned} {
				if !windowsJoinStaysUnder(backslashModelRoot, joined) {
					t.Errorf("IsLocalSlash(%q) accepted, but a Windows join of %q leaves %s", tc.in, joined, backslashModelRoot)
				}
				if !hostJoinStaysUnder(hostRoot, joined) {
					t.Errorf("IsLocalSlash(%q) accepted, but this host's join of %q leaves %s", tc.in, joined, hostRoot)
				}
			}
		})
	}
}

// TestIsLocalSlashChangesNothingButTheBackslashClimb walks every name of up to
// eight characters over a slash, a backslash, a dot and a letter, and holds the
// verdict to three properties on each:
//
//   - A name holding no backslash gets exactly the slash-only verdict.
//   - An accepted name joins beneath the root through the modelled Windows join
//     and through this host's own join, both as written and once cleaned — the
//     promise a caller relies on to skip checking the join.
//   - A name the slash-only reading accepts and this verdict refuses is a climb:
//     the modelled Windows join of it, as written or cleaned, leaves the root.
//     So what this verdict refuses beyond the slash-only reading is exactly the
//     names that climb out through a backslash, and nothing else.
//
// Failures are collected and reported together, capped, so one defect reads as a
// list of the names it reaches rather than as tens of thousands of lines.
func TestIsLocalSlashChangesNothingButTheBackslashClimb(t *testing.T) {
	t.Parallel()
	const alphabet = `a./\`
	const maxLen = 8
	const reportCap = 20
	hostRoot := filepath.FromSlash(backslashModelRoot)

	var failures []string
	fail := func(format string, args ...any) {
		if len(failures) < reportCap {
			failures = append(failures, "\t"+fmt.Sprintf(format, args...))
		} else if len(failures) == reportCap {
			failures = append(failures, "\t…")
		}
	}

	visited := 0
	buf := make([]byte, 0, maxLen)
	var walk func()
	walk = func() {
		name := string(buf)
		visited++
		got := pathscope.IsLocalSlash(name)
		reference := slashOnlyVerdict(name)
		cleaned := path.Clean(name)

		if !strings.Contains(name, `\`) && got != reference {
			fail("IsLocalSlash(%q) = %v, but the name holds no backslash and the slash-only verdict is %v", name, got, reference)
		}
		if got && !reference {
			fail("IsLocalSlash(%q) accepted a name the slash-only reading refuses", name)
		}
		if got {
			for _, joined := range []string{name, cleaned} {
				if !windowsJoinStaysUnder(backslashModelRoot, joined) {
					fail("IsLocalSlash(%q) accepted, but a Windows join of %q leaves %s", name, joined, backslashModelRoot)
				}
				if !hostJoinStaysUnder(hostRoot, joined) {
					fail("IsLocalSlash(%q) accepted, but this host's join of %q leaves %s", name, joined, hostRoot)
				}
			}
		}
		if reference && !got && windowsJoinStaysUnder(backslashModelRoot, name) && windowsJoinStaysUnder(backslashModelRoot, cleaned) {
			fail("IsLocalSlash(%q) refused a name that does not climb: a Windows join of it stays under %s as written and cleaned", name, backslashModelRoot)
		}

		if len(buf) == maxLen {
			return
		}
		for i := 0; i < len(alphabet); i++ {
			buf = append(buf, alphabet[i])
			walk()
			buf = buf[:len(buf)-1]
		}
	}
	walk()

	// The walk's own reach is asserted, so a walk that stopped early cannot pass
	// as a clean one: every length from zero to maxLen over a four-letter alphabet.
	want := 0
	for n, count := 0, 1; n <= maxLen; n, count = n+1, count*len(alphabet) {
		want += count
	}
	if visited != want {
		t.Fatalf("the walk visited %d names; want %d", visited, want)
	}
	if len(failures) > 0 {
		t.Errorf("IsLocalSlash broke a property on these names:\n%s", strings.Join(failures, "\n"))
	}
}
