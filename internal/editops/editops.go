// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package editops applies one bounded, structured edit to an authored source
// YAML document, preserving every byte the edit does not touch.
//
// # What it is for
//
// A caller holding a visual or structured view of a project — a canvas, an
// assistant, a form — needs to record a change the author made in that view back
// into the document the author owns. The document stays the single source of
// truth: the edit goes to the source, and whatever view the caller keeps is
// re-derived from it afterwards. Nothing here maintains a second model of the
// project alongside the document, and nothing here edits a compiled graph.
//
// # The bound, which is the whole design
//
// This is not a file writer. One call adds one entry under one named container,
// or sets one scalar, and it can do nothing else: it cannot delete an entry,
// cannot replace a block an author wrote, cannot reorder anything, and cannot
// write bytes a caller supplies verbatim. [Kind] enumerates the four edits, and
// the enumeration is the bound rather than a menu — a shape absent from it has
// no path through this package.
//
// Two further bounds sit on top of that one:
//
//   - The result must be a document the author could have typed. Every edit is
//     composed, spliced and then VALIDATED against the document kind's own
//     schema, and the bytes reach disk only if that validation passes. So a
//     surface cannot use this to write something the authoring schema refuses —
//     a deployment document declaring configuration values it may not, say —
//     because the refusal comes from the same schema that would refuse the
//     hand-typed document. The rule is not restated here; it is enforced where it
//     already lives.
//   - Whether the thing being wired is semantically admissible — whether a
//     dependency socket is one the producer declares, whether a Feature's
//     applicability is satisfied — is NOT decided here, and deliberately so.
//     Those questions belong to the resolution the compiler already performs,
//     each with its own reporting, and a copy of one here would be a second
//     answer that agreed until the day one of them was edited. A consumer may
//     name a connection this package has never heard of; what may be consumed
//     across it is the producer's to declare and the compiler's to check.
//
// # Format preservation
//
// The document's bytes are parsed to a node tree only to LOCATE the container
// the entry goes in and the line its block ends at. The entry is rendered and
// spliced at the line level, so every other byte carries through verbatim —
// indentation, comments, folded and literal scalars, blank lines, quoting style,
// and the trailing-newline shape. The nesting step is measured from the
// document's own indentation rather than imposed, so an entry lands in the style
// the author is already writing in.
//
// # Determinism
//
// The same document and the same edit produce the same bytes. The rendered entry
// is built as an explicit node tree in a fixed key order rather than encoded from
// a Go map, whose iteration order is unspecified — so the output does not depend
// on anything but the inputs.
package editops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// Kind names one bounded structured edit.
//
// The four are the whole set, and the set is the bound: there is no kind for
// deleting, replacing or reordering, so no caller can reach those. Each value is
// in the descriptive register — lowercase words joined by dashes — like every
// other platform-defined identifier that is not a halting condition's short code.
//
// What each kind fixes is the SHAPE of the entry written and the container it
// goes in relative to the node the request names. What it does not fix is where
// that node sits: the request supplies the node's own structural path, so a
// component authored at the top level and one authored inside a subsystem are
// both reachable without this package guessing between them.
type Kind string

const (
	// KindAddComponent adds a component instance under a components container.
	// The entry is the instance name the author chooses and the Component the
	// instance requests.
	KindAddComponent Kind = "add-component"

	// KindAttachFeature attaches a Feature to the node the request names. The
	// entry is the attachment's instance name and the Feature it requests.
	KindAttachFeature Kind = "attach-feature"

	// KindWireEdge wires a dependency edge from the node the request names,
	// inside one of that node's sockets. The entry is the alias the author
	// addresses the connection by and the node it points at.
	KindWireEdge Kind = "wire-edge"

	// KindSetValue sets one value on the node the request names. The entry is
	// the value's key and a scalar.
	KindSetValue Kind = "set-value"
)

