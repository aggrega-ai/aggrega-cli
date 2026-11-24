// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armimport

import (
	"slices"
	"strings"
	"sync"
)

// AVMModule is one resource module of the Azure Verified Modules programme:
// the telemetry prefix that starts the name of the nested template resource the
// module writes, its module path in the registry, and the provider namespace
// and resource type of the resource it creates.
type AVMModule struct {
	// Prefix is the telemetry prefix, such as 46d3xbcp.res.<name>-<suffix>.
	Prefix string
	// Module is the module's path in the registry, beginning avm/res/.
	Module string
	// Namespace is the provider namespace of the resource the module creates,
	// such as Microsoft.App.
	Namespace string
	// Type is the resource type beneath the namespace, such as containerApps,
	// or service/apis for a child type.
	Type string
}

// ResourceType returns the full type of the resource the module deploys, the
// namespace and the type joined by a slash, as a template writes it.
func (m AVMModule) ResourceType() string { return m.Namespace + "/" + m.Type }

// AVMModuleByPrefix returns the module whose telemetry prefix is prefix, which
// is matched exactly.
func AVMModuleByPrefix(prefix string) (AVMModule, bool) {
	i, found := slices.BinarySearchFunc(avmIndex, prefix, func(m AVMModule, p string) int {
		return strings.Compare(m.Prefix, p)
	})
	if !found {
		return AVMModule{}, false
	}
	return avmIndex[i], true
}

// avmByPath holds the index's position of each module path, built on first use.
var avmByPath = sync.OnceValue(func() map[string]int {
	byPath := make(map[string]int, len(avmIndex))
	for i, m := range avmIndex {
		byPath[m.Module] = i
	}
	return byPath
})

// AVMModuleByPath returns the module whose path in the registry is module,
// such as avm/res/app/container-app, which is matched exactly.
func AVMModuleByPath(module string) (AVMModule, bool) {
	i, ok := avmByPath()[module]
	if !ok {
		return AVMModule{}, false
	}
	return avmIndex[i], true
}
