// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/importersocket"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parsersocket"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// UndetectedFormatError reports a source no importer in the catalog recognises.
// It carries the occurrence of [ErrUnrecognisedImportFormat], so a surface reading
// the condition's codes off the chain reports that condition.
//
// It is a distinct type rather than the occurrence alone because the surfaces
// that meet this condition correct it differently: a reader at a terminal passes
// a flag, and a caller on a protocol sends a field. Its message is the terminal's
// remedy; a protocol surface recognises the condition through this type and
// words its own refusal around the field a request can actually carry.
//
// Matching on the type rather than on the message text is what keeps the two in
// step: a surface that compared the sentence would keep compiling and silently
// stop matching the first time either wording moved.
type UndetectedFormatError struct {
	// Source is the source path as it was received.
	Source string
	// Advertised is every format the importers in the catalog advertise, in the
	// order the recognition read them — the formats a caller may name instead.
	Advertised []string
}

// Error renders the condition's registered sentence, the source, and the
// terminal's remedy: the flag that names a format explicitly, and the formats the
// catalog's importers advertise.
func (e *UndetectedFormatError) Error() string { return e.occurrence().Error() }

// Unwrap returns the occurrence of the registered condition, so errors.Is
// against [ErrUnrecognisedImportFormat] and the code reads both reach it.
func (e *UndetectedFormatError) Unwrap() error { return e.occurrence() }

// occurrence builds the raised condition this refusal carries. It is built from
// the fields rather than held, so the value a caller constructs and the value
// the recognition returns render the same text.
func (e *UndetectedFormatError) occurrence() *cerrors.CompilerError {
	return cerrors.Raise(errUnrecognisedImportFormat, "", cerrors.SourceLocation{},
		cerrors.ComposeMessage(errUnrecognisedImportFormat,
			[]string{"source " + strconv.Quote(e.Source)},
			"Pass --format to name it explicitly ("+formatExamples(e.Advertised, "--format ")+")"),
		nil)
}

// Path returns the source path the detection could not classify, so a surface
// re-wording this refusal names the same file without parsing the message.
func (e *UndetectedFormatError) Path() string { return e.Source }

// Formats returns the formats the catalog's importers advertise, so a surface
// re-wording this refusal names the formats a caller may name without parsing
// the message.
func (e *UndetectedFormatError) Formats() []string { return e.Advertised }

// AmbiguousFormatError reports a source the signatures of more than one format
// recognise. It carries the occurrence of [ErrAmbiguousImportFormat], so a
// surface reading the short code off the chain reports that condition, and it
// is a distinct type for the reason [UndetectedFormatError] is: a protocol
// surface re-words the terminal's remedy around the field it can send.
type AmbiguousFormatError struct {
	// Source is the source path as it was received.
	Source string
	// Matched is every format a matching signature recognises the source as, in
	// the order the recognition read them.
	Matched []string
}

// Error renders the condition's registered sentence, the source and the formats
// it was recognised as, and the terminal's remedy.
func (e *AmbiguousFormatError) Error() string { return e.occurrence().Error() }

// Unwrap returns the occurrence of the registered condition, so errors.Is
// against [ErrAmbiguousImportFormat] and the short-code read both reach it.
func (e *AmbiguousFormatError) Unwrap() error { return e.occurrence() }

// Path returns the source path more than one format was recognised in.
func (e *AmbiguousFormatError) Path() string { return e.Source }

// Formats returns the formats the source was recognised as.
func (e *AmbiguousFormatError) Formats() []string { return e.Matched }

// occurrence builds the raised condition this refusal carries. It is built from
// the fields rather than held, so the value a caller constructs and the value
// the recognition returns render the same text.
func (e *AmbiguousFormatError) occurrence() *cerrors.CompilerError {
	quoted := make([]string, 0, len(e.Matched))
	for _, f := range e.Matched {
		quoted = append(quoted, strconv.Quote(f))
	}
	return cerrors.Raise(errAmbiguousImportFormat, "", cerrors.SourceLocation{},
		cerrors.ComposeMessage(errAmbiguousImportFormat,
			[]string{"source " + strconv.Quote(e.Source), "recognised as " + strings.Join(quoted, ", ")},
			"Pass --format to name the one to read it as ("+formatExamples(e.Matched, "--format ")+")"),
		nil)
}

