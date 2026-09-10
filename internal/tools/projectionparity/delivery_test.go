// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package projectionparity_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/tools/projectionparity"
)

// The fixtures below are written into a temporary tree by the same helper the
// projection cases use, for the reason recorded there: a fixture tree this read
// can analyse needs a module file at its root, and a committed one would be
// picked up by the module censuses that walk the repository.
//
// The vocabulary the fixtures declare is deliberately NOT the shipped tree's
// spelling. The read derives the delivery type and its mapping function from
// their declaration rather than from a name written into it, and a fixture that
// reused the real names could not tell a derivation from a hardcoded string.

// deliveryVocabulary is the fixture's delivery package: a type, its two values
// and the function mapping a caller's yes-or-no onto it.
const deliveryVocabulary = `package authored

type Mode int

const (
	ModeWrite Mode = iota
	ModeReport
)

func ModeFor(report bool) Mode {
	if report {
		return ModeReport
	}
	return ModeWrite
}
`

// deliveryCore is the fixture capability: one request struct carrying the
// delivery, and the entry point taking it.
const deliveryCore = `package core

import "planted/internal/authored"

type Options struct {
	File     string
	Delivery authored.Mode
}

func Apply(o Options) error { return nil }
`

// analyzeDelivery runs the delivery read over a fixture tree.
func analyzeDelivery(t *testing.T, files map[string]string) projectionparity.DeliveryResult {
	t.Helper()

	result, err := projectionparity.AnalyzeDelivery(
		write(t, files), "planted", "internal/", "internal/authored", surfaces())
	if err != nil {
		t.Fatalf("run the delivery read: %v", err)
	}
	return result
}

// refuseDelivery runs the delivery read over a fixture tree and returns the
// refusal it must produce.
func refuseDelivery(t *testing.T, deliveryDir string, files map[string]string) string {
	t.Helper()

	result, err := projectionparity.AnalyzeDelivery(
		write(t, files), "planted", "internal/", deliveryDir, surfaces())
	if err == nil {
		t.Fatalf("the read returned no error over a corpus it cannot draw a verdict from; it found %d "+
			"capability request(s) and %d composition(s), and a clean result over either emptiness certifies nothing",
			len(result.Capabilities), len(result.Offers))
	}
	return err.Error()
}

// deliveryFindings are a result's findings as "kind Capability on Surface"
// strings, sorted, which is what a case asserts against.
func deliveryFindings(result projectionparity.DeliveryResult) []string {
	out := make([]string, 0, len(result.Findings))
	for _, f := range result.Findings {
		out = append(out, string(f.Kind)+" "+f.Capability+"."+f.Selector+" on "+f.Surface)
	}
	slices.Sort(out)
	return out
}

