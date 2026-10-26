// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The rule in this file is about how a connection reaches a component a
// sub-module contributes: only through a name that module exports. It is declared
// here because the condition it reports is registered here.
//
// It reads the finalised graph because the question it answers is about a
// connection that reaches a component, and whether it does is the target the
// connection RESOLVED to — the fact the two rules beside it in this package read
// for the same reason. A connection whose target answers to nothing is theirs to
// report, so this one ends silent there and one mistyped name is reported once.
//
// Which module boundaries a connection crosses is read from where its consumer
// sits. A module's own components are its own to wire, so a target inside the
// module holding the consumer is reached freely; a target inside a module nested
// below the consumer's is reached only through that nested module's export list.
// For a connection a document drew this is exactly the document's own scope,
// because a document's connections are drawn from its own components. For one the
// Release wired, it is what lets the Release wire two components of one module to
// each other while still holding it to a module's export list from outside.

// ConnectionTargetIsExported reports each dependency connection that reaches a
// component inside a sub-module by a name the module does not list under
// `exportedComponents`.
//
// The name the connection's target was looked up by is read relative to the
// module holding the consumer. A name of one segment there is a component of that
// module and ends this rule without an objection. A dotted name passes into the
// nested module its first segment names, and is accepted only when what follows
// is one of that module's exported aliases — so a component's id inside the
// module, and a path reaching through the module into one nested inside it, are
// both refused, while an alias the module re-exports from a nested module is
// accepted like any other.
//
// It is invoked once per connection whichever surface drew it, so a connection a
// component's own dependency block wrote, one an attachment projected and one the
// Release wired are all asked.
var ConnectionTargetIsExported = constraint.Check[
	constraint.GraphView,
	constraint.EdgeSubject,
]{
	Sentinel: ErrUsesTargetNotExported(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.GraphView, s constraint.EdgeSubject) []constraint.Objection {
		node, socket, alias := s.NodeID(), s.SocketName(), s.AliasName()
		if _, held := v.FlattenedNodeComponentType(v.PerEdgeResolvedTarget(node, socket, alias)); !held {
			return nil
		}
		addressed := v.PerEdgeAddressedTarget(node, socket, alias)
		scope := enclosingModule(types.QualifiedIDModule(node), addressed)
		relative := addressed
		if scope != "" {
			relative = strings.TrimPrefix(addressed, scope+".")
		}
		module, inside, crosses := types.CutQualifiedID(relative)
		if !crosses {
			return nil
		}
		exports, imported := v.ModuleExportedAliases(types.JoinQualifiedID(scope, module))
		if !imported || slices.Contains(exports, inside) {
			return nil
		}
		// The module is named as the author wrote it: the authored target with the
		// part inside the module removed, which is the module's local name in a
		// document and its id in the composition where the Release wrote it.
		written := v.PerEdgeAuthoredTarget(node, socket, alias)
		published := "nothing"
		if len(exports) > 0 {
			published = strings.Join(exports, ", ")
		}
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrUsesTargetNotExported(),
			"Name the component through one of the module's exported aliases, or list it under the module's `exportedComponents`.",
			constraint.Uncaptioned(written),
			constraint.Captioned("module", strings.TrimSuffix(written, "."+inside)),
			constraint.Captioned("which exports", published),
			constraint.Captioned("wired into socket", socket),
		)}
	},
}

// enclosingModule returns the innermost module, among the consumer's own module
// and the modules enclosing it, that the addressed target sits inside — the scope
// the target is read relative to. It is the empty prefix, the root, where the
// target sits inside none of them.
func enclosingModule(consumerModule, addressed string) string {
	for scope := consumerModule; scope != ""; scope = types.QualifiedIDModule(scope) {
		if addressed != scope && types.IsUnderModulePrefix(addressed, scope) {
			return scope
		}
	}
	return ""
}
