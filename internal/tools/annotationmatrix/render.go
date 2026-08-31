// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The three cell marks. The page's curated legend defines each by name, so they
// are part of the contract between these regions and the prose around them.
//
// The vocabulary names the placement mechanism — whether the provider applies
// the key when it composes this path's output — and deliberately says nothing
// about where else the authored value travels. The value reaches the node's
// render context whatever a cell says, so a mark phrased as the value shaping no
// output at all would be a stronger claim than the declarations support.
const (
	// applied says the provider applies the key on this path and the rendered
	// output changes as a result.
	applied = "applied"
	// notApplied says no provider applies the key here: the target has no render
	// slot for it on this path, so the placement mechanism contributes nothing to
	// what is rendered.
	notApplied = "—"
	// notRendered says the provider does not render this input kind at all, so
	// there is no path for the key to be applied on or missed on.
	notRendered = "n/a"
)

// inputKindOrder fixes the row order within a provider and the vocabulary the
// grid's rows are named by. It is the order the input kinds are declared in on
// the render-behavior contract, so the page reads in the same order as the
// declaration it projects.
var inputKindOrder = []struct {
	// scope is the value a declaration writes.
	scope string
	// label is the word the published row carries. It is the input kind's
	// authored name except for the standalone case, which the page names as an
	// author encounters it — a Recipe whose whole output is its artifacts.
	label string
	// claimed reads the provider's own claim for this kind.
	claimed func(types.PluginInputKinds) bool
}{
	{types.WellKnownAnnotationScopeSourced, "sourced", func(k types.PluginInputKinds) bool { return k.Sourced }},
	{types.WellKnownAnnotationScopeSynthesized, "synthesized", func(k types.PluginInputKinds) bool { return k.Synthesized }},
	{types.WellKnownAnnotationScopeArtifactsStandalone, "artifacts-only", func(k types.PluginInputKinds) bool { return k.ArtifactsStandalone }},
}

// renderNodeScopedRegion renders the per-input-kind grid: one row per provider
// per input kind, one column per node-scoped key any provider declares.
//
// The column set is derived rather than fixed, so a key gaining or losing a
// node-scoped declaration moves the grid rather than needing the header edited
// to match. Column order is first-declaration order across providers in manifest
// order, which is author order twice over and never map iteration.
func renderNodeScopedRegion(providers []provider) (string, error) {
	columns := nodeScopedKeys(providers)

	var b strings.Builder
	b.WriteString("| Provider | Input kind |")
	for _, key := range columns {
		fmt.Fprintf(&b, " `%s` |", shortKey(key))
	}
	b.WriteString("\n| --- | --- |")
	for range columns {
		b.WriteString(" --- |")
	}
	b.WriteString("\n")

	for _, p := range providers {
		if strings.Contains(p.Name, "|") {
			return "", fmt.Errorf("provider %q contains a table separator, which no escaping renders correctly inside a cell", p.Name)
		}
		for _, kind := range inputKindOrder {
			fmt.Fprintf(&b, "| `%s` | %s |", p.Name, kind.label)
			for _, key := range columns {
				fmt.Fprintf(&b, " %s |", cell(p, kind.claimed(p.InputKinds), kind.scope, key))
			}
			b.WriteString("\n")
		}
	}

	// The scope note is generated rather than curated because it describes what
	// the rows above cover, and a hand-written scope claim beside a generated
	// list is the drift these regions exist to remove.
	b.WriteString("\n*Every provider distributed with the compiler has rows here, including one that reads no ")
	b.WriteString("placement annotation at all — an empty row set would be indistinguishable from a provider ")
	b.WriteString("nobody has described yet. A provider supplied separately declares what it reads in the same ")
	b.WriteString("way and is not listed here, because a published page cannot enumerate providers it has never seen.*")

	return b.String(), nil
}

// cell renders one grid cell from the provider's two claims.
func cell(p provider, kindClaimed bool, scope, key string) string {
	if !kindClaimed {
		return notRendered
	}
	if declaresKeyAt(p, key, scope) {
		return applied
	}
	return notApplied
}