// TestTheDeliveryReadNamesTheSurfaceThatDecidesForTheCaller walks the cells of
// the two conditions this read turns on — whether a surface composes a
// capability's request at all, and whether that composition names the delivery
// selector — rather than their margins.
//
// The interesting cell is a surface that composes the request and omits the
// selector while another surface offers it, because that is the one state the
// membership check cannot see: both surfaces carry the capability, and only one
// lets the caller choose. A surface that composes nothing is the other cell and
// it must stay silent, since whether a surface carries a capability at all is a
// membership question with a different repair.
func TestTheDeliveryReadNamesTheSurfaceThatDecidesForTheCaller(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			name: "one surface offers the choice and the other omits it",
			files: map[string]string{
				"internal/authored/authored.go": deliveryVocabulary,
				"internal/core/core.go":         deliveryCore,
				"internal/one/one.go": `package one

import (
	"planted/internal/authored"
	"planted/internal/core"
)

func Run(report bool) error {
	return core.Apply(core.Options{File: "x", Delivery: authored.ModeFor(report)})
}
`,
				"internal/two/two.go": `package two

import "planted/internal/core"

func Run() error {
	return core.Apply(core.Options{File: "y"})
}
`,
			},
			want: []string{"delivery-not-offered internal/core.Options.Delivery on the second surface"},
		},
		{
			name: "both surfaces offer the choice",
			files: map[string]string{
				"internal/authored/authored.go": deliveryVocabulary,
				"internal/core/core.go":         deliveryCore,
				"internal/one/one.go": `package one

import (
	"planted/internal/authored"
	"planted/internal/core"
)

func Run(report bool) error {
	return core.Apply(core.Options{File: "x", Delivery: authored.ModeFor(report)})
}
`,
				"internal/two/two.go": `package two

import (
	"planted/internal/authored"
	"planted/internal/core"
)

func Run(report bool) error {
	return core.Apply(core.Options{File: "y", Delivery: authored.ModeFor(report)})
}
`,
			},
			want: nil,
		},
		{
			name: "a surface composing the request nowhere is not this read's subject",
			files: map[string]string{
				"internal/authored/authored.go": deliveryVocabulary,
				"internal/core/core.go":         deliveryCore,
				"internal/one/one.go": `package one

import (
	"planted/internal/authored"
	"planted/internal/core"
)

func Run(report bool) error {
	return core.Apply(core.Options{File: "x", Delivery: authored.ModeFor(report)})
}
`,
				"internal/two/two.go": `package two

func Run() error { return nil }
`,
			},
			want: nil,
		},
		{
			// A boolean the capability's own package maps is a selector one step
			// removed, and a read that only looked for the delivery type would
			// report such a capability as carrying no delivery at all.
			name: "a boolean the capability maps itself is a selector too",
			files: map[string]string{
				"internal/authored/authored.go": deliveryVocabulary,
				"internal/core/core.go": `package core

import "planted/internal/authored"

type Options struct {
	File   string
	Report bool
}

func Apply(o Options) error {
	_ = authored.ModeFor(o.Report)
	return nil
}
`,
				"internal/one/one.go": `package one

import "planted/internal/core"

func Run(report bool) error {
	return core.Apply(core.Options{File: "x", Report: report})
}
`,
				"internal/two/two.go": `package two

import "planted/internal/core"

func Run() error {
	return core.Apply(core.Options{File: "y"})
}
`,
			},
			want: []string{"delivery-not-offered internal/core.Options.Report on the second surface"},
		},
		{
			// A request another capability's package composes is a carrier, not a
			// request a caller composes: the delivery travels with a delegation
			// rather than being asked for at that surface.
			name: "a request another capability composes is excluded as a carrier",
			files: map[string]string{
				"internal/authored/authored.go": deliveryVocabulary,
				"internal/core/core.go":         deliveryCore,
				"internal/outer/outer.go": `package outer

import (
	"planted/internal/authored"
	"planted/internal/core"
)

type Options struct {
	Delivery authored.Mode
}

func Apply(o Options) error {
	return core.Apply(core.Options{File: "delegated", Delivery: o.Delivery})
}
`,
				"internal/one/one.go": `package one

import (
	"planted/internal/authored"
	"planted/internal/outer"
)

func Run(report bool) error {
	return outer.Apply(outer.Options{Delivery: authored.ModeFor(report)})
}
`,
				"internal/two/two.go": `package two

import "planted/internal/core"

func Run() error {
	return core.Apply(core.Options{File: "y"})
}
`,
			},
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := analyzeDelivery(t, tc.files)
			got := deliveryFindings(result)
			if len(got) != len(tc.want) || (len(got) > 0 && strings.Join(got, "\n") != strings.Join(tc.want, "\n")) {
				t.Errorf("findings:\n got %v\nwant %v\nreport:\n%s",
					got, tc.want, projectionparity.FormatDeliveryFindings(result.Findings))
			}
		})
	}
}

// TestTheDeliveryReadExcludesACarrierLoudly pins that an excluded carrier is
// named rather than dropped in silence, and that the capability it carries for
// is still under check one hop out.
//
// The exclusion is a decision about what this read certifies, so a reader has to
// be able to see its size from the same run that reports the verdict — and the
// exclusion must not be readable as an escape: wrapping a capability in a second
// core moves the checked request outward, it does not remove it.
func TestTheDeliveryReadExcludesACarrierLoudly(t *testing.T) {
	t.Parallel()

	result := analyzeDelivery(t, map[string]string{
		"internal/authored/authored.go": deliveryVocabulary,
		"internal/core/core.go":         deliveryCore,
		"internal/outer/outer.go": `package outer

import (
	"planted/internal/authored"
	"planted/internal/core"
)

type Options struct {
	Delivery authored.Mode
}

func Apply(o Options) error {
	return core.Apply(core.Options{File: "delegated", Delivery: o.Delivery})
}
`,
		"internal/one/one.go": `package one

import (
	"planted/internal/authored"
	"planted/internal/outer"
)

func Run(report bool) error {
	return outer.Apply(outer.Options{Delivery: authored.ModeFor(report)})
}
`,
		"internal/two/two.go": `package two

import "planted/internal/outer"

func Run() error {
	return outer.Apply(outer.Options{})
}
`,
	})

	var named bool
	for _, entry := range result.Delegated {
		if strings.HasPrefix(entry, "internal/core.Options ") {
			named = true
		}
	}
	if !named {
		t.Errorf("the carrier is not named among the %d excluded request(s): %v; an exclusion nothing "+
			"reports is a loss of coverage that arrives as a green", len(result.Delegated), result.Delegated)
	}

	// The outer request is the one a caller composes, so it stays under check —
	// and the surface omitting it is still named.
	want := []string{"delivery-not-offered internal/outer.Options.Delivery on the second surface"}
	if got := deliveryFindings(result); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the delegating capability is not checked at the request a caller composes:\n got %v\nwant %v",
			got, want)
	}
}

