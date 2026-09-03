// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/namesegment"
)

// attachmentIdentity names the two authored parts a feature attached to ONE
// component is identified by: the identity of the component hosting it, and the
// name it is attached under.
//
// The host is a part rather than context a reader is expected to supply, because
// it is what separates one instance name attached to several components. Those
// attachments emit different resources and so need to be told apart, and an
// author who names them alike has written nothing wrong — a cascade that mints
// one instance name across many components by design gives an author no way to
// write anything else.
//
// # Why the parts are stated once, here
//
// THREE things express that pair: the component identity every resource the
// attachment emits is stamped with, the directory the attachment's files are
// written to, and the dispatch identity its group holds its contribution under.
// Three expressions of one fact drift, so all of them are derived from this
// one value rather than each assembling the parts for itself. A caller building
// the directory by running the host and the instance together beside the
// identity composition would re-introduce exactly that drift — the identity and
// the path could then come to disagree about which attachment they name, with
// nothing comparing them.
//
// # The two derivations are written differently, and deliberately
//
// [attachmentIdentity.parts] feeds a composition that writes each part into the
// label alphabet, escaping whatever that alphabet refuses, because a label value
// admits a narrow character set and two parts differing at all must reach two
// values.
//
// [attachmentIdentity.outputPrefix] writes the same parts, and whether it
// escapes them follows from which position its provider asked for. Where the
// parts become DIRECTORY segments no writing is applied: a path segment is not a
// label value, every character an author may put in a component identity is
// legal in a directory name, and escaping one would leave the directory
// unreadable for no gain — so a host whose id carries a dot gives a directory
// carrying that dot as written. Where instead they become a FILENAME prefix,
// they are encoded, because a filename has no separator to fall back on: the
// parts and the file's own name then share one string, and only an encoding
// keeps two distinct part lists at two distinct filenames.
//
// # What ENCLOSES those segments is the provider's, not this value's
//
// The directory the two segments sit under is declared by the provider whose
// dispatch group holds the output, so it arrives as an argument rather than
// being written here. Two providers that combine their contributors the same way
// can need opposite answers — one whose target reads only the files sitting
// directly in a directory, one whose target discovers its units in a named child
// directory — and nothing else either of them declares tells the two apart.
//
// # The boundary-scope sibling keeps a different shape, by SCOPE
//
// A feature attached to a BOUNDARY rather than to a component writes its output
// under the instance name alone. That is not an inconsistency waiting to be
// unified: at boundary scope the enclosing directory already is the boundary, so
// naming a host would repeat what the location says — and such a feature has no
// host component to name in the first place. The difference is one of scope
// rather than of input kind, and the location is what disambiguates it.
type attachmentIdentity struct {
	host     string
	instance string
}

// named reports whether this value identifies an attachment at all.
//
// A render reached from a boundary-scope caller carries no host and no instance,
// because the identity its resources are stamped with is composed from the
// boundary path instead and is settled before that render begins.
func (a attachmentIdentity) named() bool { return a.instance != "" }

// parts returns the authored parts the attachment's composed component identity
// is assembled from, in the order the composition writes them.
func (a attachmentIdentity) parts() []string { return []string{a.host, a.instance} }

// outputPrefix returns the prefix every file the attachment emits is written
// under, within the position of the component hosting it.
//
// attachmentDir is resolved from the group provider's own declaration and, where
// it names a directory, already carries its trailing separator — so it is joined
// rather than re-composed here. It is the provider's word, or the host's default
// where the provider names none, and this function reads neither, which is what
// keeps a target's directory vocabulary out of the composition. Where the
// provider declares no directory at all it is empty, and the coordinates move
// into the filename; [attachmentOutputPrefix] carries both shapes.
//
// Two identity coordinates rather than one composed token. They are
// collision-free for the same reason a composed identity is — the host is what
// separates one instance name attached to several components — while staying
// legible, which a composed token is not. They are read through
// [attachmentIdentity.parts] rather than listed again here, so the prefix and the
// composed identity name this attachment from one value: a second listing could
// come to disagree about which coordinates identify it, with nothing comparing
// the two.
func (a attachmentIdentity) outputPrefix(attachmentDir string) string {
	return attachmentOutputPrefix(attachmentDir, a.parts()...)
}

// attachmentFileEncoding is the segment encoding an attachment's filename prefix
// is written in.
//
// The alphabet is stated here rather than taken from the encoding, because it is
// a property of what a filename has to be rather than of the encoding — and it
// is deliberately not the alphabet the composed identity uses, which answers to
// what a label value accepts instead.
var attachmentFileEncoding = namesegment.NewEncoding(isAttachmentFileRune)

