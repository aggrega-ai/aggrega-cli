// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
)

// What a caller may be told about a place on the host this server runs on.
//
// A client that launched this process reaches it over the process's own streams
// and runs as the person who started it, so every location an answer could name
// is one that caller already owns, and it is told each one as the operation holds
// it. A caller reaching a listener is somewhere else: it chose none of those
// locations, it cannot open them, and a server that has published them cannot
// take them back. That caller is told a location inside the workspace this server
// serves by its name relative to the workspace root — the same form its own path
// arguments take, so a name read from one answer can be sent back in the next
// call — and nothing at all about any other location, which is kept back and
// marked as kept back. The workspace root itself is kept back: it is the one
// workspace this process serves, so it is a place the caller cannot act on and
// does not need in order to name anything.
//
// The judgement is made in ONE place, [toolWorkspace.reportedLocation]. Every
// answer field that holds a location reads it, and so does the rewriting of
// prose, so a structured field and the sentence describing it cannot come to
// spell one location two ways.

// keptBackLocationWords is what prose reads where it would have named a location
// the caller is not told.
//
// It is one fixed phrase rather than something composed per location, because
// anything composed from the location would be a partial disclosure of it.
const keptBackLocationWords = "(location withheld)"

// servedWorkspaceWords is how an answer's own text names the workspace root where
// the root is kept back. A caller reaching a listener is served exactly one
// workspace, so the phrase identifies it without naming where it sits.
const servedWorkspaceWords = "the workspace this server serves"

// reportedLocation reports what the caller of this call may be told about loc, a
// place on this host: the spelling to tell it, and whether it may be told
// anything at all.
//
// Over the process's own streams loc is told exactly as it arrived. Over a
// listener it is told by its name relative to the workspace root where it lies
// inside the workspace, and kept back otherwise — the root itself included. The
// containment verdict is [pathscope.Relative]'s, the same resolver that confines
// every path a listener caller sends, and nothing here re-derives it. A value
// already spelled relative to the root is taken as it stands, so a name an
// operation reports in that coordinate passes through unchanged.
//
// 🔴 The verdict is LEXICAL, and the direction it errs in is the one that keeps
// back. A value spelled relative that climbs above the root is kept back even
// where it would land back inside it, and a location spelled through the target
// of a symbolic link rather than through the root the server was given reads as
// lying outside that root. Both are kept back as outside locations: the caller is
// told less than it could have been, never a place outside the workspace.
//
// An empty loc names no place, and is told as it is on both transports: there is
// nothing in it to keep back, and marking it would claim a location existed.
func (tw *toolWorkspace) reportedLocation(loc string) (string, bool) {
	if loc == "" || !tw.withholdsHostLocations() {
		return loc, true
	}
	return pathscope.Relative(tw.root, loc)
}

// reportedLocationField is [toolWorkspace.reportedLocation] in the shape an
// answer carries a location in: the spelling the caller is told, and whether it
// was kept back. A kept-back location has no spelling at all, so a caller reads
// the mark beside it rather than an empty value that looks like nothing was
// there.
func (tw *toolWorkspace) reportedLocationField(loc string) (*string, bool) {
	name, told := tw.reportedLocation(loc)
	if !told {
		return nil, true
	}
	return &name, false
}

// reportedLocationWords renders a location an answer carries, for that answer's
// own text: the spelling the caller was told, or the kept-back phrase where it
// was told none.
func reportedLocationWords(loc *string) string {
	if loc == nil {
		return keptBackLocationWords
	}
	return *loc
}

// workspaceRootWords renders the workspace root an answer carries, for that
// answer's own text. A root kept back is named as the workspace this server
// serves, which is the one workspace such a caller can be answering about.
func workspaceRootWords(root *string) string {
	if root == nil {
		return servedWorkspaceWords
	}
	return *root
}

