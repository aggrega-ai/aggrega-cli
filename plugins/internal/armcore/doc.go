// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package armcore holds the Azure Resource Manager knowledge the arm provider
// writes templates with: the file name, the key spaces and validation codes it
// declares, the schema and language versions it stamps, the names it derives for
// outputs, deployments and nested deployments, and the escapes ARM requires in
// the text it writes.
//
// Each derived name and fixed string is defined once here. The package imports
// no wasm-only API, so its behaviour is pinned by tests that run on the host.
package armcore
