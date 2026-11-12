// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package oci

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegment"
)

// ErrCacheLocationOutsideCache is the halt raised when a value a cache location
// is built from would place what is cached somewhere other than the place that
// value names in the artifact cache.
//
// A cache location is composed from values: an entity's reference and version,
// a source's kind, URL, chart, module subdirectory, version and digest. Each of
// them names one position in the layout, and most of them arrive from somewhere
// this program does not write — a document an author wrote, a body a registry
// served, a record read back from disk. A value that carries a path separator, or
// names the current or the parent directory, would move everything written for it
// to another position: beside another entity's slot, or out of the cache
// altogether. The path builders refuse such a value before anything is written or
// read there, naming the value and what it was given for.
//
// Category: none is fixed here, because the value's origin decides which concern
// it belongs to, and the builder does not know it. A caller that holds the
// document the value was written in wraps the refusal with it. Match with
// errors.Is(err, ErrCacheLocationOutsideCache()).
func ErrCacheLocationOutsideCache() error { return errCacheLocationOutsideCache }

var errCacheLocationOutsideCache = cerrors.Register("cache-location-outside-cache", cerrors.Meta{
	ShortCode: "E0307",
	Message:   "a value a cache location is built from would place cached files outside the place it names in the cache",
	RulePlace: cerrors.RuleNoStageReaches("The rule is evaluated where a cache location is composed from the values " +
		"that address it: a pull writing what it fetched, a dependency resolution reading a cached entity, and the " +
		"read that hands a build a cached source. None of those moments is a declared stage of a compilation, and no " +
		"declared stage holds the cache root the location is judged against."),
})

// cacheLocationAdvice is what the refusal tells its reader. It states the rule in
// terms of the values a reader wrote or was served, which is the only thing they
// can change.
const cacheLocationAdvice = "a version, a name, a kind or a host names one directory of the cache, so it carries " +
	"no `/` or `\\` and is neither `.` nor `..`; a URL path or a module subdirectory may name directories beneath " +
	"its place but may not climb above it. Correct the value where it was written, or, where a registry served it, " +
	"ask its publisher to publish a version that carries a well-formed one"

// CacheSegment is one value a cache location is composed from, in the forms the
// composition and its refusal need.
type CacheSegment struct {
	// Key names the value for a reader: the key it was written under, such as
	// "version", "url" or "module_subdir", or what it is, such as "reference".
	Key string
	// Value is the value as it was written or served. The refusal quotes it,
	// because that is the spelling a reader can find in what they wrote.
	Value string
	// Segment is the part of Value this position names, as written: Value
	// itself, or the part of it the position takes, such as a URL's host or its
	// path. It is handed over unescaped — [JoinCacheLocation] spells it as a file
	// or directory name — and an empty Segment contributes no directory.
	Segment string
	// Nested marks a value that names a slash-separated directory path of its
	// own, such as a URL path or a module subdirectory. Such a value may descend
	// beneath its place but may not climb above it, and contributes one directory
	// for each of its slash-separated parts. A value that is neither nested nor
	// whole names exactly one directory.
	Nested bool
	// Whole marks a value that is one identity although it carries separators,
	// such as an entity's reference: it names exactly one directory, its
	// separators spelled like every other character a name cannot carry.
	Whole bool
}

// JoinCacheLocation joins segments beneath root, in order, and returns the
// location, or the refusal naming the first segment that would not stay in the
// place it names.
//
// It is the one statement of what keeps a cache location inside the cache, and
// every path builder composing one calls it: the entity slot ([CachePath]) and
// the source-artifact slot the source resolver composes. The rule is per value
// rather than per result: a value confined to its own place cannot climb out of
// the cache, nor above its own position into the places the values before it
// name, which a check over the joined result alone would admit.
//
// The verdict is the same on every platform. A backslash is a path separator on
// one of the platforms this program runs on, so it is read as one everywhere;
// judging it by the separators of the platform at hand would let a value pass on
// one machine and be refused on another.
//
// It is also the one place a cache location's directory names are spelled: each
// segment it admits is written through [pathsegment.Encode], a nested one part by
// part, so every directory it names is one Windows, Linux and macOS all accept,
// two different values never share one even where a file system ignores letter
// case, and a caller cannot join a value under a spelling of its own.
//
// subject says what the location is for, such as `entity "demo/app/web"`; the
// refusal carries it beside the value.
func JoinCacheLocation(root, subject string, segments ...CacheSegment) (string, error) {
	parts := make([]string, 0, len(segments)+1)
	parts = append(parts, root)
	for _, s := range segments {
		if s.Segment == "" {
			continue
		}
		if !staysInItsPlace(s) {
			return "", cacheLocationRefusal(subject, s)
		}
		parts = append(parts, spelled(s)...)
	}
	return filepath.Join(parts...), nil
}

// staysInItsPlace reports whether the segment names only the place its position
// gives it.
func staysInItsPlace(s CacheSegment) bool {
	switch {
	case s.Nested:
		// Slash arithmetic over a value whose backslashes are read as separators
		// too, so the containment predicate decides and the answer does not depend
		// on the platform.
		return pathscope.IsLocalSlash(strings.ReplaceAll(s.Segment, `\`, "/"))
	case s.Whole:
		return s.Segment != "." && s.Segment != ".."
	default:
		return s.Segment != "." && s.Segment != ".." && !strings.ContainsAny(s.Segment, `/\`)
	}
}

// spelled returns the directory names an admitted segment contributes: one for a
// value naming one directory or a whole identity, and one for each part of a
// nested path once the path is cleaned, so a part naming the current directory, or
// a parent step the path climbs back out of, contributes nothing.
func spelled(s CacheSegment) []string {
	if !s.Nested {
		return []string{pathsegment.Encode(s.Segment)}
	}
	cleaned := path.Clean(s.Segment)
	if cleaned == "." {
		return nil
	}
	parts := strings.Split(cleaned, "/")
	for i, part := range parts {
		parts[i] = pathsegment.Encode(part)
	}
	return parts
}

// cacheLocationRefusal composes the refusal: the condition's sentence, the value
// as it was written with the key it was written under, what the location was
// for, and the advice.
func cacheLocationRefusal(subject string, s CacheSegment) error {
	values := []string{fmt.Sprintf("%s %q", s.Key, s.Value)}
	if subject != "" {
		values = append(values, "for "+subject)
	}
	return &cacheLocationError{
		message: cerrors.ComposeMessage(ErrCacheLocationOutsideCache(), values, cacheLocationAdvice),
	}
}

// cacheLocationError is the refusal JoinCacheLocation returns. It renders the
// composed message and unwraps to the condition, so the condition's identifiers
// are read off it however many frames a caller wraps around it. It is not an
// envelope: a caller wrapping it names the document the value came from, and a
// plain error keeps that caller's text in what a reader is shown.
type cacheLocationError struct {
	message string
}

func (e *cacheLocationError) Error() string { return e.message }

func (e *cacheLocationError) Unwrap() error { return errCacheLocationOutsideCache }
