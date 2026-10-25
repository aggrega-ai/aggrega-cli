// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// The one decode of a compose fragment into the types this package carries.
//
// Every fragment the merge folds — one a workload synthesized, one rendered from
// a compose source with the node's values on top, one an artifacts: body wrote —
// reaches the assembled file only through ComposeFile, and a key ComposeFile
// does not declare has nowhere to go there. A plain decode into the struct skips
// such a key and the assembled file comes out without it, a service that runs
// differently from the one written and nothing to say so. So this decode walks
// the fragment's keys against the shape it decodes into first, and refuses the
// fragment naming every key it would leave behind, where it sits in the
// fragment, and the fragment it came from. The merge is the only caller, which
// is what makes this the one place the refusal is raised.
//
// A fragment's top-level networks: block is the one place a declared key is
// refused: the assembled file's networks are the ones the merge derives, so an
// entry a fragment declares there has nowhere to go and is refused as a whole.
//
// The walk reads the shape from the types themselves — the yaml tag on each
// field, an inline struct's fields as the parent's own, an inline map as
// accepting every remaining key — so a key added to a type is carried the moment
// it is declared, with nothing here to update. A type that decodes itself
// declares the shape of what it reads through fragmentKeyShape, which is what
// lets the walk reach inside a long-form depends_on or models entry.
//
// Nothing here reads what a key means. A key is carried when a type declares it
// and refused when none does; its value is the decoder's business, and a value
// of the wrong shape fails that decode as it always has.
//
// This file carries no build tag: it compiles for the host and for the wasip1
// guest alike, so it imports no host-only capability.

package composecore

import (
	"fmt"
	"reflect"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// fragmentKeyShape is implemented by a field type that decodes itself, so the
// walk cannot read its shape from struct tags. walkFragmentKeys walks node — the
// YAML the type's own decode reads — and reports through w every key that decode
// would not carry, at its path below path.
type fragmentKeyShape interface {
	walkFragmentKeys(w *fragmentKeyWalk, node *yaml.Node, path string)
}

// opaqueFragmentTypes are the self-decoding types whose content holds no key of
// a compose shape: each one carries what it decodes as written. The environment
// mapping is the one such type — its keys are variable names the author chose,
// and every one of them reaches the assembled file.
var opaqueFragmentTypes = map[reflect.Type]bool{
	reflect.TypeOf(types.OrderedMap[string]{}): true,
}

var (
	fragmentKeyShapeType = reflect.TypeOf((*fragmentKeyShape)(nil)).Elem()
	composeFileType      = reflect.TypeOf(ComposeFile{})
)

// decodeFragment decodes one compose fragment into a ComposeFile, refusing it
// when it writes a key ComposeFile does not carry. nodeID names the node the
// fragment belongs to; origin names the fragment and leads the refusal's
// sentence. A source-rendered fragment is named `<nodeID>.compose-fragment.yaml`
// and an artifacts: fragment by the name its Recipe gave it.
//
// The first YAML document is the one decoded; a later document in the same body
// is not read. An empty fragment decodes to the zero ComposeFile.
func decodeFragment(body []byte, nodeID, origin string) (ComposeFile, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return ComposeFile{}, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("compose: node %q %s: parse", nodeID, origin))
	}
	if doc.Kind == 0 {
		return ComposeFile{}, nil
	}

	w := &fragmentKeyWalk{}
	root := &doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	w.walk(root, composeFileType, "")
	if refusal := w.refusal(); refusal != "" {
		return ComposeFile{}, cerrors.Wrap(wasm.ErrPluginCannotExpressInput(),
			cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
			fmt.Sprintf("compose: node %q %s %s", nodeID, origin, refusal))
	}

	var frag ComposeFile
	if err := doc.Decode(&frag); err != nil {
		return ComposeFile{}, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("compose: node %q %s: parse", nodeID, origin))
	}
	return frag, nil
}