// Options is one edit request.
//
// # Why the node is a path rather than a name
//
// Node is a structural breadcrumb from the document root, not a component's
// name. A name would have to be searched for, and a search would have to decide
// what to do about a name appearing at two levels — which is this package
// deciding an author's meaning from a spelling. A path addresses exactly one
// place, so a caller that knows which node it is editing says so, and a caller
// that does not knows it has a question to answer before editing anything.
//
// It is the same breadcrumb vocabulary the position-reporting surfaces use, so a
// caller that asked where an entry belongs and a caller that applies one speak
// about the document in one language.
type Options struct {
	// File names the document, relative to the workspace root or by absolute
	// path, as every other per-document request is named.
	File string

	// Kind is the edit.
	Kind Kind

	// Node is the structural path to the node the edit is about, from the
	// document root — for example spec.components.web, spelled as its segments.
	//
	// For KindAddComponent it names the components container the instance is
	// added to, because there is no node yet. For the other three it names the
	// node the edit is about, and the container is derived from the kind.
	Node []string

	// Socket is the dependency socket the edge is wired inside. It is read for
	// KindWireEdge and ignored otherwise.
	Socket string

	// Name is the key the new entry is written under: the component instance
	// name, the attachment's instance name, the connection's alias, or the
	// value's key.
	Name string

	// Value is what the entry carries. Its meaning follows the kind: the
	// Component or Feature the instance requests, the node an edge points at,
	// or the scalar a value is set to.
	//
	// For KindSetValue it may be any scalar — a string, a boolean, a number, or
	// nothing at all. A mapping or a sequence is refused, because writing one
	// would be adding a block rather than setting a value, and that is outside
	// this package's bound.
	Value any

	// Delivery says who writes the result. The zero value writes, so a caller
	// that states nothing gets the behaviour it would have had before a
	// reporting delivery existed.
	Delivery authoredcontent.Delivery
}

// Result is what an edit produced.
type Result struct {
	// Documents carries the edited document: the file it is for and the content
	// the edit produced. It is populated under both deliveries, so a caller that
	// reports and a caller that writes are told the same thing about what the
	// edit came to.
	Documents []authoredcontent.Document

	// Written names each file that reached disk, workspace-relative. It is empty
	// under a reporting delivery, which writes nothing.
	Written []string

	// Container is the structural path of the container the entry landed in, so
	// a caller can locate what it just added without re-deriving the kind's
	// container rule for itself.
	Container []string

	// CreatedContainers are the container segments the document did not carry
	// and this edit wrote, outermost first. It is empty where the whole
	// container path was already there, which is the ordinary case.
	CreatedContainers []string
}

// Apply performs the edit described by opts against the document under root and
// returns what it produced.
//
// Nothing reaches disk unless the edited document validates against its own
// kind's schema. A document that would not is refused with
// [ErrEditRefusedInvalidResult] and the file is left byte-for-byte as it was, so
// a refused edit is indistinguishable from an edit never attempted.
//
// An edit whose entry is already present, carrying what the request asked for,
// is a no-op: the document is reported unchanged and nothing is written. An
// entry present carrying something else is refused rather than replaced, because
// replacing an entry an author wrote is outside this package's bound.
func Apply(root string, opts Options) (Result, error) {
	if strings.TrimSpace(root) == "" {
		return Result{}, errNoWorkspace
	}
	abs, rel, err := resolveInside(root, opts.File)
	if err != nil {
		return Result{}, err
	}
	if err := checkRequest(opts); err != nil {
		return Result{}, err
	}

	before, err := os.ReadFile(abs)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %s", errFileUnreadable, rel)
	}

	kind, err := documentKind(before, rel)
	if err != nil {
		return Result{}, err
	}

	container, err := containerFor(opts)
	if err != nil {
		return Result{}, err
	}

	spliced, created, changed, err := splice(before, container, opts)
	if err != nil {
		return Result{}, err
	}

	// The validation runs on an unchanged document too. A document that was
	// already invalid before this edit reports that fact rather than reporting
	// success, so a caller is never told an edit landed in a document the
	// product would refuse to read.
	if _, verr := envelope.ValidateBytes(kind, spliced, rel); verr != nil {
		return Result{}, fmt.Errorf("%w: %s", ErrEditRefusedInvalidResult(), verr.Error())
	}

	result := Result{
		Documents:         []authoredcontent.Document{{Path: rel, Content: string(spliced)}},
		Container:         container,
		CreatedContainers: created,
	}
	if opts.Delivery.Reports() || !changed {
		return result, nil
	}
	if werr := writeFileAtomic(abs, spliced, 0o644); werr != nil {
		return Result{}, fmt.Errorf("write %s: %w", rel, werr)
	}
	result.Written = []string{rel}
	return result, nil
}