// reportedText is what the caller of this call may be told of prose this surface
// did not compose for it — an operation's error, a resolution's diagnostic line,
// a command's captured output — where that prose names a place on this host.
//
// Over the process's own streams the text is told as it is. Over a listener,
// each location in it that this call's workspace holds is spelled as
// [toolWorkspace.reportedLocation] spells it: by its name relative to the
// workspace root, or by the kept-back phrase. Every rewrite is that function's
// answer for the location it replaces, so the prose and the structured half of
// one answer cannot disagree about a location.
//
// 🔴 It looks for the locations the workspace holds and decides nothing about
// whether a string looks like a path. Those are the root and anything under it,
// the config files the configuration cascade read, and the directories the
// catalog is read from — the workspace's own stack and every stack the
// configuration gives a single Release. A location outside all of those that a
// lower layer composed into a message is not recognised and reaches the caller as
// it was written; that is the bound of this rule, and it is stated rather than
// papered over with a guess at what else might be a path.
//
// A location's extent in prose starts where a held location begins at the start
// of the text or after a character that cannot continue a path as an answer
// writes one — whitespace, a quote, a bracket, a comma, a semicolon, a colon, an
// equals sign or a vertical bar — and it runs to the next such character, less
// any full stops that close the sentence it ends. The spelling so delimited is
// the location judged: it is rewritten only where it IS a held location or lies
// under one, so a sibling directory whose name merely extends the root's is left
// as it stands, and a climb out of the root spelled under it is kept back. A
// location whose own name carries one of those characters is judged on the part
// before it; that part is still its leading directories, so no absolute location
// under a held one survives the rewrite.
func (tw *toolWorkspace) reportedText(text string) string {
	if text == "" || !tw.withholdsHostLocations() {
		return text
	}
	held := tw.heldLocations()
	if len(held) == 0 {
		return text
	}
	var b strings.Builder
	copied := 0
	for from := 0; from < len(text); {
		start := nextHeldLocation(text, from, held)
		if start < 0 {
			break
		}
		end := locationExtent(text, start)
		spelled := text[start:end]
		if !underAHeldLocation(spelled, held) {
			from = start + 1
			continue
		}
		words, told := tw.reportedLocation(spelled)
		if !told {
			words = keptBackLocationWords
		}
		b.WriteString(text[copied:start])
		b.WriteString(words)
		copied, from = end, end
	}
	if copied == 0 {
		return text
	}
	b.WriteString(text[copied:])
	return b.String()
}

// reportedTexts is [toolWorkspace.reportedText] over a list an answer carries,
// such as its diagnostic lines. A nil list stays nil and every entry keeps its
// place, so the answer's shape is the same whatever was rewritten.
func (tw *toolWorkspace) reportedTexts(lines []string) []string {
	if len(lines) == 0 || !tw.withholdsHostLocations() {
		return lines
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = tw.reportedText(line)
	}
	return out
}

// heldLocations lists the places on this host this call's workspace holds, each
// absolute and cleaned: its root, the config files the cascade read, and every
// directory a catalog of it is read from.
//
// The config files are the contributing layers the cascade labels as files,
// classified by the one function that produces those labels rather than by the
// label's text. The catalog directories are resolved through the one resolver
// every surface reads a stack through, for the workspace and for each Release the
// configuration gives directories of its own. Both collections arrive in the
// cascade's own order, so the list is the same for identical input.
func (tw *toolWorkspace) heldLocations() []string {
	var held []string
	add := func(loc string) {
		if loc != "" && filepath.IsAbs(loc) {
			held = append(held, filepath.Clean(loc))
		}
	}
	add(tw.root)
	if tw.rw == nil {
		return held
	}
	for _, layer := range tw.rw.ContributingLayers() {
		if sourceNamesAHostLocation(layer) {
			add(layer)
		}
	}
	for _, dir := range tw.dirs {
		add(dir)
	}
	for _, release := range tw.cfg.Workspace.Releases.Keys() {
		for _, dir := range tw.rw.ResolveCatalogDirs(release, tw.flagDirs) {
			add(dir)
		}
	}
	return held
}

