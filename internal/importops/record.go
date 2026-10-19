// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
)

// RecordFileName is the name of the record an import writes beside its drafts,
// in the directory the drafted paths are relative to.
//
// The record lists every part the import drafted and a digest of the text it
// wrote for each, which is what lets a later sync tell a part the import wrote
// and nobody has touched from a part an author edited or added. It is JSON
// rather than YAML so that nothing reading the workspace's YAML documents reads
// it, and its name opens with a full stop so a directory listing keeps it out of
// the author's way.
const RecordFileName = ".aggrega-import.json"

// importRecord is the record's document.
//
// Target is the target the import was given — the provider capability its
// drafted Manifest is for — and is empty, and left out of the record, for an
// import given none. A sync given no target reuses it, so a sync that leaves the
// target out drafts the same Manifest the import it brings up to date drafted.
//
// Files is in the order the importer drafted the files, each path once. A file
// owned whole carries Digest; a drafted Aggregate or Release carries Parts, one
// per mapping entry, in document order. Both are slices rather than maps, so the
// record's bytes are a function of the draft alone.
type importRecord struct {
	Target string       `json:"target,omitempty"`
	Files  []recordFile `json:"files"`
}

// recordFile is one drafted file in the record.
type recordFile struct {
	Path   string       `json:"path"`
	Digest string       `json:"digest,omitempty"`
	Parts  []recordPart `json:"parts,omitempty"`
}

// recordPart is one mapping entry of a drafted Aggregate or Release in the
// record: the keys leading to it from the top of the document, and the digest of
// its text.
type recordPart struct {
	Key    []string `json:"key"`
	Digest string   `json:"digest"`
}

// draftedFile is one file of a draft, each path once, with the content the
// import writes there.
//
// doc is set where the content is a drafted Aggregate or Release the import owns
// entry by entry; a file whose doc is nil is owned whole.
type draftedFile struct {
	path    string
	content string
	doc     *yamlDoc
}

// draftedFiles turns the importer's files into the set the record describes:
// each path once, at the place it first appears, holding the content it is
// written with last — which is what a write of the importer's files in order
// leaves on disk.
//
// A drafted Aggregate and a drafted Release are each owned entry by entry,
// because each is a document an author goes on editing once it is drafted — the
// drafted Release asks for its environment to be filled in — so a key an author
// adds to either is a key the author wrote, and a sync changes only the entries
// the source changed. Every other drafted file is owned whole.
func draftedFiles(files []abi.ScaffoldFile) []draftedFile {
	at := make(map[string]int, len(files))
	out := make([]draftedFile, 0, len(files))
	for _, f := range files {
		if i, seen := at[f.Path]; seen {
			out[i].content = f.Content
			continue
		}
		at[f.Path] = len(out)
		out = append(out, draftedFile{path: f.Path, content: f.Content})
	}
	for i := range out {
		if ownedByEntries([]byte(out[i].content)) {
			if doc, err := indexYAML(out[i].content); err == nil {
				out[i].doc = doc
			}
		}
	}
	return out
}

// recordFor composes the record of what a draft writes, for the target the draft
// was composed for.
func recordFor(target string, files []draftedFile) importRecord {
	rec := importRecord{Target: target, Files: make([]recordFile, 0, len(files))}
	for _, f := range files {
		rec.Files = append(rec.Files, recordEntryFor(f))
	}
	return rec
}

// recordEntryFor is the record's entry for one drafted file.
func recordEntryFor(f draftedFile) recordFile {
	if f.doc == nil {
		return recordFile{Path: f.path, Digest: digestOf(f.content)}
	}
	parts := make([]recordPart, 0, len(f.doc.units))
	for _, u := range f.doc.units {
		parts = append(parts, recordPart{Key: u.key, Digest: digestOf(u.content)})
	}
	return recordFile{Path: f.path, Parts: parts}
}

// recordBytes renders a record as the bytes written to disk: indented JSON with
// a closing newline.
func recordBytes(rec importRecord) string {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		// The record holds strings and slices only, which always marshal.
		panic(fmt.Sprintf("importops: marshal the import record: %v", err))
	}
	return string(data) + "\n"
}

// recordScaffold is the record as one more file an import writes, placed after
// the drafts it describes.
func recordScaffold(target string, files []draftedFile) abi.ScaffoldFile {
	return abi.ScaffoldFile{Path: RecordFileName, Content: recordBytes(recordFor(target, files))}
}

