// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredshape"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"go.yaml.in/yaml/v3"
)

// aggregaAPIGroupPrefix is what the value of an Aggrega document's apiVersion
// begins with, whatever version follows it.
const aggregaAPIGroupPrefix = "aggrega.ai/"

// documentHeader is the envelope a whole-catalog check decodes a document into
// when its caller names no header of its own: the fields every Aggrega
// document's identity is read from.
type documentHeader struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
	} `yaml:"metadata"`
}

// UnloadableDocument is the one answer to whether a file a whole-catalog check
// read is an Aggrega document the check cannot load. data and readErr are what
// reading the file at path returned. It returns nil when the file loads, or when
// it is not an Aggrega document at all; the [ErrUnknownCatalogDocumentKind]
// occurrence naming the file and the kind when it loads and declares a kind a
// catalog does not hold; and otherwise the [resolver.ErrYAMLLoad] occurrence
// naming the file and the reason.
//
// A file is an Aggrega document when its text declares the envelope: a line at
// column 0 reading `apiVersion:` whose value, quoted or not, begins
// `aggrega.ai/`. The declaration is read from the text rather than from a parse,
// because a document that does not parse is exactly the one whose parse cannot
// say what it is. A catalog directory legitimately holds YAML that is not an
// Aggrega document — a Helm chart's templates among it, which are Go template
// source and commonly do not parse as YAML until rendered — and each such file
// declares some other apiVersion or none, so it is passed over here. An
// apiVersion line indented inside a block scalar is not at column 0 and declares
// nothing.
//
// Such a document does not load when it does not parse as YAML, or does not
// decode into header — the caller's own header, so a check that reads more of the
// envelope than its identity cannot pass in silence over a document its own
// decode refuses. A nil header decodes into the envelope's identity fields alone.
//
// A document that loads and declares a kind no catalog holds is answered too,
// because every load passes over it and the check would otherwise report a clean
// catalog over a document no catalog read. A Manifest, a Release or a Workload is
// not such a document: a catalog directory may hold one beside the entities — a
// Workload in the directory of the Recipe that imports it — and every load
// passes over it by design.
//
// A file that could not be read at all is answered as not loading whatever it
// holds: nothing was read, so nothing shows it is not an Aggrega document, and a
// check answering for every document under its directories does not pass over
// one it could not read.
func UnloadableDocument(path string, data []byte, readErr error, header any) error {
	if readErr != nil {
		return unloadableDocumentError(path, "failed to read file: "+readErr.Error())
	}
	if !declaresAggregaEnvelope(data) {
		return nil
	}
	if header == nil {
		header = &documentHeader{}
	}
	if err := yaml.Unmarshal(data, header); err != nil {
		return unloadableDocumentError(path, unloadableDocumentReason(err, data))
	}
	return unheldKindDocument(path, data, header)
}

// unloadableDocumentReason says why a document that declares the envelope did
// not load: the parser's own message where its YAML does not parse, and where it
// parses but does not decode into the header, each line the decoder gave up on,
// the field written there, the shape it was written in and the shape the
// envelope takes at that field — in the author's words rather than the decoder's,
// which end with the Go type it was decoding into.
func unloadableDocumentReason(err error, data []byte) string {
	var typeErr *yaml.TypeError
	if !errors.As(err, &typeErr) {
		return "failed to unmarshal YAML: " + err.Error()
	}
	return "failed to decode the envelope: " + authoredshape.DescribeYAMLDecodeErrorIn(typeErr, data).Error()
}

// UnloadableDocumentsHalt is the halt a whole-catalog check raises after its
// walk when found — the occurrences [UnloadableDocument] returned over that walk
// — is not empty, naming every such document beneath one head, ordered by path.
// It returns nil for an empty found.
//
// The head takes no identity of its own: each finding beneath it is the
// [resolver.ErrYAMLLoad] or [ErrUnknownCatalogDocumentKind] occurrence naming its
// file, so the conditions the halt reports are the ones its findings name. The head counts them
// and names their files as well, because a surface printing only the head's own
// line — a one-line answer to a tool call — would otherwise say how many
// documents failed and not which.
func UnloadableDocumentsHalt(found []error) error {
	if len(found) == 0 {
		return nil
	}
	sorted := append([]error(nil), found...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return unloadableDocumentPath(sorted[i]) < unloadableDocumentPath(sorted[j])
	})
	paths := make([]string, 0, len(sorted))
	for _, f := range sorted {
		paths = append(paths, unloadableDocumentPath(f))
	}
	counted := "1 document under the catalog directories declares"
	if len(sorted) != 1 {
		counted = fmt.Sprintf("%d documents under the catalog directories declare", len(sorted))
	}
	return cerrors.Wrap(
		errors.Join(sorted...),
		cerrors.CatalogError,
		cerrors.SourceLocation{},
		fmt.Sprintf("%s an Aggrega apiVersion and could not be loaded, so nothing was checked: %s", counted, strings.Join(paths, ", ")),
	)
}

// unloadableDocumentError is the occurrence naming one document that did not
// load, with reason saying whether the read or the parse failed and why.
func unloadableDocumentError(path, reason string) error {
	return cerrors.Wrap(
		resolver.ErrYAMLLoad(),
		cerrors.CatalogError,
		cerrors.SourceLocation{File: path},
		cerrors.ComposeMessage(resolver.ErrYAMLLoad(), []string{"catalog document", reason}, ""),
	)
}

// unloadableDocumentPath is the file an occurrence names, or "" for an error
// neither [unloadableDocumentError] nor [unheldKindError] produced.
func unloadableDocumentPath(err error) string {
	var ce *cerrors.CompilerError
	if errors.As(err, &ce) {
		return ce.Source().File
	}
	return ""
}

// declaresAggregaEnvelope reports whether data holds a line at column 0 reading
// `apiVersion:` whose value, after optional whitespace and one optional opening
// quote, begins [aggregaAPIGroupPrefix]. A leading byte-order mark is not part of
// the first line's column count.
func declaresAggregaEnvelope(data []byte) bool {
	rest := bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	for len(rest) > 0 {
		line := rest
		if i := bytes.IndexByte(rest, '\n'); i >= 0 {
			line, rest = rest[:i], rest[i+1:]
		} else {
			rest = nil
		}
		value, ok := bytes.CutPrefix(line, []byte("apiVersion:"))
		if !ok {
			continue
		}
		value = bytes.TrimSpace(value)
		if len(value) > 0 && (value[0] == '"' || value[0] == '\'') {
			value = value[1:]
		}
		if bytes.HasPrefix(value, []byte(aggregaAPIGroupPrefix)) {
			return true
		}
	}
	return false
}
