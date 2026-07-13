// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package recipepin owns the resolution-time freeze of a Recipe or
// FeatureRecipe's intrinsic-input content pins — the sourceDigests (one per
// resolved remote source) and artifactDigests (one per $import artifact file)
// sections — into a writable recipe file, plus the pure pin-matching helpers
// the build-time verify consults.
//
// The freeze is a surgical, byte-faithful splice: the original file bytes are
// parsed to a YAML node tree only to LOCATE the spec mapping and the pin
// blocks' line ranges — never to re-emit the document. Only the two pin
// sections are rewritten; every other byte of the file — indentation, comments,
// folded and literal scalars, blank lines, and the trailing-newline shape —
// carries through verbatim. The file is left byte-identical when the
// freshly-computed pins equal the pins already present (an unchanged declaration
// is read, not re-frozen). The pins are frozen FROM the sources at resolution
// and read back at build; the package never fetches — the caller supplies the
// resolved source pins and the $import file bytes are read from disk.
package recipepin

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// WritePins freezes srcPins and artPins into the recipe file at path, rewriting
// only the spec.sourceDigests and spec.artifactDigests blocks and carrying every
// other byte of the file through verbatim. Both pin lists are sorted to a
// canonical order before comparison and emission (sourceDigests by
// {kind, url, chart, version}, artifactDigests by name) so the write is deterministic.
//
// The edit is a line-range splice, not a document re-marshal: the original bytes
// are parsed only to locate the spec mapping and the pin blocks' extents, then
// the pin sections are added, replaced in place, or removed while indentation,
// comments, folded and literal scalars, blank lines, and the trailing-newline
// shape of the surrounding content stay untouched. Added pins land at the end of
// the spec mapping, sourceDigests first, separated from the preceding content by
// exactly one blank line.
//
// The write is skipped — and the file left byte-identical — when the recipe
// already carries pins equal to the freshly-computed ones (a declaration whose
// sources and $import paths are unchanged is read, not re-frozen). A recipe
// whose declared sources or $import set changed re-freezes. Returns whether the
// file was rewritten.
//
// An empty srcPins (respectively artPins) removes the corresponding block rather
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

	specKey, spec := specMapping(&root)
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

	out, serr := splicePins(data, specKey, spec, src, art)
	if serr != nil {
		return false, fmt.Errorf("recipepin: freeze %s: %w", path, serr)
	}
	if werr := os.WriteFile(path, out, 0o644); werr != nil {
		return false, fmt.Errorf("recipepin: write %s: %w", path, werr)
	}
	return true, nil
}

// splicePins produces the recipe bytes with the sourceDigests / artifactDigests
// blocks brought into agreement with src / art, editing only the pin line ranges
// and carrying every other byte of data through unchanged. It adds an absent
// block at the end of the spec mapping (with exactly one leading blank line),
// replaces a present block in place, or removes a block whose pin list is empty
// (also reclaiming that block's leading blank separator).
func splicePins(data []byte, specKey, spec *yaml.Node, src []types.SourceDigest, art []types.ArtifactDigest) ([]byte, error) {
	lines := strings.Split(string(data), "\n")

	specIndent := specKey.Column - 1
	childIndent := specIndent + 2
	if len(spec.Content) > 0 {
		childIndent = spec.Content[0].Column - 1
	}
	step := childIndent - specIndent
	if step <= 0 {
		step = 2
	}

	// Render the desired pin section (sourceDigests first, then artifactDigests),
	// indented to sit under spec at the file's own indent step.
	var section []string
	if len(src) > 0 {
		block, rerr := renderPinBlock("sourceDigests", src, childIndent, step)
		if rerr != nil {
			return nil, fmt.Errorf("encode sourceDigests: %w", rerr)
		}
		section = append(section, block...)
	}
	if len(art) > 0 {
		block, rerr := renderPinBlock("artifactDigests", art, childIndent, step)
		if rerr != nil {
			return nil, fmt.Errorf("encode artifactDigests: %w", rerr)
		}
		section = append(section, block...)
	}

	// Locate the current pin section (the union of any present pin blocks).
	sectionStart, sectionEnd, present := currentPinRegion(lines, spec, childIndent)

	var result []string
	switch {
	case !present && len(section) == 0:
		// No pins present and none wanted: nothing to splice. The skip path in
		// WritePins covers the equal-pins case, so this is only reached when the
		// caller passes empty lists for a recipe that already carries none.
		return data, nil

	case !present:
		// Add: insert at the end of the spec mapping's content, preceded by
		// exactly one blank line.
		insertAt := endOfSpecContent(lines, specKey, spec, childIndent)
		insert := section
		if !(insertAt > 0 && strings.TrimSpace(lines[insertAt-1]) == "") {
			insert = append([]string{""}, section...)
		}
		result = append(result, lines[:insertAt]...)
		result = append(result, insert...)
		result = append(result, lines[insertAt:]...)

	case len(section) == 0:
		// Remove: drop the block's line range and its single leading blank
		// separator so no doubled or trailing blank churn is left behind.
		cut := sectionStart
		if cut > 0 && strings.TrimSpace(lines[cut-1]) == "" {
			cut--
		}
		result = append(result, lines[:cut]...)
		result = append(result, lines[sectionEnd:]...)

	default:
		// Change: replace the block's line range in place, keeping the leading
		// blank separator (already present, or restored when absent).
		replacement := section
		if !(sectionStart > 0 && strings.TrimSpace(lines[sectionStart-1]) == "") {
			replacement = append([]string{""}, section...)
		}
		result = append(result, lines[:sectionStart]...)
		result = append(result, replacement...)
		result = append(result, lines[sectionEnd:]...)
	}

	return []byte(strings.Join(result, "\n")), nil
}

