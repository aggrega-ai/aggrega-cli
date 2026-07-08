// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package recipepin owns the resolution-time freeze of a Recipe or
// FeatureRecipe's intrinsic-input content pins — the sourceDigests (one per
// resolved remote source) and artifactDigests (one per $import artifact file)
// sections — into a writable recipe file, plus the pure pin-matching helpers
// the build-time verify consults.
//
// The freeze is a surgical, deterministic YAML-node edit modeled on the
// workspace-config writer: it rewrites only the two pin keys under spec,
// carrying every other key, value, comment, and scalar style through untouched,
// and it leaves the file byte-identical when the freshly-computed pins equal the
// pins already present (an unchanged declaration is read, not re-frozen). The
// pins are frozen FROM the sources at resolution and read back at build; the
// package never fetches — the caller supplies the resolved source pins and the
// $import file bytes are read from disk.
package recipepin

import (
	"fmt"
	"os"
	"reflect"
	"sort"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// WritePins freezes srcPins and artPins into the recipe file at path, rewriting
// only the spec.sourceDigests and spec.artifactDigests keys and preserving every
// other key, value, comment, and scalar style. Both pin lists are sorted to a
// canonical order before comparison and emission (sourceDigests by
// {kind, url, version}, artifactDigests by name) so the write is deterministic.
//
// The write is skipped — and the file left byte-identical — when the recipe
// already carries pins equal to the freshly-computed ones (a declaration whose
// sources and $import paths are unchanged is read, not re-frozen). A recipe
// whose declared sources or $import set changed re-freezes. Returns whether the
// file was rewritten.
//
// An empty srcPins (respectively artPins) removes the corresponding key rather
// than emitting an empty list, so a recipe that no longer references any remote
// source or any $import artifact carries no vestigial pin section.
func WritePins(path string, srcPins []types.SourceDigest, artPins []types.ArtifactDigest) (bool, error) {
	src := append([]types.SourceDigest(nil), srcPins...)
	art := append([]types.ArtifactDigest(nil), artPins...)
	sortSourcePins(src)
	sortArtifactPins(art)

	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("recipepin: read %s: %w", path, err)
	}

	var root yaml.Node
	if uerr := yaml.Unmarshal(data, &root); uerr != nil {
		return false, fmt.Errorf("recipepin: parse %s: %w", path, uerr)
	}

	spec := specMapping(&root)
	if spec == nil {
		return false, fmt.Errorf("recipepin: %s: no spec mapping to pin into", path)
	}

	// A declaration whose pins already match is read, not re-frozen: leave the
	// file untouched so a re-resolution over an unchanged recipe never churns it.
	existingSrc, existingArt := decodeExistingPins(spec)
	sortSourcePins(existingSrc)
	sortArtifactPins(existingArt)
	if pinsEqual(existingSrc, src) && artifactPinsEqual(existingArt, art) {
		return false, nil
	}

	if err := setOrDeleteSeq(spec, "sourceDigests", len(src) > 0, src); err != nil {
		return false, fmt.Errorf("recipepin: encode sourceDigests: %w", err)
	}
	if err := setOrDeleteSeq(spec, "artifactDigests", len(art) > 0, art); err != nil {
		return false, fmt.Errorf("recipepin: encode artifactDigests: %w", err)
	}

	out, merr := yaml.Marshal(&root)
	if merr != nil {
		return false, fmt.Errorf("recipepin: marshal %s: %w", path, merr)
	}
	if werr := os.WriteFile(path, out, 0o644); werr != nil {
		return false, fmt.Errorf("recipepin: write %s: %w", path, werr)
	}
	return true, nil
}

