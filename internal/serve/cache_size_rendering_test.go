// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

// The rendered half of the size round trip, asserted on the wire rather than on
// the struct. Every assertion below marshals a result the way a response is
// marshalled and reads the rendered text back out of the encoded bytes, so a
// field whose tag is wrong fails here rather than passing a struct-level
// comparison and reaching a caller under a name it cannot find.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/cacheops"
)

// sizeRoundTripCase is one byte count and the text the shared formatter writes
// for it.
//
// The expected text is spelled out rather than computed, so a change to the
// formatter's rounding is a failure here instead of a comparison that follows
// the change and asserts nothing.
type sizeRoundTripCase struct {
	name string
	// bytes is the quantity a result carries as a number.
	bytes int64
	// text is what the formatter writes for it.
	text string
}

// exactSizeRoundTrips covers one real value in every unit the formatter can
// emit, plus the two boundaries.
//
// Each value is an exact multiple of its unit — or an exact half-multiple, which
// the one decimal place also represents exactly — because those are the values
// the round trip returns unchanged. That bound is not a shortcoming of these
// cases; it is the contract, and inexactSizeDoesNotReturnItsOwnNumber below
// pins the other side of it.
var exactSizeRoundTrips = []sizeRoundTripCase{
	{name: "zero", bytes: 0, text: "0 B"},
	{name: "bytes below the first unit", bytes: 512, text: "512 B"},
	{name: "one binary kilobyte", bytes: 1 << 10, text: "1.0 KiB"},
	{name: "a half-multiple, which one decimal place holds exactly", bytes: 1536, text: "1.5 KiB"},
	{name: "binary megabytes", bytes: 5 << 20, text: "5.0 MiB"},
	{name: "binary gigabytes", bytes: 3 << 30, text: "3.0 GiB"},
	{name: "binary terabytes", bytes: 2 << 40, text: "2.0 TiB"},
	{name: "binary petabytes", bytes: 7 << 50, text: "7.0 PiB"},
	{name: "binary exabytes, the largest unit the formatter emits", bytes: 1 << 60, text: "1.0 EiB"},
}

// TestARenderedCacheSizeParsesBackToTheNumberItCameFrom is the round trip the
// rendered field exists for: the text leaves the daemon on the wire, goes back
// through the parser that reads a caller-supplied size, and yields the number
// the result carried.
//
// The result is built by cacheRemovals — the projection a removal actually goes
// through — rather than by a literal written here, so a projection that stopped
// setting the rendered field fails this instead of passing a comparison against
// a value the test supplied itself.
func TestARenderedCacheSizeParsesBackToTheNumberItCameFrom(t *testing.T) {
	t.Parallel()
	for _, tc := range exactSizeRoundTrips {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			projected := cacheRemovals([]cacheops.Removal{{
				Class: "workspace external-input store",
				Label: "an-element",
				Size:  tc.bytes,
			}})
			if len(projected) != 1 {
				t.Fatalf("the projection produced %d removals for one input", len(projected))
			}

			encoded, err := json.Marshal(projected[0])
			if err != nil {
				t.Fatalf("marshal the removal: %v", err)
			}
			var onTheWire struct {
				Size     int64  `json:"size"`
				SizeText string `json:"sizeText"`
			}
			if err := json.Unmarshal(encoded, &onTheWire); err != nil {
				t.Fatalf("unmarshal the removal: %v (bytes: %s)", err, encoded)
			}

			if onTheWire.Size != tc.bytes {
				t.Fatalf("the number changed on the wire: got %d, want %d", onTheWire.Size, tc.bytes)
			}
			if onTheWire.SizeText != tc.text {
				t.Fatalf("the rendered size is %q, want %q", onTheWire.SizeText, tc.text)
			}

			parsed, err := cacheops.ParseHumanSize(onTheWire.SizeText)
			if err != nil {
				t.Fatalf("the parser refused the daemon's own rendered size %q: %v", onTheWire.SizeText, err)
			}
			if parsed != tc.bytes {
				t.Fatalf("the round trip lost the quantity: %d rendered as %q parsed back to %d",
					tc.bytes, onTheWire.SizeText, parsed)
			}
		})
	}
}

