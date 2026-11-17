// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package schemadraft is the format-neutral core of the importers that draft a
// Component and a Recipe for each resource type a vendor schema describes.
//
// It holds the resource model every schema reader lifts a type into, reads the
// resource_types and target_namespace settings, and selects the types an import
// drafts from the types its sources hold, halting with a diagnostic that names
// what to fix when a selection cannot be made.
package schemadraft
