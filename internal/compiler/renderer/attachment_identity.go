// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import "github.com/aggrega-ai/aggrega-cli/pkg/namesegment"

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
// [attachmentIdentity.outputPrefix] writes the same parts into a FILENAME
// prefix, and it is encoded because a filename has no separator to fall back on:
// the parts and the file's own name share one string, and only an encoding keeps
// two distinct part lists at two distinct filenames.
//
// # Where the parts become a DIRECTORY they are composed by the group, not here
//
// A provider that gives each of its contributors a position of its own places an
// attachment at a position of its own too, beside the contributor it is attached
// to rather than inside it — so there the parts are one directory segment the
// dispatch group composes for every contributor alike, through
// [contributorDirSegment], and this value contributes no prefix at all. A
// provider that writes its group's output directly at the dispatch path, and one
// that folds it into a single document, have no such position, and that is the
// case this prefix answers.
//
// The two are not two spellings of one composition. A segment beside a target's
// own names is written in an alphabet drawn for a directory and a pointer to it,
// while a filename prefix is written in one drawn for a file — and each escapes
// what the other admits, which is why they are declared beside one another and
// reached separately.
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

// outputPrefix returns the FILENAME prefix every file the attachment emits is
// written under, beside the files of the component hosting it.
//
// It is the shape for a group whose provider gives no contributor a position of
// its own. Where the provider does give each contributor a position, the
// attachment gets one too — beside its host rather than inside it — and the
// coordinates become that position's directory segment instead, composed by the
// group through [contributorDirSegment]; this prefix is not used there at all.
//
// Two identity coordinates rather than one composed token. They are
// collision-free for the same reason a composed identity is — the host is what
// separates one instance name attached to several components — while staying
// legible, which a composed token is not. They are read through
// [attachmentIdentity.parts] rather than listed again here, so the prefix and the
// composed identity name this attachment from one value: a second listing could
// come to disagree about which coordinates identify it, with nothing comparing
// the two.
func (a attachmentIdentity) outputPrefix() string {
	return attachmentOutputPrefix(a.parts()...)
}

// attachmentFileEncoding is the segment encoding an attachment's filename prefix
// is written in.
//
// The alphabet is stated here rather than taken from the encoding, because it is
// a property of what a filename has to be rather than of the encoding — and it
// is deliberately not the alphabet the composed identity uses, which answers to
// what a label value accepts instead.
var attachmentFileEncoding = namesegment.NewEncoding('-', isAttachmentFileRune)

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

// contributorDirEncoding is the segment encoding a contributor's own directory is
// written in, declared here beside the filename encoding because the two answer
// the same question for two different positions and a reader comparing them
// should not have to go looking.
//
// It is ONE encoding reached by every nesting provider rather than one per
// provider, and that is the whole reason a third instance of this mechanic is
// accepted: a directory segment sits beside a target's own names, so were each
// guest to encode its own the targets would disagree about the spelling of a
// position the host composed.
var contributorDirEncoding = namesegment.NewEncoding('-', isContributorDirRune)

// isContributorDirRune reports whether a character is carried into a
// contributor's directory segment as itself: an ASCII letter of either case, a
// digit, a full stop, or an underscore. Every other character is escaped.
//
// # Why the set is drawn at the unreserved characters rather than at what a filesystem tolerates
//
// The segment is not only a path component. It is also carried inside a
// file-scheme URL, as the pointer an assembled artifact addresses this
// contributor's directory by — so a character legal in a filename and unsafe in a
// URL would make the directory legal on disk and wrong in the value that names
// it. The set is therefore the characters a URL leaves alone, less the two below:
// a number sign or a question mark would truncate the pointer, a percent sign
// would make it re-decode to something else, and each is a perfectly legal
// filename.
//
// A tilde is legal in both and is still escaped, because a leading one expands to
// a home directory at a shell and these segments are typed by people.
//
// # Uppercase is ADMITTED here and escaped in the filename encoding, deliberately
//
// The filename encoding escapes an uppercase letter because a
// case-insensitive filesystem would fold two distinct coordinates onto one FILE.
// A directory segment is not exposed to that: it is the position a contributor's
// whole set of files sits at, and two contributors reaching one position is
// answered by the attribution the assembled-file set carries rather than by the
// spelling. Escaping case here would buy nothing and would cost the readability
// that is the reason this encoding exists — an identity carrying a capital would
// otherwise reach a directory whose name is a run of hex digits.
//
// # The hyphen is the escape opener, so an authored one is mostly KEPT
//
// The opener is not a free choice: the separator between two coordinates is two
// openers, and a segment joining a host to an instance is written to read as
// `host--instance`, which fixes the opener as the hyphen. An authored hyphen is
// still carried through wherever the character after it is admitted, so an
// ordinary hyphenated identity reaches an identical directory name; only a
// doubled or trailing hyphen escapes, which is what keeps the join unambiguous.
//
// # Two edges the alphabet has to decide, and where each one lands
//
// A full stop is ADMITTED mid-name, so a dotted identity reaches a directory
// carrying that dot as written rather than a name interrupted by an escape. The
// names "." and ".." are not names at all, and for a segment composed from more
// than one coordinate they are unreachable rather than guarded: such a segment
// always carries the separator, and neither of those two strings does. For a
// segment composed from a single coordinate they ARE reachable — from a
// coordinate that is itself "." or ".." — and so is a segment opening with the
// escape opener, from a coordinate opening with a hyphen. Neither is introduced
// by this encoding: the same coordinate reaches the same directory name where no
// encoding is applied at all, because an admitted character is carried through as
// authored. Refusing them belongs to whatever guards a contributor's directory
// for every provider rather than to the alphabet, which cannot refuse a character
// it is required to carry.
func isContributorDirRune(r rune) bool {
	return r >= 'a' && r <= 'z' ||
		r >= 'A' && r <= 'Z' ||
		r >= '0' && r <= '9' ||
		r == '.' || r == '_'
}