// formatExamples lists formats as examples of what to name, each behind prefix,
// joined into one phrase — "for example --format a or --format b". With no
// format to offer it says so, because a remedy naming an empty list would send
// the reader looking for something that is not there.
func formatExamples(formats []string, prefix string) string {
	if len(formats) == 0 {
		return "no importer plugin in the workspace's catalog advertises a format to name"
	}
	named := make([]string, 0, len(formats))
	for _, f := range formats {
		named = append(named, prefix+f)
	}
	if len(named) == 1 {
		return "for example " + named[0]
	}
	return "for example " + strings.Join(named[:len(named)-1], ", ") + " or " + named[len(named)-1]
}

// SelectFormat resolves the source format for one import: an explicit format the
// caller named wins outright; otherwise the format is recognised from the
// signatures the importers in the import's catalog view declare.
//
// It is the only implementation of that precedence, and every surface reaches it
// through [Import] — a surface that wants to name a format supplies one, and a
// surface that relies on recognition supplies the empty string. Giving the
// naming surface its own path would have been a second implementation of the
// same two-line rule, and the two would then have disagreed about which of an
// explicit format and a recognised one wins.
//
// It mirrors the extension-versus-explicit precedence the parser socket uses,
// with the importers' declared signatures standing in for the extension
// fallback because the supported source formats share extensions.
//
// src is the source as [Import] found it, a file or a directory, and the
// precedence is the same for both: a named format opens nothing here.
func SelectFormat(ctx context.Context, explicit string, src *Source, seam Seam) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	return detectFormat(ctx, src, seam)
}

// detectFormat recognises a source's format from the signatures the importers
// of the seam's catalog view record, and from nothing else.
//
// The host knows no format. Each importer's signatures name the syntax the
// source is read in, the file names they apply to and the paths the parsed tree
// must or must not carry; a file is parsed through the parser socket — the
// parser the import resolves for that syntax, the catalog's or, where the catalog
// serves none, the one the binary carries, never a grammar in the host — and the
// predicates are evaluated over the tree it returns. An importer one of
// whose signatures matches recognises the source as every format it advertises.
//
// A directory is recognised exactly as a file is, over the files it holds: a
// signature's patterns select the files it is tried against, and the first of
// them, in name order, that parses in its syntax and satisfies every predicate
// matches it. A signature declaring no file pattern applies to no file of a
// directory. Before any file of a directory is opened, every link among its files
// that a pattern of any importer selects, and every link to a directory, is held
// to the directory, so a link leading out refuses with nothing read.
//
// It is deterministic. The importers are read in the order the catalog view
// lists them, their signatures in declared order and a directory's files in name
// order, a file is read at most once and parsed at most once per syntax however
// many signatures name it, and the formats recognised are collected in first-seen
// order without repeats — so two versions of one importer, or two importers of
// one format, recognise one format. Exactly one format recognised selects it.
// None is an [*UndetectedFormatError] naming every format the importers
// advertise; more than one is an [*AmbiguousFormatError] naming the formats
// recognised. Neither guesses.
//
// A file that does not parse in a syntax — its parser reports a halting
// diagnostic — matches no signature naming that syntax. A syntax that cannot be
// parsed at all — neither the catalog nor the binary serves a parser for it, or
// its parser cannot be run — is returned as an error rather than passed over:
// the signatures naming it went unevaluated, and choosing among the rest would
// be a guess about them. A file a pattern selects that cannot be read is
// returned as the [*SourceReadError] it is.
//
// It makes no network access and reads no file but the source it was handed and,
// of a directory, the files a signature's patterns select; the parser runs
// hermetically.
func detectFormat(ctx context.Context, src *Source, seam Seam) (string, error) {
	if src.directory {
		if err := src.judgeLinks(func(name string) bool { return anySelects(seam.Importers, name) }); err != nil {
			return "", err
		}
	}
	parsed := map[parseKey]parsedSource{}
	var advertised, recognised []string
	for _, importer := range seam.Importers {
		formats := catalog.ImporterFormats(importer)
		advertised = appendUnseen(advertised, formats...)
		matched, err := importerRecognises(ctx, importer, src, seam, parsed)
		if err != nil {
			if readErr := (*SourceReadError)(nil); errors.As(err, &readErr) {
				return "", err
			}
			return "", fmt.Errorf("recognise the source format of %q: %w", src.name, err)
		}
		if matched {
			recognised = appendUnseen(recognised, formats...)
		}
	}
	switch len(recognised) {
	case 1:
		return recognised[0], nil
	case 0:
		return "", &UndetectedFormatError{Source: src.name, Advertised: advertised}
	default:
		return "", &AmbiguousFormatError{Source: src.name, Matched: recognised}
	}
}

