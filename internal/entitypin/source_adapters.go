// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package entitypin

// Blank imports link the shipped source-resolver adapters into every program that
// links SourceResolver. Each adapter registers itself with the package-level
// default dispatcher in its own init(), and SourceResolver is the one production
// caller of that dispatcher, so the adapters are wired beside it: the pin step and
// the dependency resolution that pins or checks pins reach a source only through
// this package, and so the command line, the language server and the assistant
// tools dispatch every remote source to the same adapter set. Without these
// imports a program reaching SourceResolver would answer every remote source "no
// source adapter is registered in this program for the source kind". Keeping them
// in one explicit file makes the registered adapter set auditable in a single
// place, and source_adapters_test.go holds it to the served set pkg/sourcekind
// declares.
//
// One import of the helm package covers both helm adapters (helm-oci-chart and
// helm-https-chart register in the same package's init functions), and one import
// of the ociartifact package covers every OCI module kind, which it registers one
// adapter per kind for.
import (
	_ "github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/compose"
	_ "github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/gitrepo"
	_ "github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/helm"
	_ "github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/ociartifact"
)