// ArtifactPinsForRecipe computes the artifactDigests for a recipe's artifacts
// map: one pin per entry whose value is a file import ($import), digesting the
// imported file's bytes. Inline (literal) entries are part of the entity and
// carry no pin. Each entry's Import is the resolved absolute $import path (the
// catalog rewrites relative $import paths to absolute at load). The result is
// sorted by name.
func ArtifactPinsForRecipe(artifacts types.OrderedMap[types.ArtifactEntry]) ([]types.ArtifactDigest, error) {
	var pins []types.ArtifactDigest
	for _, name := range artifacts.Keys() {
		entry, _ := artifacts.Get(name)
		if entry.Import == "" {
			continue
		}
		body, err := os.ReadFile(entry.Import)
		if err != nil {
			return nil, fmt.Errorf("recipepin: read artifact %q import %s: %w", name, entry.Import, err)
		}
		pins = append(pins, types.ArtifactDigest{Name: name, Digest: digest.ContentDigest(body)})
	}
	sortArtifactPins(pins)
	return pins, nil
}

// MatchSourcePin returns the recipe-carried source pin for the identity triple
// {kind, url, version}, and whether one is present.
func MatchSourcePin(pins []types.SourceDigest, kind, url, version string) (types.SourceDigest, bool) {
	for _, p := range pins {
		if p.Kind == kind && p.URL == url && p.Version == version {
			return p, true
		}
	}
	return types.SourceDigest{}, false
}

// MatchArtifactPin returns the recipe-carried artifact pin for the artifact key
// name, and whether one is present.
func MatchArtifactPin(pins []types.ArtifactDigest, name string) (types.ArtifactDigest, bool) {
	for _, p := range pins {
		if p.Name == name {
			return p, true
		}
	}
	return types.ArtifactDigest{}, false
}

func sortSourcePins(p []types.SourceDigest) {
	sort.Slice(p, func(i, j int) bool {
		if p[i].Kind != p[j].Kind {
			return p[i].Kind < p[j].Kind
		}
		if p[i].URL != p[j].URL {
			return p[i].URL < p[j].URL
		}
		return p[i].Version < p[j].Version
	})
}

func sortArtifactPins(p []types.ArtifactDigest) {
	sort.Slice(p, func(i, j int) bool { return p[i].Name < p[j].Name })
}

func pinsEqual(a, b []types.SourceDigest) bool           { return reflect.DeepEqual(a, b) }
func artifactPinsEqual(a, b []types.ArtifactDigest) bool { return reflect.DeepEqual(a, b) }

// specMapping returns the mapping node for the document's top-level spec key,
// or nil when the document is not a mapping or has no spec mapping.
func specMapping(root *yaml.Node) *yaml.Node {
	n := root
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == "spec" && n.Content[i+1].Kind == yaml.MappingNode {
			return n.Content[i+1]
		}
	}
	return nil
}

// decodeExistingPins reads the current sourceDigests / artifactDigests values
// from the spec mapping, tolerating their absence (nil slices).
func decodeExistingPins(spec *yaml.Node) ([]types.SourceDigest, []types.ArtifactDigest) {
	var src []types.SourceDigest
	var art []types.ArtifactDigest
	for i := 0; i+1 < len(spec.Content); i += 2 {
		switch spec.Content[i].Value {
		case "sourceDigests":
			_ = spec.Content[i+1].Decode(&src)
		case "artifactDigests":
			_ = spec.Content[i+1].Decode(&art)
		}
	}
	return src, art
}

// setOrDeleteSeq sets key in the spec mapping to an encoding of value when
// present is true, or removes the key entirely when present is false.
func setOrDeleteSeq(spec *yaml.Node, key string, present bool, value any) error {
	if !present {
		deleteMappingKey(spec, key)
		return nil
	}
	var valNode yaml.Node
	if err := valNode.Encode(value); err != nil {
		return err
	}
	for i := 0; i+1 < len(spec.Content); i += 2 {
		if spec.Content[i].Value == key {
			spec.Content[i+1] = &valNode
			return nil
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	spec.Content = append(spec.Content, keyNode, &valNode)
	return nil
}

// deleteMappingKey removes key and its value from a mapping node, if present.
func deleteMappingKey(mapping *yaml.Node, key string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
	}
}