// containerFor returns the structural path of the container the entry goes in,
// derived from the kind and the node the request names.
//
// It is the one home of that derivation. The mapping is the authoring surface's
// own vocabulary — the keys an author types — and it is written once here rather
// than at each calling surface, so two surfaces cannot come to disagree about
// where an attachment belongs.
func containerFor(opts Options) ([]string, error) {
	node := append([]string(nil), opts.Node...)
	switch opts.Kind {
	case KindAddComponent:
		// The request names the components container itself, because the entry
		// being added IS the node.
		return node, nil
	case KindAttachFeature:
		return append(node, "features"), nil
	case KindWireEdge:
		return append(node, "uses", opts.Socket), nil
	case KindSetValue:
		return append(node, "values"), nil
	default:
		return nil, fmt.Errorf("%w: %q", errUnknownKind, string(opts.Kind))
	}
}

// nodeDepth reports how many of the container path's leading segments address a
// node that must already be there, as opposed to a container this edit may
// create on the author's behalf.
//
// For the three node-scoped edits it is the whole of the request's node path:
// attaching, wiring and setting all happen ON a node, so the node is a premise
// rather than something the edit brings into being. For adding a component it is
// zero, because the request's path names a container and the entry being added
// IS the node — so the container may be created and nothing is presupposed.
func nodeDepth(opts Options) int {
	if opts.Kind == KindAddComponent {
		return 0
	}
	return len(opts.Node)
}

// entryValue renders what the entry carries as an explicit node tree.
//
// The tree is built rather than encoded from a Go map, and that is a
// determinism requirement rather than a style: a map's iteration order is
// unspecified, so encoding one would let the same edit produce different bytes
// on different runs. Every shape below carries its keys in a fixed order by
// construction.
func entryValue(opts Options) (*yaml.Node, error) {
	switch opts.Kind {
	case KindAddComponent, KindAttachFeature:
		// Both request an entity by reference under the same keyword, which is
		// the authoring surface's own shape for an instance and an attachment.
		return mappingNode("type", scalarNode(opts.Value)), nil
	case KindWireEdge:
		return mappingNode("target", scalarNode(opts.Value)), nil
	case KindSetValue:
		return scalarNode(opts.Value), nil
	default:
		return nil, fmt.Errorf("%w: %q", errUnknownKind, string(opts.Kind))
	}
}

// mappingNode builds a single-pair block mapping.
func mappingNode(key string, value *yaml.Node) *yaml.Node {
	return &yaml.Node{
		Kind:    yaml.MappingNode,
		Tag:     "!!map",
		Content: []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value},
	}
}

// scalarNode builds the scalar a request supplied.
//
// A string is tagged as a string explicitly rather than left to the encoder's
// resolution. Without the tag a value the author meant as text — a version like
// 1.0, or the word "true" — would be written unquoted and read back as a number
// or a boolean, which is this package changing the type of what it was given.
// checkRequest has already refused anything that is not a scalar.
func scalarNode(v any) *yaml.Node {
	switch t := v.(type) {
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: t}
	default:
		var n yaml.Node
		// A non-string scalar has one spelling under the encoder's own
		// resolution, so it needs no tag of its own. The error cannot fire for
		// the scalar kinds checkRequest admits.
		_ = n.Encode(v)
		return &n
	}
}