// TestTheRenderedSizeIsTheSharedFormattersOwnOutput holds the rendered field to
// the formatter rather than to a spelling.
//
// It is the guard against the repair this change exists to prevent: a second
// implementation of the rounding rule, which would agree on every value anyone
// thought to try on the day it was written. A copy that rounds differently on
// one input fails here; a copy that agrees today would not, which is why the
// comparison is against the function and not against a table.
func TestTheRenderedSizeIsTheSharedFormattersOwnOutput(t *testing.T) {
	t.Parallel()

	// Values chosen to straddle the rounding decision rather than to sit on
	// tidy multiples: each one renders to a decimal the formatter had to round.
	straddling := []int64{0, 1, 1023, 1024, 1025, 1500, 1048575, 1048576, 1073741823, 1099511627776 + 1}
	for _, n := range straddling {
		projected := cacheRemovals([]cacheops.Removal{{Label: "an-element", Size: n}})
		if got, want := projected[0].SizeText, cacheops.HumanSize(n); got != want {
			t.Errorf("the daemon rendered %d as %q; the shared formatter writes %q", n, got, want)
		}
	}
}

// TestInexactSizeDoesNotReturnItsOwnNumber records what the round trip does NOT
// guarantee, so nobody later reads the guarantee as wider than it is.
//
// Above a byte the formatter writes one decimal place, so a quantity that is not
// an exact multiple (or half-multiple) of its unit is rendered to the nearest
// tenth and parses back to that tenth rather than to itself. The text is still
// the shared spelling and still parseable — which is what the rendered field
// promises — but it is a display quantity, and a caller needing the exact count
// reads the number beside it. That is why the number is carried and not replaced.
func TestInexactSizeDoesNotReturnItsOwnNumber(t *testing.T) {
	t.Parallel()

	const authored = int64(1500)
	rendered := cacheRemovals([]cacheops.Removal{{Label: "an-element", Size: authored}})[0].SizeText
	if rendered != "1.5 KiB" {
		t.Fatalf("the formatter wrote %q for %d, want %q", rendered, authored, "1.5 KiB")
	}
	parsed, err := cacheops.ParseHumanSize(rendered)
	if err != nil {
		t.Fatalf("the parser refused %q: %v", rendered, err)
	}
	if parsed == authored {
		t.Fatalf("an inexact quantity round-tripped exactly, so this bound no longer holds: "+
			"%d rendered as %q parsed back to %d", authored, rendered, parsed)
	}
	if parsed != 1536 {
		t.Fatalf("the rounded quantity is %d, want 1536 — the tenth %q names", parsed, rendered)
	}
}

