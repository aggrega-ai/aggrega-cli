// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cacheops

import (
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// entitySlotRel is the path of the fixtures' one unreferenced entity slot beneath
// the entity class root, as the one builder of a slot's place (oci.CachePath)
// spells it, so a fixture laying the slot out by hand lays it where every reader
// of the cache looks. The values are well formed by construction, so a refusal is
// a fault in the fixture and panics.
func entitySlotRel() string {
	slot, err := oci.CachePath("", "", "Component", "zz-unreferenced", "1.0.0")
	if err != nil {
		panic("the fixture slot: " + err.Error())
	}
	rel, err := filepath.Rel(oci.EntitiesRoot("", ""), slot)
	if err != nil {
		panic("the fixture slot beneath the entity root: " + err.Error())
	}
	return rel
}
