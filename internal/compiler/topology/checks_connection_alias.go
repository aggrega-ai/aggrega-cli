// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package topology

import (
	"strconv"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// ConnectionAliasNamesOneConnection reports each connection of a component wired
// under an alias an earlier connection of the same component already holds.
//
// # Why the alias, and not the socket and the alias
//
// An alias is the name a component reads a connection by, and the two views a
// component is given of its connections key it differently: a Recipe template
// reads them keyed by alias alone, and an assertion keyed by socket and then by
// alias. Two connections sharing one alias therefore share one slot in at least
// one of those views — on one socket, in both — and the later one fills it, so
// the other is absent from what the component's own Recipe or assertions see.
// Refusing the pair over the graph both views are built from closes both at
// once, which is why the rule is one alias per component rather than one alias
// per socket.
//
// Presence is the whole signal. The rule compares no targets and does not treat
// two connections naming one target as acceptable: a list naming one target twice
// is two connections with one name, and only one of them is visible.
//
// # Whatever drew each connection
//
// It is invoked once per connection together with the surface that drew it, so a
// connection a component's own block drew, one an attachment's block drew at any
// scope, and one the Release's edge surface drew are each asked. The connections
// it compares against are every connection the composition drew on the node, in
// the order the graph holds them, so the first connection under an alias is the
// one the views would show and every later one is the connection refused.
//
// # Where each finding is, and what it names
//
// The finding is raised on the later connection, so it is positioned at that
// connection's own entry and filed under the document that entry was written in.
// The connection already holding the alias is named in the detail by its socket,
// its target, and the file and key path of its own entry, read from the same
// record the position is assembled by. Where both connections are entries of one
// list — a list naming one target twice — each is also named by its place in
// the list, which is the only thing that tells the two entries apart.
//
// A key repeated in one block of keyed entries is refused before this rule could
// be asked, because the document does not load; and a Release entry at a socket
// and alias some other block drew configures that connection rather than drawing
// a second one. Neither reaches this rule as a pair.
var ConnectionAliasNamesOneConnection = constraint.Check[
	constraint.GraphView,
	constraint.OriginatedConnectionSubject,
]{
	Sentinel: ErrDuplicateConnectionAlias(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.GraphView, s constraint.OriginatedConnectionSubject) []constraint.Objection {
		drawn := v.NodeDrawnConnections(s.NodeID())
		holder := -1
		var out []constraint.Objection
		for i, c := range drawn {
			if c.Alias != s.AliasName() {
				continue
			}
			if holder < 0 {
				holder = i
				continue
			}
			// Only the connections this subject stands for are refused here: every
			// other connection under the alias is its own subject and reports at
			// its own entry.
			if c.Origin != s.OriginKind() || c.Attachment != s.OriginAttachment() ||
				c.Socket != s.SocketName() || c.Target != s.TargetNodeID() {
				continue
			}
			held := drawn[holder]
			detail := []constraint.DetailValue{
				constraint.Uncaptioned(s.AliasName()),
				constraint.Captioned("on component", s.NodeID()),
				constraint.Captioned("this connection wired into socket", c.Socket),
				constraint.Captioned("with target", c.Target),
			}
			detail = append(detail, connectionPlace(c, held)...)
			detail = append(detail,
				constraint.Captioned("the connection already holding the alias wired into socket", held.Socket),
				constraint.Captioned("with target", held.Target),
			)
			detail = append(detail, connectionPlace(held, c)...)
			out = append(out, s.RaiseWithAdvice(
				ErrDuplicateConnectionAlias(),
				"Give one of the two connections an alias of its own: a component reads each connection by its "+
					"alias, whatever socket it is wired into. A list entry takes its target's name as its alias, "+
					"so a target listed twice is listed once, or written as two keyed entries under two aliases.",
				detail...,
			))
		}
		return out
	},
}

// connectionPlace spells where one connection's entry was written, as the
// captioned values a finding carries: the file, the key path inside it, and —
// where the other connection of the pair is an entry of the same list — the
// entry's place in that list, which is then the only thing telling the two
// apart.
//
// Two connections are entries of one list when one block drew both on one
// socket and the record places each among what that block drew. Under one alias
// nothing else can be: a block's keyed entries cannot repeat a key, so a block
// drawing two connections under one alias on one socket wrote that socket as a
// list. The block is compared rather than the file and key path, because two
// blocks whose place nothing recorded share an empty file and an empty path and
// would otherwise be named as two places in a list neither was written as.
//
// A part the record could not name is left out rather than spelled empty, so a
// finding never points at a file or a path nothing recorded.
func connectionPlace(c, other constraint.NodeConnection) []constraint.DetailValue {
	var detail []constraint.DetailValue
	if c.SourceFile != "" {
		detail = append(detail, constraint.Captioned("written in", c.SourceFile))
	}
	if c.Locator != "" {
		detail = append(detail, constraint.Captioned("at", c.Locator))
	}
	if entriesOfOneList(c, other) {
		detail = append(detail, constraint.Captioned("list entry", strconv.Itoa(c.Position)))
	}
	return detail
}

// entriesOfOneList reports whether two connections are entries of one list: one
// block drew both on one socket, and the record names each one's place among the
// connections that block drew there. A position of zero is the record saying no
// single block can be named, so it never pairs.
func entriesOfOneList(c, other constraint.NodeConnection) bool {
	return c.Origin == other.Origin && c.Attachment == other.Attachment && c.Socket == other.Socket &&
		c.Position > 0 && other.Position > 0
}