// parseRecord reads a record's bytes, refusing anything that is not a record an
// import writes: an unknown field, a file named twice or with neither or both of
// a digest and parts, a part with no key or named twice, a digest of another
// shape, or a second document after the first.
func parseRecord(data []byte) (importRecord, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var rec importRecord
	if err := dec.Decode(&rec); err != nil {
		return importRecord{}, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return importRecord{}, fmt.Errorf("it holds more than one JSON value")
	}
	paths := make(map[string]bool, len(rec.Files))
	for _, f := range rec.Files {
		if f.Path == "" {
			return importRecord{}, fmt.Errorf("it lists a file with no path")
		}
		if paths[f.Path] {
			return importRecord{}, fmt.Errorf("it lists the file %q more than once", f.Path)
		}
		paths[f.Path] = true
		if (f.Digest == "") == (len(f.Parts) == 0) {
			return importRecord{}, fmt.Errorf("its entry for %q carries neither or both of a digest and parts", f.Path)
		}
		if f.Digest != "" && !wellFormedDigest(f.Digest) {
			return importRecord{}, fmt.Errorf("its entry for %q carries the digest %q, which is not one an import writes",
				f.Path, f.Digest)
		}
		keys := make(map[string]bool, len(f.Parts))
		for _, p := range f.Parts {
			if len(p.Key) == 0 {
				return importRecord{}, fmt.Errorf("its entry for %q lists a part with no key", f.Path)
			}
			if !wellFormedDigest(p.Digest) {
				return importRecord{}, fmt.Errorf("its entry for %q carries the digest %q, which is not one an import "+
					"writes", f.Path, p.Digest)
			}
			k := joinKey(p.Key)
			if keys[k] {
				return importRecord{}, fmt.Errorf("its entry for %q lists the part %s more than once", f.Path,
					RenderKey(p.Key))
			}
			keys[k] = true
		}
	}
	return rec, nil
}

// digestPrefix opens every digest the record carries.
const digestPrefix = "sha256:"

// digestOf is the digest the record carries for a text.
func digestOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return digestPrefix + hex.EncodeToString(sum[:])
}

// wellFormedDigest reports whether d is a digest of the shape digestOf writes.
func wellFormedDigest(d string) bool {
	hexPart, ok := strings.CutPrefix(d, digestPrefix)
	if !ok || len(hexPart) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(hexPart)
	return err == nil && strings.ToLower(hexPart) == hexPart
}

// RenderKey writes the keys leading to a part of a drafted Aggregate or Release
// as a reader reads them: joined by full stops, with a key that holds a full
// stop, whitespace or a quotation mark, or is empty, written quoted, so two
// different parts are never written alike.
func RenderKey(key []string) string {
	segs := make([]string, 0, len(key))
	for _, k := range key {
		if k == "" || strings.ContainsAny(k, ". \t\n\r\"'") {
			segs = append(segs, strconv.Quote(k))
			continue
		}
		segs = append(segs, k)
	}
	return strings.Join(segs, ".")
}

// joinKey is a part's keys as one value a map can be keyed by. The separator is
// a character no YAML key the record lists is compared through.
func joinKey(key []string) string {
	return strings.Join(key, "\x00")
}

// isAggregateBytes reports whether data reads as a document whose top-level
// kind is Aggregate.
func isAggregateBytes(data []byte) bool {
	return draftKind(string(data)) == entitykind.Aggregate
}

// ownedByEntries reports whether data reads as a document the record lists entry
// by entry: one whose top-level kind is Aggregate or Release.
func ownedByEntries(data []byte) bool {
	kind := draftKind(string(data))
	return kind == entitykind.Aggregate || kind == entitykind.Release
}

// yamlDoc is a YAML document read as the entries a sync judges: its lines, as
// they are on disk, and every entry of its mappings, at every depth, with where
// each sits in those lines.
//
// The entries are read from the text rather than from a decoded value, because a
// sync changes the document by replacing lines: every line it does not replace
// stays exactly as it was, a person's blank lines, quoting and indentation
// included, which a decode and re-encode would not keep.
type yamlDoc struct {
	lines    []string // each line with its own line ending
	crlf     bool     // lines end in a carriage return and a line feed
	units    []*yamlUnit
	byKey    map[string]*yamlUnit
	children map[string][]*yamlUnit // the entries of the mapping at a key, the top level at ""
}

// yamlUnit is one entry of a block mapping in a yamlDoc.
//
// Its text runs from start — the first of the comment lines directly above its
// key at the key's own column, or the key line where there are none — to end,
// the last line of its value that is not a trailing comment. An interior entry,
// whose value is a block mapping holding entries, owns its head comment and key
// line only: what is beneath them belongs to its entries. content is the text
// the entry owns with the key's indentation removed, an interior one marked as a
// mapping, so a digest of it does not move when the entry is placed at another
// depth.
type yamlUnit struct {
	key      []string
	start    int
	keyLine  int
	end      int
	col      int
	interior bool
	content  string
}

// ownedLast is the last line the unit owns: its key line for an interior entry,
// the end of its value otherwise.
func (u *yamlUnit) ownedLast() int {
	if u.interior {
		return u.keyLine
	}
	return u.end
}