// refusal words what the walk found, or returns "" when it found nothing: the
// networks the fragment declares of its own, then the keys it writes that the
// types do not carry, in one sentence so one halt names everything the author
// has to change.
func (w *fragmentKeyWalk) refusal() string {
	var parts []string
	if n := len(w.networks); n > 0 {
		parts = append(parts, fmt.Sprintf("declares %s of its own under networks:, %s, which the assembled file cannot hold: the compose provider writes only the networks it derives, one for each aggrega.ai/namespace annotation among the contributing nodes or for the boundary path where a node carries none, and a service joins its own node's network with nothing declared, so remove %s from the fragment, set aggrega.ai/namespace to choose the network a node joins, and attach the compose-bridge Feature to reach a service on another network",
			pluralNetworks(n), strings.Join(w.networks, "; "), pronounEntries(n)))
	}
	if n := len(w.uncarried); n > 0 {
		parts = append(parts, fmt.Sprintf("writes %s the compose provider does not carry into the assembled file: %s; the assembled file holds only the keys the provider carries, so where %s written in the fragment, remove %s from it, and where the fragment is rendered from a compose source whose Recipe declares no valuesMapping:, the node's values are laid over that file as they are, so declare a valuesMapping: that places each value under a key the provider carries",
			pluralKeys(n), strings.Join(w.uncarried, "; "), keysAre(n), pronounKeys(n)))
	}
	return strings.Join(parts, "; and it ")
}

func pluralNetworks(n int) string {
	if n == 1 {
		return "a network"
	}
	return "networks"
}

func pronounEntries(n int) string {
	if n == 1 {
		return "the entry"
	}
	return "the entries"
}

// pluralKeys, keysAre and pronounKeys word the refusal for one key or for
// several.
func keysAre(n int) string {
	if n == 1 {
		return "it is"
	}
	return "they are"
}

func pluralKeys(n int) string {
	if n == 1 {
		return "a key"
	}
	return "keys"
}