// renderBoundaryScopedRegion renders the keys that sit outside the grid: those a
// provider resolves once per boundary, for which input kind is not an axis.
//
// Giving each of these a row per input kind would repeat one cell three times
// and encode a distinction that does not exist, which is why they are a table of
// their own rather than three more grid rows.
func renderBoundaryScopedRegion(providers []provider) (string, error) {
	var b strings.Builder
	b.WriteString("| Annotation | Applied by | Purpose | Default |\n")
	b.WriteString("| --- | --- | --- | --- |\n")

	rows := 0
	for _, p := range providers {
		for _, a := range p.Annotations {
			if !hasScope(a, types.WellKnownAnnotationScopeBoundary) {
				continue
			}
			if strings.ContainsAny(a.Key+a.Purpose+a.DefaultBehavior+p.Name, "|") {
				return "", fmt.Errorf("provider %q key %q carries a table separator, which no escaping renders correctly inside a cell", p.Name, a.Key)
			}
			fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s |\n", a.Key, p.Name, a.Purpose, dashIfEmpty(a.DefaultBehavior))
			rows++
		}
	}
	if rows == 0 {
		// An empty table would read as "no key is resolved per boundary", which
		// is a claim about the product rather than about the extraction. Refuse
		// instead: the shipped set has always carried at least one.
		return "", fmt.Errorf("no provider declares a boundary-scoped annotation; the declarations or the scope vocabulary moved")
	}
	// The splice puts the end marker on its own line, so a trailing newline here
	// would leave a blank line inside the region and the region would not be
	// byte-stable against a hand-formatted page.
	return strings.TrimRight(b.String(), "\n"), nil
}

// nodeScopedKeys returns every key declared at an input-kind scope by any
// provider, in first-declaration order across providers in manifest order.
func nodeScopedKeys(providers []provider) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range providers {
		for _, a := range p.Annotations {
			if seen[a.Key] || !hasInputKindScope(a) {
				continue
			}
			seen[a.Key] = true
			out = append(out, a.Key)
		}
	}
	return out
}

// declaresKeyAt reports whether the provider declares key at the given scope.
func declaresKeyAt(p provider, key, scope string) bool {
	for _, a := range p.Annotations {
		if a.Key == key && hasScope(a, scope) {
			return true
		}
	}
	return false
}

// hasScope reports whether an entry declares the given scope.
func hasScope(a types.WellKnownAnnotation, scope string) bool {
	for _, s := range a.Scope {
		if s == scope {
			return true
		}
	}
	return false
}

// hasInputKindScope reports whether an entry declares any of the three
// input-kind scopes, which is what puts a key in the grid rather than in the
// boundary table.
func hasInputKindScope(a types.WellKnownAnnotation) bool {
	for _, kind := range inputKindOrder {
		if hasScope(a, kind.scope) {
			return true
		}
	}
	return false
}

// shortKey drops the reserved prefix for a column header, where repeating it on
// every column would push the grid past a readable width. The prefix is stated
// once in the prose above the grid.
func shortKey(key string) string {
	if _, rest, ok := strings.Cut(key, "/"); ok {
		return rest
	}
	return key
}

// dashIfEmpty renders an em dash for an entry that declares no absence
// behaviour, so the column never carries an empty cell a reader would read as a
// rendering fault.
func dashIfEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// renderList prints the assembled claims as tab-separated records, for reading
// what the providers declare without touching the page.
func renderList(providers []provider) string {
	var b strings.Builder
	b.WriteString("provider\tsourced\tsynthesized\tartifacts_standalone\tkey\tscope\n")
	for _, p := range providers {
		k := p.InputKinds
		if len(p.Annotations) == 0 {
			fmt.Fprintf(&b, "%s\t%t\t%t\t%t\t-\t-\n", p.Name, k.Sourced, k.Synthesized, k.ArtifactsStandalone)
			continue
		}
		for _, a := range p.Annotations {
			fmt.Fprintf(&b, "%s\t%t\t%t\t%t\t%s\t%s\n",
				p.Name, k.Sourced, k.Synthesized, k.ArtifactsStandalone, a.Key, strings.Join(a.Scope, ","))
		}
	}
	return b.String()
}