// TestTheDeliveryReadRefusesEveryEmptinessACleanVerdictWouldHide drives each
// state in which this read would return no finding and no error while having
// established nothing.
//
// Each is refused rather than reported, and they are separate cases because they
// have separate repairs: an empty capability census means nothing under the scope
// carries a delivery, an empty offered set means the corpus reached no surface
// composition, and a stale vocabulary directory means the read never found the
// vocabulary it derives the census from. A verdict drawn over any of the three
// would certify a tree it never opened.
func TestTheDeliveryReadRefusesEveryEmptinessACleanVerdictWouldHide(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		deliveryDir string
		files       map[string]string
		want        string
	}{
		{
			// The capability census is empty: the vocabulary is declared and no
			// request under the scope carries it. Every surface would pass for
			// composing none of an empty set.
			name:        "no request carries a delivery",
			deliveryDir: "internal/authored",
			files: map[string]string{
				"internal/authored/authored.go": deliveryVocabulary,
				"internal/core/core.go": `package core

type Options struct {
	File string
}

func Apply(o Options) error { return nil }
`,
				"internal/one/one.go": `package one

import "planted/internal/core"

func Run() error { return core.Apply(core.Options{File: "x"}) }
`,
				"internal/two/two.go": `package two

import "planted/internal/core"

func Run() error { return core.Apply(core.Options{File: "y"}) }
`,
			},
			want: "no request type carrying a delivery selector",
		},
		{
			// The offered set is empty: the census is non-empty and no surface
			// composes any of it, so no surface's offered set was read at all.
			// This is not the case above inverted — the capability set can be
			// full while the corpus reaches nothing.
			name:        "no surface composes any capability request",
			deliveryDir: "internal/authored",
			files: map[string]string{
				"internal/authored/authored.go": deliveryVocabulary,
				"internal/core/core.go":         deliveryCore,
				"internal/one/one.go": `package one

func Run() error { return nil }
`,
				"internal/two/two.go": `package two

func Run() error { return nil }
`,
			},
			want: "no surface composing any of the",
		},
		{
			// The spelled vocabulary directory is stale. It has to fail naming its
			// own cause: left to derive, it would find no capability and the
			// refusal above would blame the tree for the read's own wrong input.
			name:        "the vocabulary directory declares no mapping",
			deliveryDir: "internal/core",
			files: map[string]string{
				"internal/authored/authored.go": deliveryVocabulary,
				"internal/core/core.go":         deliveryCore,
				"internal/one/one.go": `package one

import (
	"planted/internal/authored"
	"planted/internal/core"
)

func Run(report bool) error {
	return core.Apply(core.Options{File: "x", Delivery: authored.ModeFor(report)})
}
`,
				"internal/two/two.go": `package two

import "planted/internal/core"

func Run() error { return core.Apply(core.Options{File: "y"}) }
`,
			},
			want: "declares no function taking one boolean",
		},
		{
			name:        "the vocabulary directory does not exist",
			deliveryDir: "internal/gone",
			files: map[string]string{
				"internal/authored/authored.go": deliveryVocabulary,
				"internal/core/core.go":         deliveryCore,
				"internal/one/one.go": `package one

func Run() error { return nil }
`,
				"internal/two/two.go": `package two

func Run() error { return nil }
`,
			},
			want: "read the delivery vocabulary",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := refuseDelivery(t, tc.deliveryDir, tc.files)
			if !strings.Contains(got, tc.want) {
				t.Errorf("the refusal does not say what it refused:\n got %q\nwant it to contain %q", got, tc.want)
			}
		})
	}
}

// TestTheDeliveryReadRefusesACorpusThatNamesNoSurface pins the two corpus
// defects that would have the read draw a verdict over surfaces it was never
// given.
func TestTheDeliveryReadRefusesACorpusThatNamesNoSurface(t *testing.T) {
	t.Parallel()

	root := write(t, map[string]string{
		"internal/authored/authored.go": deliveryVocabulary,
		"internal/core/core.go":         deliveryCore,
	})

	for _, tc := range []struct {
		name     string
		surfaces []projectionparity.Surface
		want     string
	}{
		{
			name:     "no surface at all",
			surfaces: nil,
			want:     "was given no surface",
		},
		{
			name:     "a surface with no name",
			surfaces: []projectionparity.Surface{{Dirs: []string{"internal/one"}}},
			want:     "no finding could say which surface it is about",
		},
		{
			name:     "a surface with no directory",
			surfaces: []projectionparity.Surface{{Name: "the first surface"}},
			want:     "was given no directory",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := projectionparity.AnalyzeDelivery(root, "planted", "internal/", "internal/authored", tc.surfaces)
			if err == nil {
				t.Fatal("the read accepted a corpus that names no surface it could report about")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say what it refused:\n got %q\nwant it to contain %q",
					err.Error(), tc.want)
			}
		})
	}
}
