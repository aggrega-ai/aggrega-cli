// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parsersocket"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// UndetectedFormatError reports a source no importer in the catalog recognises.
//
// It is a distinct type rather than a plain error because the surfaces that
// meet this condition correct it differently: a reader at a terminal passes a
// flag, and a caller on a protocol sends a field. Its message is the terminal's
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

// Error renders the terminal's remedy for a source whose format was not
// recognised: the file, the flag that names a format explicitly, and the
// formats the catalog's importers advertise.
func (e *UndetectedFormatError) Error() string {
	return fmt.Sprintf("cannot detect the source format of %q; pass --format to name it explicitly (%s)",
		e.Source, formatExamples(e.Advertised, "--format "))
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
func SelectFormat(ctx context.Context, explicit, source string, content []byte, seam Seam) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	return detectFormat(ctx, source, content, seam)
}

// detectFormat recognises a source's format from the signatures the importers
// of the seam's catalog view record, and from nothing else.
//
// The host knows no format. Each importer's signatures name the syntax the
// source is read in, the file names they apply to and the paths the parsed tree
// must or must not carry; the source is parsed through the parser socket — the
// parser the catalog serves for that syntax, never a grammar of the host's own —
// and the predicates are evaluated over the tree it returns. An importer one of
// whose signatures matches recognises the source as every format it advertises.
//
// It is deterministic. The importers are read in the order the catalog view
// lists them, their signatures in declared order, the source is parsed at most
// once per syntax however many signatures name it, and the formats recognised
// are collected in first-seen order without repeats — so two versions of one
// importer, or two importers of one format, recognise one format. Exactly one
// format recognised selects it. None is an [*UndetectedFormatError] naming every
// format the importers advertise; more than one is an [*AmbiguousFormatError]
// naming the formats recognised. Neither guesses.
//
// A source that does not parse in a syntax — its parser reports a halting
// diagnostic — matches no signature naming that syntax. A syntax that cannot be
// parsed at all — no parser advertises it, or its parser cannot be run — is
// returned as an error rather than passed over: the signatures naming it went
// unevaluated, and choosing among the rest would be a guess about them.
//
// It makes no network access and reads no file but the source it was handed;
// the parser runs hermetically.
func detectFormat(ctx context.Context, source string, content []byte, seam Seam) (string, error) {
	name := filepath.ToSlash(source)
	parsed := map[string]parsedSource{}
	var advertised, recognised []string
	for _, importer := range seam.Importers {
		formats := catalog.ImporterFormats(importer)
		advertised = appendUnseen(advertised, formats...)
		matched, err := importerRecognises(ctx, importer, name, content, seam, parsed)
		if err != nil {
			return "", fmt.Errorf("recognise the source format of %q: %w", source, err)
		}
		if matched {
			recognised = appendUnseen(recognised, formats...)
		}
	}
	switch len(recognised) {
	case 1:
		return recognised[0], nil
	case 0:
		return "", &UndetectedFormatError{Source: source, Advertised: advertised}
	default:
		return "", &AmbiguousFormatError{Source: source, Matched: recognised}
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

// parsedSource is the source read in one syntax: whether it parsed, and the
// tree it parsed to.
type parsedSource struct {
	ok   bool
	tree any
}

// importerRecognises reports whether any of an importer's recorded signatures
// matches the source. The signatures are read off the importer's entity, which
// the load holds equal to what the importer's own describe() reports.
func importerRecognises(ctx context.Context, importer types.Plugin, name string, content []byte, seam Seam,
	parsed map[string]parsedSource) (bool, error) {
	if importer.Spec.Importer == nil {
		return false, nil
	}
	for _, sig := range importer.Spec.Importer.Detect {
		if !filesMatch(sig.Files, name) {
			continue
		}
		src, err := parseOnce(ctx, sig.Syntax, content, seam, parsed)
		if err != nil {
			return false, fmt.Errorf("importer %q declares a signature read in %s: %w", importer.Coordinate(), sig.Syntax, err)
		}
		if !src.ok {
			continue
		}
		if allHold(src.tree, sig.Match) {
			return true, nil
		}
	}
	return false, nil
}

// parseOnce returns the source read in syntax, parsing it through the parser
// socket the first time a syntax is asked for and answering every later request
// for that syntax from the first.
func parseOnce(ctx context.Context, syntax string, content []byte, seam Seam,
	parsed map[string]parsedSource) (parsedSource, error) {
	if src, done := parsed[syntax]; done {
		return src, nil
	}
	if seam.ParserRes == nil || seam.ParserLoad == nil {
		return parsedSource{}, fmt.Errorf("no parser resolution is wired for the %s syntax", syntax)
	}
	out, err := parsersocket.ParseArtifact(ctx, seam.ParserRes, seam.ParserLoad, syntax, content, nil)
	if err != nil {
		return parsedSource{}, err
	}
	src := parsedSource{}
	if !haltedParse(out) {
		dec := json.NewDecoder(bytes.NewReader(out.Tree))
		dec.UseNumber()
		var tree any
		if err := dec.Decode(&tree); err != nil {
			return parsedSource{}, fmt.Errorf("the %s parser returned a tree that is not a JSON document: %w", syntax, err)
		}
		src = parsedSource{ok: true, tree: tree}
	}
	parsed[syntax] = src
	return src, nil
}

// haltedParse reports whether a parse reported a halting diagnostic — the
// source is not in the syntax it was read as.
func haltedParse(out abi.ParseOutput) bool {
	for _, d := range out.Diagnostics {
		if d.Severity == abi.SeverityHalt {
			return true
		}
	}
	return false
}

// filesMatch reports whether a signature's file patterns admit the source name:
// any name where it declares none, and otherwise a name whose trailing segments
// match one pattern segment for segment. name is slash-separated.
//
// A pattern that is not well formed matches nothing; the describe() gate refuses
// one at load, so it reaches here only from an entity no plugin vouches for.
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
