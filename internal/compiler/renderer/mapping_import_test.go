// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"errors"
	"sort"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// mappingLibraryFixture builds a MappingLibrary from entry-name to template-body
// pairs, inserting in sorted-name order for stable iteration.
func mappingLibraryFixture(name string, entries map[string]string) types.MappingLibrary {
	ml := types.MappingLibrary{
		APIVersion: "aggrega.ai/v1beta1",
		Kind:       "MappingLibrary",
		Metadata:   types.MappingLibraryMetadata{Name: name, Version: "1.0.0"},
	}
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ml.Spec.Mappings.Set(k, types.MappingEntry{Body: entries[k]})
	}
	return ml
}

// TestValuesMappingImport_RecursiveTargetCarryingHostHalts pins that a host
// written in a library entry's own body, which no entity schema describes, is
// refused when it arrives on the recursive hop of a Recipe's own valuesMapping
// import. The target library is present and resolvable, so accepting the host
// would produce its fragment rather than a different failure.
func TestValuesMappingImport_RecursiveTargetCarryingHostHalts(t *testing.T) {
	t.Parallel()
	libs := map[string]types.MappingLibrary{
		"Helpers": mappingLibraryFixture("Helpers", map[string]string{"Core": "$import: oci://hub.example.test/Base:1.0.0#/mappings/Real"}),
		"Base":    mappingLibraryFixture("Base", map[string]string{"Real": `{"final": true}`}),
	}
	if _, err := resolveValuesMappingText(types.ValuesMapping{Import: "Helpers#/mappings/Core"}, "recursive-host", "Recipe", libs); !errors.Is(err, ErrHostInLibraryImport()) {
		t.Errorf("error = %v; want ErrHostInLibraryImport through the valuesMapping surface", err)
	}
}