// indexYAML reads text as one YAML document whose top level is a block
// mapping, and indexes every entry of its block mappings. Anything else — text
// that does not parse, a second document, a top level that is not a block
// mapping, a key that is not a scalar, or a key named twice in one mapping — is
// refused, because a sync cannot say which of its parts an import wrote.
func indexYAML(text string) (*yamlDoc, error) {
	dec := yaml.NewDecoder(strings.NewReader(text))
	var root yaml.Node
	if err := dec.Decode(&root); err != nil {
		return nil, err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("it holds more than one YAML document")
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) != 1 {
		return nil, fmt.Errorf("it holds no YAML document")
	}
	top := root.Content[0]
	if top.Kind != yaml.MappingNode || top.Style&yaml.FlowStyle != 0 || len(top.Content) == 0 {
		return nil, fmt.Errorf("its top level is not a block mapping")
	}
	doc := &yamlDoc{
		lines:    splitLines(text),
		byKey:    map[string]*yamlUnit{},
		children: map[string][]*yamlUnit{},
	}
	doc.crlf = len(doc.lines) > 0 && strings.HasSuffix(doc.lines[0], "\r\n")
	if err := doc.walk(top, nil, -1); err != nil {
		return nil, err
	}
	return doc, nil
}

// walk indexes the entries of the block mapping m, whose keys lead from the top
// through prefix, and whose first entry's head comment begins after line bound.
func (d *yamlDoc) walk(m *yaml.Node, prefix []string, bound int) error {
	parent := joinKey(prefix)
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			return fmt.Errorf("line %d holds a key that is not a scalar", k.Line)
		}
		key := append(append(make([]string, 0, len(prefix)+1), prefix...), k.Value)
		joined := joinKey(key)
		if _, twice := d.byKey[joined]; twice {
			return fmt.Errorf("line %d names the key %s a second time", k.Line, RenderKey(key))
		}
		u := &yamlUnit{key: key, keyLine: k.Line - 1, col: k.Column - 1}
		u.start = u.keyLine
		for u.start-1 > bound && commentAt(d.lines[u.start-1], u.col) {
			u.start--
		}
		u.interior = v.Kind == yaml.MappingNode && v.Style&yaml.FlowStyle == 0 && len(v.Content) > 0
		u.end = d.valueEnd(u.keyLine, u.col, v)
		u.content = d.ownedText(u)
		d.units = append(d.units, u)
		d.byKey[joined] = u
		d.children[parent] = append(d.children[parent], u)
		if u.interior {
			if err := d.walk(v, key, u.keyLine); err != nil {
				return err
			}
		}
		bound = u.end
	}
	return nil
}

// valueEnd is the last line of the value of the entry whose key sits at column
// col of line keyLine.
//
// The value runs on while lines are blank, comments, or indented deeper than
// the key — or, for a block sequence written at the key's own column, items of
// it — and ends at the last of those lines that is not a trailing comment. A
// literal or folded scalar's lines are its content whatever they hold, so none
// of them is taken for a comment.
func (d *yamlDoc) valueEnd(keyLine, col int, v *yaml.Node) int {
	blockScalar := v.Kind == yaml.ScalarNode && v.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0
	compactSeq := v.Kind == yaml.SequenceNode && v.Style&yaml.FlowStyle == 0 && v.Column-1 == col
	end := keyLine
	for i := keyLine + 1; i < len(d.lines); i++ {
		text := lineText(d.lines[i])
		trimmed := strings.TrimLeft(text, " ")
		if strings.TrimSpace(text) == "" {
			continue
		}
		indent := len(text) - len(trimmed)
		comment := strings.HasPrefix(trimmed, "#")
		switch {
		case indent > col:
			if blockScalar || !comment {
				end = i
			}
		case compactSeq && indent == col && (trimmed == "-" || strings.HasPrefix(trimmed, "- ")):
			end = i
		case comment:
		default:
			return end
		}
	}
	return end
}

// ownedText is the text u owns, with the key's indentation removed.
func (d *yamlDoc) ownedText(u *yamlUnit) string {
	lines := make([]string, 0, u.ownedLast()-u.start+1)
	for i := u.start; i <= u.ownedLast(); i++ {
		lines = append(lines, dedent(lineText(d.lines[i]), u.col))
	}
	text := strings.Join(lines, "\n")
	if u.interior {
		text += "\n\x00mapping"
	}
	return text
}

// ownedLines reports, for every line of the document, whether some entry owns
// it.
func (d *yamlDoc) ownedLines() []bool {
	owned := make([]bool, len(d.lines))
	for _, u := range d.units {
		for i := u.start; i <= u.ownedLast(); i++ {
			owned[i] = true
		}
	}
	return owned
}

// splitLines splits text into lines, each keeping its own line ending; a last
// line with no ending is kept as it is.
func splitLines(text string) []string {
	var lines []string
	for text != "" {
		i := strings.IndexByte(text, '\n')
		if i < 0 {
			lines = append(lines, text)
			break
		}
		lines = append(lines, text[:i+1])
		text = text[i+1:]
	}
	return lines
}

// lineText is a line without its line ending.
func lineText(line string) string {
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
}

// commentAt reports whether line is a comment written at column col.
func commentAt(line string, col int) bool {
	text := lineText(line)
	return len(text) > col && strings.TrimLeft(text[:col], " ") == "" && text[col] == '#'
}

// dedent removes up to n leading spaces from text.
func dedent(text string, n int) string {
	for i := 0; i < n && strings.HasPrefix(text, " "); i++ {
		text = text[1:]
	}
	return text
}