// nextHeldLocation reports the earliest index at or after from where one of the
// held locations begins at a location boundary, or -1 where none does.
func nextHeldLocation(text string, from int, held []string) int {
	earliest := -1
	for _, loc := range held {
		for at := from; at < len(text); {
			i := strings.Index(text[at:], loc)
			if i < 0 {
				break
			}
			i += at
			if opensALocation(text, i) {
				if earliest < 0 || i < earliest {
					earliest = i
				}
				break
			}
			at = i + 1
		}
	}
	return earliest
}

// opensALocation reports whether a location may begin at index i: at the start
// of the text, or right after a character that cannot continue a path.
func opensALocation(text string, i int) bool {
	if i == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(text[:i])
	return endsALocation(r)
}

// locationExtent reports where the location beginning at start ends: at the
// first character that cannot continue a path, less the full stops that close a
// sentence ending there. The location keeps at least its first character.
func locationExtent(text string, start int) int {
	end := len(text)
	for i, r := range text[start:] {
		if endsALocation(r) {
			end = start + i
			break
		}
	}
	for end > start+1 && text[end-1] == '.' {
		end--
	}
	return end
}

// endsALocation reports whether r is a character an answer's prose writes around
// a location rather than inside one.
func endsALocation(r rune) bool {
	if unicode.IsSpace(r) {
		return true
	}
	switch r {
	case '"', '\'', '`', ',', ';', ':', '(', ')', '[', ']', '{', '}', '<', '>', '=', '|':
		return true
	}
	return false
}

// underAHeldLocation reports whether spelled is one of the held locations or lies
// under one of them, judged at a separator boundary so that a sibling whose name
// merely extends a held location's is not taken for it.
func underAHeldLocation(spelled string, held []string) bool {
	for _, loc := range held {
		if spelled == loc || strings.HasPrefix(spelled, loc+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// answer carries this workspace out on a tool's result, so the one point every
// result leaves dispatch through reads the locations of the workspace the call
// itself resolved rather than resolving one a second time. It returns the pair it
// was handed, so a handler wraps its own return in it.
func (tw *toolWorkspace) answer(res *callToolResult, rpcErr *rpcError) (*callToolResult, *rpcError) {
	if res != nil {
		res.workspace = tw
	}
	return res, rpcErr
}

// servedWorkspace is the workspace an answer is read against where the tool that
// composed it carried none — a refusal composed before any workspace resolved,
// or an operation that resolves its own out of reach of the handler.
//
// Over the process's own streams nothing is rewritten, so nothing is resolved and
// the workspace returned carries the transport alone. Over a listener it is the
// served workspace, resolved exactly as every tool resolves it, since a caller
// there cannot name another. A workspace that does not resolve falls back as
// [server.resolveServedWorkspace] states, to the directory the server was started
// over. That directory is held only where it is absolute, as every held location
// is, so a start directory spelled relative, or left empty to begin from the
// process's working directory, holds nothing to match in prose.
//
// Nothing is kept between calls: each answer resolves its own, so two concurrent
// calls never share what one of them read.
func (s *server) servedWorkspace() *toolWorkspace {
	unresolved := &toolWorkspace{transport: s.transport}
	if !unresolved.withholdsHostLocations() {
		return unresolved
	}
	return s.resolveServedWorkspace()
}

// withoutHostLocations is the one point every tool's result passes on its way to
// the caller, and it rewrites that result's text for the transport this session
// serves: over a listener, each location the workspace holds is spelled the way
// the caller may be told it, and over the process's own streams the result is
// handed on as it was composed.
//
// It reads the text blocks and never the structured payload. Each tool routes its
// own structured fields, so a comparison of the two transports' payloads still
// sees a tool that left a location in one of them — which a second pass over the
// payload here would hide.
func (s *server) withoutHostLocations(res *callToolResult) *callToolResult {
	if res == nil || len(res.Content) == 0 {
		return res
	}
	tw := res.workspace
	if tw == nil {
		tw = s.servedWorkspace()
	}
	for i := range res.Content {
		res.Content[i].Text = tw.reportedText(res.Content[i].Text)
	}
	return res
}