// documentKind reads the document's own kind and reports it as the family whose
// schema the result is validated against.
//
// The kind is read off the document rather than supplied by the caller. A caller
// naming the kind could name the wrong one, and the result would then be
// validated against a schema the document is not written to — which would admit
// exactly the edit the validation exists to refuse.
func documentKind(data []byte, path string) (envelope.Kind, error) {
	var head struct {
		Kind string `yaml:"kind"`
	}
	if err := yaml.Unmarshal(data, &head); err != nil {
		return "", fmt.Errorf("%w: %s", errFileUnparseable, path)
	}
	if strings.TrimSpace(head.Kind) == "" {
		return "", fmt.Errorf("%w: %s", errNoDocumentKind, path)
	}
	for _, known := range envelope.Kinds() {
		if string(known) == head.Kind {
			return known, nil
		}
	}
	return "", fmt.Errorf("%w: %q in %s", errUnknownDocumentKind, head.Kind, path)
}

// checkRequest refuses a request that does not address an edit, before anything
// is read or written.
func checkRequest(opts Options) error {
	switch opts.Kind {
	case KindAddComponent, KindAttachFeature, KindWireEdge, KindSetValue:
	default:
		return fmt.Errorf("%w: %q", errUnknownKind, string(opts.Kind))
	}
	if len(opts.Node) == 0 {
		return errNoNodePath
	}
	for _, seg := range opts.Node {
		if strings.TrimSpace(seg) == "" {
			return errNoNodePath
		}
	}
	if strings.TrimSpace(opts.Name) == "" {
		return errNoEntryName
	}
	if opts.Kind == KindWireEdge && strings.TrimSpace(opts.Socket) == "" {
		return errNoSocket
	}
	if opts.Kind == KindSetValue {
		switch opts.Value.(type) {
		case nil, string, bool, int, int32, int64, float32, float64:
		default:
			return fmt.Errorf("%w: %T", errValueNotScalar, opts.Value)
		}
		return nil
	}
	// The other three kinds carry an entity reference or a target, which is a
	// string and cannot be empty: an entry naming nothing is an entry no
	// resolution can act on.
	ref, ok := opts.Value.(string)
	if !ok {
		return fmt.Errorf("%w: %T", errValueNotAReference, opts.Value)
	}
	if strings.TrimSpace(ref) == "" {
		return errValueNotAReference
	}
	return nil
}

// resolveInside resolves the request's file against the workspace root and
// reports it both absolutely and relative to that root.
//
// A path that climbs out of the workspace is refused. The predicate is the one
// the rest of the product resolves paths with, so a path one surface accepts
// every other surface accepts identically.
func resolveInside(root, file string) (abs string, rel string, err error) {
	if strings.TrimSpace(file) == "" {
		return "", "", errNoFile
	}
	abs = file
	if !filepath.IsAbs(abs) {
		resolved, ok := pathscope.Resolve(root, file)
		if !ok {
			return "", "", fmt.Errorf("%w: %q", errFileOutsideWorkspace, file)
		}
		abs = resolved
	}
	abs = filepath.Clean(abs)
	r, err := filepath.Rel(filepath.Clean(root), abs)
	if err != nil || !pathscope.IsLocalSlash(filepath.ToSlash(r)) {
		return "", "", fmt.Errorf("%w: %q", errFileOutsideWorkspace, file)
	}
	return abs, filepath.ToSlash(r), nil
}

// writeFileAtomic writes data to path atomically: it writes a temporary file in
// the same directory and renames it over path, so a reader never observes a
// partial document and an interrupted write leaves the author's file intact.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, perm); err != nil {
		return err
	}
	return os.Rename(name, path)
}
