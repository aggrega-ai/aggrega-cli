// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package arm is the Azure Resource Manager format of the schema drafting core:
// it reads a Bicep type file, a types.json from Azure/bicep-types-az, and drafts
// an arm Recipe declaring one resource of each type it is asked for, as a
// fragment the arm provider merges into its boundary's template.
package arm
