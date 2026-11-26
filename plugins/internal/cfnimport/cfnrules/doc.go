// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package cfnrules holds the two rules by which a CloudFormation resource is
// read as part of another rather than as a resource of its own, read alike by
// the CloudFormation importer, which reads a template's resources, and by the
// generator of its mapping table, which reads the hub's cloudformation
// Recipes.
//
// An inline rule names a parent's property that references, with Ref, a child
// the parent owns: the child's Properties are read in place of the reference,
// beneath that property, so the parent's row places them as its own. A parent
// link names a child's property that references its parent, with Ref or with
// GetAtt: the child is read as the parent's, and the parent's row folds it.
package cfnrules