// isAttachmentFileRune reports whether a character is carried into an
// attachment's filename prefix as itself: a lowercase ASCII letter or a digit.
// Every other character is escaped, and three of the alphabet's edges are why.
//
// A path separator is escaped, which is what makes the result a filename prefix
// at all. A component identity may be composed from a nested path, so a
// separator carried through as authored would open a directory level and put the
// files back underneath one — the very position naming no directory exists to
// leave.
//
// An uppercase letter is escaped rather than admitted. An authored coordinate
// stays cased, so admitting uppercase would give a derivation injective as a
// string and not injective as a filename: a filesystem that compares names
// case-insensitively would fold two distinct coordinates onto one file.
//
// The full stop is escaped for a third reason. It would otherwise let a prefix
// be a relative-path reference, and let a derived filename begin with the
// character that hides a file — while the file's own name keeps whatever full
// stops it carries, so its suffix still reads as a suffix.
func isAttachmentFileRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
}

// attachmentOutputPrefix returns the prefix every file an attachment emits is
// written under, for a contributor the given coordinates identify.
//
// It is the one home of that composition because both attachment scopes make it
// and they differ only in the coordinates they pass: a component-scope
// attachment names its host as well as its instance, while a boundary-scope one
// sits inside the boundary already, so the instance alone identifies it. That
// difference is one of scope rather than of layout, so it stays with each caller
// while the shape of the prefix stays here — and a second spelling of the shape
// would let the two scopes come to lay their output out differently for no
// reason either could state.
//
// # The two shapes, and which one the provider asked for
//
// Where attachmentDir names a directory, the coordinates follow it as directory
// segments, each as authored. Where it is empty the provider has declared that
// its target reads only the files sitting directly in a directory, so there is
// no enclosing directory to put them in: the coordinates are encoded into a
// FILENAME prefix instead and joined to the file's own name, which lands the
// attachment's files directly beside the files of whatever it is attached to.
//
// # What the flat shape guarantees, and what it does NOT
//
// 🔴 The flat filename is injective in its CONTRIBUTOR PREFIX and is not
// injective in the file name that follows it.
//
// The prefix is injective because the coordinates are encoded rather than
// joined raw, and because the prefix meets the file's own name at
// [namesegment.SegmentSeparator] — the one sequence no encoded coordinate can
// contain. So for one file name, two distinct coordinate lists always reach two
// distinct filenames, including lists of different lengths, and including one
// scope's list against the other's. A shorter boundary would not carry that: on
// a single hyphen, coordinates ("a", "b") with the name "c-x.yaml" and
// coordinates ("a", "b-c") with the name "x.yaml" both assemble to
// "a--b-c-x.yaml", which is exactly the collision the encoding exists to
// prevent.
//
// What it does not carry is any claim about the file's own name, which arrives
// as the rendering provider wrote it and may well have folded distinct resource
// identities onto one string. Two resource identities within ONE contributor
// that differ only where that name folds still meet, and they meet before this
// composition sees them — which is why the provider that derives such a name is
// where that collision is answered.
func attachmentOutputPrefix(attachmentDir string, parts ...string) string {
	if attachmentDir == "" {
		return attachmentFileEncoding.Name(parts...) + namesegment.SegmentSeparator
	}
	return attachmentDir + strings.Join(parts, "/") + "/"
}

// attachmentNodePrefix marks a synthetic per-node key that carries a
// component-scope attachment's contribution rather than a component's own
// render. It is declared once so the key's construction and every reader of it
// agree, and it is spelled apart from the boundary-scope marker because the two
// name different kinds of contributor and a group can hold both.
const attachmentNodePrefix = "<attachment>:"

// dispatchNodeID is the identity the attachment's dispatch group holds its
// contribution under: a marker saying what kind of contributor it is, followed by
// both authored coordinates.
//
// Both coordinates are present because either alone identifies the wrong thing.
// The instance name alone is shared by every component the same feature is
// attached to, and those components can sit in one dispatch group — so an
// identity without the host arrives at that group as one contributor where
// several were declared, and all but one are lost. The host alone is shared by
// every attachment on that component.
//
// 🔴 It is a marker for the compiler's own dispatch and is NEVER a name a target
// accepts: it opens with an angle bracket and joins with a colon and an at sign,
// which a path refuses on some platforms and a name alphabet refuses outright.
// Nothing reads it back apart either — it joins its two parts with characters
// either part may itself contain, so cutting it cannot return what went in. What
// a target's own naming reads instead is the basis carried beside the
// contribution, and what a reader meets is the coordinates recorded beside it.
func (a attachmentIdentity) dispatchNodeID() string {
	return attachmentNodePrefix + a.host + "@" + a.instance
}
