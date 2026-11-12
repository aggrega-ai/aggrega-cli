// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasm

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
)

// HostExport is one export the host calls a guest with a payload: a name some
// plugin type's host-export list holds in the plugin-type registry
// ([plugintype.Entry.HostExports]).
//
// [InvokeExport] and [InvokeExportStatus] accept an export only as a
// HostExport, and the only values that exist are the identifiers generated from
// those lists, each built from the list's own element. The type's one field is
// unexported and nothing outside this package converts a string into one, so a
// socket cannot call an export no list holds: a string where a HostExport is
// expected does not compile. A new export the host is to call is added to its
// plugin type's list, which is what generates its identifier.
//
// An export authored content names is the one exception, because no list can
// hold a name an author chose: see [InvokeAuthoredExport] and
// [InvokeNamedExportStatus].
type HostExport struct {
	name string
}

// Name returns the export's ABI name, the symbol the guest exports.
func (e HostExport) Name() string { return e.name }

// String returns the export's ABI name, so a HostExport formats as the name it
// carries in a message.
func (e HostExport) String() string { return e.name }

// listedExport returns the HostExport for name from plugin type t's host-export
// list, built from the list's element rather than from name. It is the only
// constructor of a HostExport, and the generated identifiers are its only
// callers. It panics when t is not in the registry or its list does not hold
// name: the identifiers are package variables, so a list that dropped a name the
// generated file still declares stops every program using this package at
// start-up, before any guest runs, rather than letting it call an export the
// list no longer holds.
func listedExport(t plugintype.Type, name string) HostExport {
	entry, ok := plugintype.Lookup(t)
	if !ok {
		panic(fmt.Sprintf("wasm: no plugin-type registry entry for %q, so its host export %q has no list to come from", t, name))
	}
	for _, listed := range entry.HostExports {
		if listed == name {
			return HostExport{name: listed}
		}
	}
	panic(fmt.Sprintf("wasm: the %s plugin type's host-export list does not hold %q; regenerate the host-export identifiers from the plugin-type registry", t, name))
}

// AuthoredExportName returns the name of the export [InvokeAuthoredExport]
// calls for the same arguments: function when authored content names one, else
// the listed default. A socket names the export it dispatched with it — in a
// decode, a failure, a message — so that name and the call cannot disagree.
func AuthoredExportName(def HostExport, function string) string {
	if function != "" {
		return function
	}
	return def.name
}
