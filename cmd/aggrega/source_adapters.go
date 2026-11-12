// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// Blank imports wire the shipped source-resolver adapters into the package-level
// default dispatcher. Each adapter self-registers in its own init(), but nothing
// else in the CLI imports these packages — so without this file the production
// binary's dispatcher would have zero adapters and every remote source fetch
// would fail "no source adapter is registered in this program for the source
// kind". Keeping the imports in one explicit file makes the registered adapter set
// auditable in a single place, and source_adapters_test.go holds it to the served
// set pkg/sourcekind declares.
//
// One import of the helm package covers both helm adapters (helm-oci-chart and
// helm-https-chart register in the same package's init functions). The shipped
// kinds are helm-oci-chart, helm-https-chart, compose-https-fragment, and
// git-repository; the reserved terraform-module-oci kind has no adapter yet and
// is intentionally absent.
import (
	_ "github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/compose"
	_ "github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/gitrepo"
	_ "github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/helm"
)
