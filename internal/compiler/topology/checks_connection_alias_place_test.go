// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"slices"
	"strconv"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// TestAListPositionIsNamedOnlyForTwoEntriesOfOneList pins when the alias refusal
// names a connection's place in a list, at the level of the per-connection record
// the place is read from.
//
// The grid through the real build covers the shapes every record carries a file
// and key path for. One shape it cannot reach is held here instead: two
// connections drawn by two different blocks whose place nothing recorded — a
// module's own scope-wide attachments — carry the same empty file, the same empty
// key path and each a position of one, and are two keyed entries rather than two
// places in a list.
func TestAListPositionIsNamedOnlyForTwoEntriesOfOneList(t *testing.T) {
	t.Parallel()

	// ownList is an entry of the component's own list on socket, at position.
	ownList := func(socket string, position int) constraint.NodeConnection {
		return constraint.NodeConnection{
			Socket: socket, Alias: "db", Target: "db", Origin: constraint.OriginComponent,
			SourceFile: "aggregate.yaml", Locator: ".spec.components.app.uses." + socket, Position: position,
		}
	}
	// unrecorded is a keyed entry of an attachment block whose place nothing
	// recorded.
	unrecorded := func(attachment, target string) constraint.NodeConnection {
		return constraint.NodeConnection{
			Socket: "models", Alias: "shared", Target: target, Origin: constraint.OriginAttachment,
			Attachment: attachment, Position: 1,
		}
	}
	// sharedName is a connection drawn by an attachment name more than one scope
	// declares, which the record places in no single block.
	sharedName := func(target string) constraint.NodeConnection {
		return constraint.NodeConnection{
			Socket: "models", Alias: "local", Target: target, Origin: constraint.OriginAttachment,
			Attachment: "marker", SourceFile: "aggregate.yaml",
			Locator: ".spec.components.app.features.marker.uses.models.local",
		}
	}

	cases := []struct {
		name        string
		later, held constraint.NodeConnection
		namesPlaces bool
	}{
		{
			name:        "control: a list naming one target twice names both places",
			later:       ownList("models", 2),
			held:        ownList("models", 1),
			namesPlaces: true,
		},
		{
			name:  "one alias on two sockets, each a list: two sockets of one block, no place",
			later: ownList("databases", 1),
			held:  ownList("models", 1),
		},
		{
			name:  "two blocks whose place nothing recorded: equal empty file and path, still no place",
			later: unrecorded("modb", "llm2"),
			held:  unrecorded("moda", "llm"),
		},
		{
			name:  "one attachment name at two scopes: the record names no single block, so no place",
			later: sharedName("db2"),
			held:  sharedName("db"),
		},
		{
			name: "a Release entry beside the component's own list entry: two surfaces, no place",
			later: constraint.NodeConnection{
				Socket: "models", Alias: "db", Target: "db", Origin: constraint.OriginRelease,
				SourceFile: "release.yaml", Locator: ".spec.components.app.uses.models.db",
			},
			held: ownList("models", 1),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := entriesOfOneList(tc.later, tc.held); got != tc.namesPlaces {
				t.Fatalf("entriesOfOneList = %v; want %v", got, tc.namesPlaces)
			}
			for _, pair := range [][2]constraint.NodeConnection{{tc.later, tc.held}, {tc.held, tc.later}} {
				c, other := pair[0], pair[1]
				place := constraint.Captioned("list entry", strconv.Itoa(c.Position))
				if named := slices.Contains(connectionPlace(c, other), place); named != tc.namesPlaces {
					t.Errorf("connectionPlace names the list position: %v for %+v; want %v", named, c, tc.namesPlaces)
				}
			}
		})
	}
}
