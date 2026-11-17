// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package sourcekind

import cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"

// ErrOCIModuleSourceMalformed reports a Recipe or FeatureRecipe whose source of an
// OCI module kind does not name exactly one artifact: a `url:` that is not
// `oci://<registry>/<repository>`, a `version:` that is absent or is neither a tag
// nor a `sha256:` manifest digest, a `chart:` or `ref:` the kind has no use for, or
// a `module_subdir:` on a kind that does not take one.
//
// It is refused as the document is read, ahead of any fetch, so every surface that
// reads the document reports it before a pull.
func ErrOCIModuleSourceMalformed() error { return errOCIModuleSourceMalformed }

var errOCIModuleSourceMalformed = cerrors.Register("oci-module-source-malformed", cerrors.Meta{
	ShortCode: "E0379",
	Message:   "the source does not name one artifact of its OCI module kind",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})