// renderPinBlock serializes a single pin key and its value as YAML lines
// indented to sit under spec: the key at keyIndent spaces and its sequence at
// the file's indent step. The rendering is deterministic — a yaml.Encoder emits
// the {key: value} subtree, and each line is shifted right by keyIndent spaces.
func renderPinBlock(key string, value any, keyIndent, step int) ([]string, error) {
	var valNode yaml.Node
	if err := valNode.Encode(value); err != nil {
		return nil, err
	}
	m := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			&valNode,
		},
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(step)
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}

	prefix := strings.Repeat(" ", keyIndent)
	raw := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	out := make([]string, len(raw))
	for i, line := range raw {
		if line == "" {
			out[i] = ""
			continue
		}
		out[i] = prefix + line
	}
	return out, nil
}

// currentPinRegion returns the line range [start, end) 0-indexed spanning the
// pin section currently present in lines — the union of the sourceDigests and
// artifactDigests block extents — and whether any pin block is present. The pin
// blocks are written contiguously at the end of the spec mapping, so their union
// is a single range.
func currentPinRegion(lines []string, spec *yaml.Node, childIndent int) (int, int, bool) {
	start, end := -1, -1
	for _, key := range []string{"sourceDigests", "artifactDigests"} {
		keyLine, ok := specChildLine(spec, key)
		if !ok {
			continue
		}
		s, e := blockRange(lines, keyLine, childIndent)
		if start == -1 || s < start {
			start = s
		}
		if e > end {
			end = e
		}
	}
	if start == -1 {
		return 0, 0, false
	}
	return start, end, true
}

// endOfSpecContent returns the line index (0-indexed, exclusive) immediately
// after the last content line of the spec mapping's non-pin children — the
// insertion point for a newly added pin section. When spec has no non-pin
// children it falls back to the line just after the spec key line.
func endOfSpecContent(lines []string, specKey, spec *yaml.Node, childIndent int) int {
	end := 0
	for i := 0; i+1 < len(spec.Content); i += 2 {
		key := spec.Content[i].Value
		if key == "sourceDigests" || key == "artifactDigests" {
			continue
		}
		_, e := blockRange(lines, spec.Content[i].Line, childIndent)
		if e > end {
			end = e
		}
	}
	if end == 0 {
		end = specKey.Line
	}
	return end
}

// blockRange returns the line range [start, end) 0-indexed for the block whose
// key sits on 1-indexed line keyLine at keyIndent spaces of indentation. The
// block runs from the key line through the last following line indented deeper
// than keyIndent; interleaved blank lines (as occur inside a literal or folded
// scalar) are spanned, but blank lines trailing the last deeper-indented line
// are excluded so a following separator is not swallowed.
func blockRange(lines []string, keyLine, keyIndent int) (int, int) {
	start := keyLine - 1
	lastContent := start
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		if indentOf(lines[i]) > keyIndent {
			lastContent = i
			continue
		}
		break
	}
	return start, lastContent + 1
}

// indentOf counts the leading spaces of a line. YAML forbids tabs for
// indentation, so a space count is the block's indent depth.
func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
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
		body, err := entry.Text()
		if err != nil {
			return nil, fmt.Errorf("recipepin: read artifact %q import %s: %w", name, entry.Import, err)
		}
		pins = append(pins, types.ArtifactDigest{Name: name, Digest: digest.ContentDigest([]byte(body))})
	}
	sortArtifactPins(pins)
	return pins, nil
}

// MatchSourcePin returns the recipe-carried source pin for the identity
// {kind, url, chart, version}, and whether one is present.
func MatchSourcePin(pins []types.SourceDigest, kind, url, chart, version string) (types.SourceDigest, bool) {
	for _, p := range pins {
		if p.Kind == kind && p.URL == url && p.Chart == chart && p.Version == version {
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
		if p[i].Chart != p[j].Chart {
			return p[i].Chart < p[j].Chart
		}
		return p[i].Version < p[j].Version
	})
}

func sortArtifactPins(p []types.ArtifactDigest) {
	sort.Slice(p, func(i, j int) bool { return p[i].Name < p[j].Name })
}

func pinsEqual(a, b []types.SourceDigest) bool           { return reflect.DeepEqual(a, b) }
func artifactPinsEqual(a, b []types.ArtifactDigest) bool { return reflect.DeepEqual(a, b) }

// specMapping returns the document's top-level spec key node and its mapping
// value node, or (nil, nil) when the document is not a mapping or has no spec
// mapping. The key node carries the spec line and column the splice uses to
// locate and indent the pin blocks.
func specMapping(root *yaml.Node) (*yaml.Node, *yaml.Node) {
	n := root
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil, nil
		}
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == "spec" && n.Content[i+1].Kind == yaml.MappingNode {
			return n.Content[i], n.Content[i+1]
		}
	}
	return nil, nil
}

// specChildLine returns the 1-indexed line of the spec child key, and whether
// the key is present.
func specChildLine(spec *yaml.Node, key string) (int, bool) {
	for i := 0; i+1 < len(spec.Content); i += 2 {
		if spec.Content[i].Value == key {
			return spec.Content[i].Line, true
		}
	}
	return 0, false
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
