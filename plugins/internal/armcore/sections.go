// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

// The top-level keys of an ARM template. Each is the key an author writes, so a
// message naming one names something the author can search their own files for.
const (
	sectionSchema          = "$schema"
	sectionLanguageVersion = "languageVersion"
	sectionContentVersion  = "contentVersion"
	sectionAPIProfile      = "apiProfile"
	sectionDefinitions     = "definitions"
	sectionParameters      = "parameters"
	sectionVariables       = "variables"
	sectionFunctions       = "functions"
	sectionResources       = "resources"
	sectionOutputs         = "outputs"
	sectionMetadata        = "metadata"
)

// disposition is what becomes of a top-level key a fragment writes.
type disposition int

const (
	// dispositionScope: the key is read for the scope the content is written for
	// and stamped again per level.
	dispositionScope disposition = iota
	// dispositionStampedChecked: the assembler stamps the key itself; a fragment's
	// own value must be absent or the stamped one, else it is refused.
	dispositionStampedChecked
	// dispositionStampedReported: the assembler stamps the key itself; a
	// fragment's own value is reported and not carried.
	dispositionStampedReported
	// dispositionRefused: a fragment writing the key is refused by name.
	dispositionRefused
	// dispositionMergedBySymbol: the entries are merged by symbolic name and read
	// verbatim.
	dispositionMergedBySymbol
	// dispositionCarried: the entries are carried under the names written.
	dispositionCarried
	// dispositionCarriedByMember: the entries are carried under the name of their
	// namespace and member.
	dispositionCarriedByMember
	// dispositionScopedByNode: the entries are carried under names scoped to the
	// node that contributes them.
	dispositionScopedByNode
)

// topLevelSection is one top-level key and its disposition.
type topLevelSection struct {
	key         string
	disposition disposition
}

// topLevelSections accounts for every top-level key an ARM template admits, in
// the order the template syntax documents them, metadata last. Each key has
// exactly one disposition, so a key is never dropped without being said; a key a
// fragment writes that is not listed is reported and not carried. The list
// cannot confirm itself complete — the template language is not ours — so it is
// written in the language's own order to be read against its documentation.
var topLevelSections = []topLevelSection{
	{sectionSchema, dispositionScope},
	{sectionLanguageVersion, dispositionStampedChecked},
	{sectionContentVersion, dispositionStampedReported},
	{sectionAPIProfile, dispositionRefused},
	{sectionDefinitions, dispositionCarried},
	{sectionParameters, dispositionCarried},
	{sectionVariables, dispositionCarried},
	{sectionFunctions, dispositionCarriedByMember},
	{sectionResources, dispositionMergedBySymbol},
	{sectionOutputs, dispositionScopedByNode},
	{sectionMetadata, dispositionCarried},
}

// dispositionOf returns the disposition of a top-level key and whether the key
// is one an ARM template admits.
func dispositionOf(key string) (disposition, bool) {
	for _, s := range topLevelSections {
		if s.key == key {
			return s.disposition, true
		}
	}
	return 0, false
}
