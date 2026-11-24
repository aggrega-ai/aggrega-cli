// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package schemadraft is the format-neutral core of the importers that draft a
// Component and a Recipe for each resource type a vendor schema describes, and
// for a resource as a source such as a template writes it.
//
// It holds the resource model every schema reader lifts a type into, reads the
// resource_types and target_namespace settings, and selects the types an import
// drafts from the types its sources hold, halting with a diagnostic that names
// what to fix when a selection cannot be made. A resource a source writes fills
// the same model (FromWritten) and is drafted through the same steps
// (DraftResource), a neighbour's value delivered by property inject.
package schemadraft