func pronounKeys(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// fragmentKeyWalk collects, in document order, every key a fragment writes that
// the type it decodes into does not carry, each as `"<key>" at <path>`, and
// every network the fragment declares of its own, each as `"<name>" at
// networks.<name>`.
type fragmentKeyWalk struct {
	uncarried []string
	networks  []string
}

// report records one key the decode would not carry.
func (w *fragmentKeyWalk) report(key, path string) {
	w.uncarried = append(w.uncarried, fmt.Sprintf("%q at %s", key, path))
}

// networkEntries records every entry of a fragment's top-level networks: block.
//
// The assembled file's networks are the ones the merge derives from the
// contributing nodes — one per aggrega.ai/namespace annotation, or per boundary
// path where a node carries none — and nothing a fragment declares at the top
// level reaches it. So an entry is refused as a whole, whatever its body holds,
// rather than read and left out. An empty or absent block declares nothing and
// passes. The entry's body is not walked: refusing the entry already names
// everything in it.
func (w *fragmentKeyWalk) networkEntries(node *yaml.Node) {
	node = resolveFragmentAlias(node)
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	for _, kv := range fragmentPairs(node) {
		w.networks = append(w.networks, fmt.Sprintf("%q at %s", kv.key, joinFragmentPath("networks", kv.key)))
	}
}

// walk reports every key below node that a decode into t would not carry. A node
// whose kind does not match what t decodes from is left to the decode itself,
// which refuses it; the walk only ever reports keys.
func (w *fragmentKeyWalk) walk(node *yaml.Node, t reflect.Type, path string) {
	node = resolveFragmentAlias(node)
	if node == nil {
		return
	}
	if t.Kind() == reflect.Pointer {
		w.walk(node, t.Elem(), path)
		return
	}
	if t.Implements(fragmentKeyShapeType) {
		reflect.Zero(t).Interface().(fragmentKeyShape).walkFragmentKeys(w, node, path)
		return
	}
	// A self-decoding type is reached here only when it is opaque. One that is
	// neither opaque nor shaped falls through to the struct walk, where its
	// unexported fields name no key and every key it is given is reported: an
	// undeclared shape refuses loudly rather than letting a key through unread.
	if opaqueFragmentTypes[t] {
		return
	}
	switch t.Kind() {
	case reflect.Struct:
		if node.Kind != yaml.MappingNode {
			return
		}
		fields, acceptsAny := fragmentFields(t)
		for _, kv := range fragmentPairs(node) {
			if t == composeFileType && path == "" && kv.key == "networks" {
				w.networkEntries(kv.value)
				continue
			}
			ft, carried := fields[kv.key]
			if !carried {
				if !acceptsAny {
					w.report(kv.key, joinFragmentPath(path, kv.key))
				}
				continue
			}
			w.walk(kv.value, ft, joinFragmentPath(path, kv.key))
		}
	case reflect.Map:
		if node.Kind != yaml.MappingNode {
			return
		}
		for _, kv := range fragmentPairs(node) {
			w.walk(kv.value, t.Elem(), joinFragmentPath(path, kv.key))
		}
	case reflect.Slice, reflect.Array:
		if node.Kind != yaml.SequenceNode {
			return
		}
		for i, item := range node.Content {
			w.walk(item, t.Elem(), fmt.Sprintf("%s[%d]", path, i))
		}
	}
}

// fragmentFields maps each key a struct decodes to the type of the field it
// lands in, with an inline struct's keys taken as the struct's own, and reports
// whether an inline map takes every key the fields do not name. It reads the
// yaml tags the decoder reads, so the two cannot disagree about a key.
func fragmentFields(t reflect.Type) (map[string]reflect.Type, bool) {
	fields := map[string]reflect.Type{}
	acceptsAny := false
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" && !f.Anonymous {
			continue
		}
		tag := f.Tag.Get("yaml")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if hasTagOption(opts, "inline") {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			switch ft.Kind() {
			case reflect.Map:
				acceptsAny = true
			case reflect.Struct:
				inner, innerAny := fragmentFields(ft)
				for k, v := range inner {
					fields[k] = v
				}
				acceptsAny = acceptsAny || innerAny
			}
			continue
		}
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		fields[name] = f.Type
	}
	return fields, acceptsAny
}

func hasTagOption(opts, want string) bool {
	for _, o := range strings.Split(opts, ",") {
		if o == want {
			return true
		}
	}
	return false
}

// fragmentPair is one key of a mapping and the value written for it.
type fragmentPair struct {
	key   string
	value *yaml.Node
}

// fragmentPairs lists a mapping's keys in document order, with every key a YAML
// merge key (`<<`) brings in listed where the merge sits, as the decoder reads
// them: a merged key lands in the same struct as one written in place, so it is
// carried or dropped on the same terms.
func fragmentPairs(mapping *yaml.Node) []fragmentPair {
	var out []fragmentPair
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		k, v := mapping.Content[i], mapping.Content[i+1]
		if k.Kind == yaml.ScalarNode && k.ShortTag() == "!!merge" {
			out = append(out, mergedFragmentPairs(v)...)
			continue
		}
		out = append(out, fragmentPair{key: k.Value, value: v})
	}
	return out
}

// mergedFragmentPairs lists the keys one merge value brings in: a mapping, or a
// sequence of mappings, each possibly reached through an alias.
func mergedFragmentPairs(v *yaml.Node) []fragmentPair {
	v = resolveFragmentAlias(v)
	if v == nil {
		return nil
	}
	switch v.Kind {
	case yaml.MappingNode:
		return fragmentPairs(v)
	case yaml.SequenceNode:
		var out []fragmentPair
		for _, item := range v.Content {
			out = append(out, mergedFragmentPairs(item)...)
		}
		return out
	}
	return nil
}

// resolveFragmentAlias follows an alias, through a chain of them, to the node it
// names.
func resolveFragmentAlias(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// joinFragmentPath names a key below path, the dotted path from the fragment's
// root to it.
func joinFragmentPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}
