// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package tfimport

// The mapping table's generated rows are read from the terraform Recipes of
// the aggrega-hub checkout beside this repository, and from the Components,
// the libraries and the pass-through module they read; a test in
// ../cloudimport/cloudtable fails while the generated table differs from what
// a regeneration would write.
//go:generate go run ../cloudimport/cloudtable -format terraform -hub ../../../../aggrega-hub -out table_gen.go