// contributorDirSegment returns the directory segment one contributor's files sit
// under, composed from the coordinates that identify it.
//
// The coordinates are encoded PER PART and joined by the encoding's own
// multi-part entry point rather than run together, and that is what makes the
// segment injective: a raw two-hyphen join is not. The pair ("a--b", "c") and the
// pair ("a", "b--c") both reach "a--b--c" when the parts are concatenated, so one
// directory would hold two contributors; encoding each part first separates them
// as "a-zh-b--c" and "a--b-zh-c".
//
// # One function for every arity, which is what keeps the arities apart
//
// A contributor identified by one coordinate and a contributor identified by two
// both compose here, and the encoding's assembled name records how many there
// were — so a component whose own name is "a--b" and an attachment "b" on host
// "a" reach different segments rather than meeting at one directory. Composing
// only the two-coordinate case here and leaving the one-coordinate case as the
// raw name would reintroduce exactly that meet, and it would be invisible: two
// contributors sharing a directory with different file names collide on no path,
// so the attribution that reports two producers meeting at one path would report
// nothing.
//
// For a coordinate the alphabet already admits the result is the coordinate
// itself, so an ordinary name reaches the directory it always reached.
func contributorDirSegment(parts ...string) string {
	return contributorDirEncoding.Name(parts...)
}

// attachmentOutputPrefix returns the FILENAME prefix every file an attachment
// emits is written under, for a contributor the given coordinates identify.
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
// # It answers for ONE assembly shape, and the other has no prefix at all
//
// This is the shape for a group whose provider gives no contributor a position of
// its own — one writing its output directly at the dispatch path, or folding it
// into a single document. There is no enclosing directory to put the coordinates
// in, so they are encoded into a filename prefix and joined to the file's own
// name, which lands the attachment's files directly beside the files of whatever
// it is attached to.
//
// A group whose provider DOES give each contributor a position places the
// attachment at one too, beside its host rather than inside it, and the
// coordinates become that position's directory segment. That composition is
// [contributorDirSegment] and it is written in a different alphabet, for the
// reason recorded there: a directory segment and a filename admit different
// characters. Nothing here is reached in that case.
//
// # There is no enclosing directory for an attachment, on any assembly shape
//
// Neither shape puts an attachment's output inside a directory named for the
// authoring surface it came from, and the host holds no word for one. Beside is
// what a nesting group's target can read: it discovers its contributors at the
// positions the group gives them, so an attachment one level below one of those
// positions is not a contributor at all — it is content belonging to whichever
// contributor's position encloses it, addressed by nothing and loaded by nothing.
// A flat or folding group has no per-contributor position to be beside, which is
// what leaves the coordinates nowhere to go but the filename.
//
// So there is nothing left for a provider to declare here either. A provider
// states how it assembles a dispatch group and, where that assembly gives each
// contributor a position, what directory those positions sit under — and those
// two are the whole of what it declares about layout. A third declaration naming
// an attachment's enclosing directory would be free to contradict the first: a
// provider declaring that its target reads only what sits directly in a
// directory, and a directory for its attachments, would be describing two layouts
// at once.
//
// # What the flat shape guarantees, and what it does NOT
//
// 🔴 The flat filename is injective in its CONTRIBUTOR PREFIX and is not
// injective in the file name that follows it.
//
// The prefix is injective because the coordinates are encoded rather than
// joined raw, and because the prefix meets the file's own name at
// [namesegment.Encoding.SegmentSeparator] — the one sequence no encoded
// coordinate can contain. So for one file name, two distinct coordinate lists
// always reach two
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
func attachmentOutputPrefix(parts ...string) string {
	return attachmentFileEncoding.Name(parts...) + attachmentFileEncoding.SegmentSeparator()
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