// appendUnseen appends each value not already in list, in order.
func appendUnseen(list []string, values ...string) []string {
	for _, v := range values {
		if !slices.Contains(list, v) {
			list = append(list, v)
		}
	}
	return list
}

// parsedSource is a file read in one syntax: whether it parsed, and the tree it
// parsed to.
type parsedSource struct {
	ok   bool
	tree any
}

// parseKey names one parse: the file, by its name in the source, and the syntax
// it was read in.
type parseKey struct {
	name, syntax string
}

// importerRecognises reports whether any of an importer's recorded signatures
// matches the source. The signatures are read off the importer's entity, which
// the load holds equal to what the importer's own describe() reports.
func importerRecognises(ctx context.Context, importer types.Plugin, src *Source, seam Seam,
	parsed map[parseKey]parsedSource) (bool, error) {
	if importer.Spec.Importer == nil {
		return false, nil
	}
	for _, sig := range importer.Spec.Importer.Detect {
		for _, f := range src.entries {
			if f.dir || !src.applies(sig.Files, f.name) {
				continue
			}
			content, err := f.bytes()
			if err != nil {
				return false, err
			}
			read, err := parseOnce(ctx, sig.Syntax, f.name, content, seam, parsed)
			if err != nil {
				return false, fmt.Errorf("importer %q declares a signature read in %s: %w", importer.Coordinate(), sig.Syntax, err)
			}
			if read.ok && allHold(read.tree, sig.Match) {
				return true, nil
			}
		}
	}
	return false, nil
}

// parseOnce returns the file named name read in syntax, parsing it through the
// parser socket the first time that file is asked for in that syntax and
// answering every later request for the pair from the first.
func parseOnce(ctx context.Context, syntax, name string, content []byte, seam Seam,
	parsed map[parseKey]parsedSource) (parsedSource, error) {
	key := parseKey{name: name, syntax: syntax}
	if src, done := parsed[key]; done {
		return src, nil
	}
	load := seam.parserLoader(ctx)
	if seam.ParserRes == nil || load == nil {
		return parsedSource{}, fmt.Errorf("no parser resolution is wired for the %s syntax", syntax)
	}
	out, err := parsersocket.ParseArtifact(ctx, seam.ParserRes, load, syntax, content, nil)
	if err != nil {
		return parsedSource{}, err
	}
	src := parsedSource{}
	if importersocket.ParseRefusal(out) == nil {
		dec := json.NewDecoder(bytes.NewReader(out.Tree))
		dec.UseNumber()
		var tree any
		if err := dec.Decode(&tree); err != nil {
			return parsedSource{}, fmt.Errorf("the %s parser returned a tree that is not a JSON document: %w", syntax, err)
		}
		src = parsedSource{ok: true, tree: tree}
	}
	parsed[key] = src
	return src, nil
}

// filesMatch reports whether a signature's file patterns admit the source name:
// any name where it declares none, and otherwise a name whose trailing segments
// match one pattern segment for segment. name is slash-separated.
//
// A pattern that is not well formed matches nothing; the describe() gate refuses
// one at load, so it reaches here only from an entity no plugin vouches for.
//
// It is the one matcher of a name against declared file patterns: recognition
// asks it per signature, and the selection of a directory's files asks it through
// [selectsFile], so which files an importer's patterns name has one answer.
func filesMatch(patterns []string, name string) bool {
	if len(patterns) == 0 {
		return true
	}
	segments := strings.Split(name, "/")
	for _, pattern := range patterns {
		want := strings.Count(pattern, "/") + 1
		if want > len(segments) {
			continue
		}
		tail := strings.Join(segments[len(segments)-want:], "/")
		if ok, err := path.Match(pattern, tail); err == nil && ok {
			return true
		}
	}
	return false
}