// TestEveryCacheResultCarriesItsRenderedSizes walks each cache response shape
// and asserts no size crosses the wire as a bare number.
//
// It reads the encoded JSON rather than the struct so the assertion is about
// what a caller receives. A size field with no rendered sibling is exactly the
// state this change removed, and a shape added later without one fails here.
func TestEveryCacheResultCarriesItsRenderedSizes(t *testing.T) {
	t.Parallel()

	const n = int64(3 << 20)
	rendered := cacheops.HumanSize(n)

	// Each case is one response as the daemon assembles it, with every size set
	// to the same quantity so a missing rendered field is visible as an absent
	// or empty key rather than as a value that happens to look plausible.
	cases := []struct {
		name string
		// pairs maps a numeric JSON key to the key that must carry its text.
		pairs   map[string]string
		payload any
	}{
		{
			name:  "status",
			pairs: map[string]string{"totalSize": "totalSizeText"},
			payload: cacheStatusResult{
				Classes:       []cacheClassResult{},
				Provenance:    []cacheProvenanceResult{},
				TotalSize:     n,
				TotalSizeText: cacheops.HumanSize(n),
			},
		},
		{
			name:  "an enumerated element",
			pairs: map[string]string{"size": "sizeText"},
			payload: cacheElementResult{
				Label:    "an-element",
				Size:     n,
				SizeText: cacheops.HumanSize(n),
			},
		},
		{
			name:    "a removal",
			pairs:   map[string]string{"size": "sizeText"},
			payload: cacheRemovals([]cacheops.Removal{{Label: "an-element", Size: n}})[0],
		},
		{
			name:    "a shortfall",
			pairs:   map[string]string{"size": "sizeText"},
			payload: cacheShortfalls([]cacheops.Shortfall{{Class: "a-class", Count: 1, Size: n}})[0],
		},
		{
			name: "prune",
			pairs: map[string]string{
				"reclaimedBytes":   "reclaimedBytesText",
				"unattributedSize": "unattributedSizeText",
			},
			payload: cachePruneResult{
				ReclaimedBytes:       n,
				ReclaimedBytesText:   cacheops.HumanSize(n),
				UnattributedSize:     n,
				UnattributedSizeText: cacheops.HumanSize(n),
			},
		},
		{
			name: "evict",
			pairs: map[string]string{
				"totalBefore":    "totalBeforeText",
				"reclaimedBytes": "reclaimedBytesText",
				"remainingBytes": "remainingBytesText",
				"capBytes":       "capBytesText",
			},
			payload: cacheEvictResult{
				TotalBefore:        n,
				TotalBeforeText:    cacheops.HumanSize(n),
				ReclaimedBytes:     n,
				ReclaimedBytesText: cacheops.HumanSize(n),
				RemainingBytes:     n,
				RemainingBytesText: cacheops.HumanSize(n),
				CapBytes:           n,
				CapBytesText:       cacheops.HumanSize(n),
			},
		},
		{
			name: "a cleared subtree",
			pairs: map[string]string{
				"size":          "sizeText",
				"heldOpenBytes": "heldOpenBytesText",
			},
			payload: cacheClearedResult{
				Class:             "a-class",
				Size:              n,
				SizeText:          cacheops.HumanSize(n),
				HeldOpenBytes:     n,
				HeldOpenBytesText: cacheops.HumanSize(n),
			},
		},
		{
			name:  "clear",
			pairs: map[string]string{"reclaimedBytes": "reclaimedBytesText"},
			payload: cacheClearResult{
				Cleared:            []cacheClearedResult{},
				ReclaimedBytes:     n,
				ReclaimedBytesText: cacheops.HumanSize(n),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			encoded, err := json.Marshal(tc.payload)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var onTheWire map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &onTheWire); err != nil {
				t.Fatalf("unmarshal: %v (bytes: %s)", err, encoded)
			}

			for numberKey, textKey := range tc.pairs {
				if _, ok := onTheWire[numberKey]; !ok {
					t.Fatalf("the response carries no %q key at all: %s", numberKey, encoded)
				}
				raw, ok := onTheWire[textKey]
				if !ok {
					t.Fatalf("%q crosses the wire as a bare number: no %q beside it: %s",
						numberKey, textKey, encoded)
				}
				var text string
				if err := json.Unmarshal(raw, &text); err != nil {
					t.Fatalf("%q is not a string: %v", textKey, err)
				}
				if text != rendered {
					t.Errorf("%q is %q, want the shared formatter's %q", textKey, text, rendered)
				}
				parsed, perr := cacheops.ParseHumanSize(text)
				if perr != nil {
					t.Errorf("the parser refused %q from %q: %v", text, textKey, perr)
					continue
				}
				if parsed != n {
					t.Errorf("%q does not round-trip: %q parsed back to %d, want %d", textKey, text, parsed, n)
				}
			}
		})
	}
}

// TestAPluginSizeAndItsRenderedFormAreBothPresentOrBothAbsent holds the one
// optional pair to the same presence.
//
// The plugin size is carried only for a plugin entity, so a caller reading a
// rendered size with no number beside it — or the reverse — would have to guess
// which of the two to trust. The publication result therefore sets the rendered
// form only where the number is set.
func TestAPluginSizeAndItsRenderedFormAreBothPresentOrBothAbsent(t *testing.T) {
	t.Parallel()

	for _, pluginSize := range []int{0, 4 << 20} {
		out := publishEntityResult{
			Size:     1 << 20,
			SizeText: cacheops.HumanSize(1 << 20),
		}
		out.PluginSize = pluginSize
		if pluginSize != 0 {
			out.PluginSizeText = cacheops.HumanSize(int64(pluginSize))
		}

		encoded, err := json.Marshal(out)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		body := string(encoded)
		hasNumber := strings.Contains(body, `"pluginSize"`)
		hasText := strings.Contains(body, `"pluginSizeText"`)
		if hasNumber != hasText {
			t.Errorf("for a plugin size of %d the pair is half-present: number %v, text %v: %s",
				pluginSize, hasNumber, hasText, body)
		}
	}
}