// declaresFilePatterns reports whether any of an importer's recorded signatures
// declares a file pattern — whether it says which files of a directory it reads.
func declaresFilePatterns(importer types.Plugin) bool {
	return len(filePatternsOf(importer)) > 0
}

// selectsFile reports whether one of the file patterns an importer's recorded
// signatures declare matches name, a slash-separated path inside a directory. A
// signature declaring no pattern selects no file of a directory, so an importer
// declaring none selects nothing.
func selectsFile(importer types.Plugin, name string) bool {
	if importer.Spec.Importer == nil {
		return false
	}
	for _, sig := range importer.Spec.Importer.Detect {
		if len(sig.Files) > 0 && filesMatch(sig.Files, name) {
			return true
		}
	}
	return false
}

// anySelects reports whether a file pattern of any of importers selects name.
func anySelects(importers []types.Plugin, name string) bool {
	for _, importer := range importers {
		if selectsFile(importer, name) {
			return true
		}
	}
	return false
}

// filePatternsOf returns every file pattern an importer's recorded signatures
// declare, in declared order without repeats.
func filePatternsOf(importer types.Plugin) []string {
	if importer.Spec.Importer == nil {
		return nil
	}
	var patterns []string
	for _, sig := range importer.Spec.Importer.Detect {
		patterns = appendUnseen(patterns, sig.Files...)
	}
	return patterns
}

// allHold reports whether every predicate holds over tree.
func allHold(tree any, predicates []types.PluginPathMatch) bool {
	for _, m := range predicates {
		value, reached := lookupPath(tree, m.Path)
		if !predicateHolds(m, value, reached) {
			return false
		}
	}
	return true
}

// lookupPath follows a dotted path from the top of tree: a segment of decimal
// digits selects that position of an array, and any segment selects a member of
// an object. It reports the value reached and whether the path reached one.
func lookupPath(tree any, dotted string) (any, bool) {
	current := tree
	for _, segment := range strings.Split(dotted, ".") {
		switch node := current.(type) {
		case map[string]any:
			next, ok := node[segment]
			if !ok {
				return nil, false
			}
			current = next
		case []any:
			index, ok := arrayIndex(segment)
			if !ok || index >= len(node) {
				return nil, false
			}
			current = node[index]
		default:
			return nil, false
		}
	}
	return current, true
}

// arrayIndex reads a segment written as a whole number, 0 or greater.
func arrayIndex(segment string) (int, bool) {
	if segment == "" {
		return 0, false
	}
	for _, r := range segment {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	index, err := strconv.Atoi(segment)
	return index, err == nil
}

// predicateHolds reports whether one predicate holds for the value a path
// reached: the kind first, then the string comparison, which only a string
// value satisfies.
func predicateHolds(m types.PluginPathMatch, value any, reached bool) bool {
	if !kindHolds(m.Is, value, reached) {
		return false
	}
	if m.Equals == "" && m.Prefix == "" {
		return true
	}
	text, isString := value.(string)
	if !reached || !isString {
		return false
	}
	if m.Equals != "" && text != m.Equals {
		return false
	}
	return m.Prefix == "" || strings.HasPrefix(text, m.Prefix)
}

// kindHolds reports whether the value a path reached is of the kind is names. A
// kind outside the declared set holds for nothing.
func kindHolds(is string, value any, reached bool) bool {
	switch is {
	case types.IsPresent:
		return reached
	case types.IsAbsent:
		return !reached
	}
	if !reached {
		return false
	}
	switch is {
	case types.IsObject:
		_, ok := value.(map[string]any)
		return ok
	case types.IsArray:
		_, ok := value.([]any)
		return ok
	case types.IsString:
		_, ok := value.(string)
		return ok
	case types.IsNumber:
		_, ok := value.(json.Number)
		return ok
	case types.IsBoolean:
		_, ok := value.(bool)
		return ok
	}
	return false
}
