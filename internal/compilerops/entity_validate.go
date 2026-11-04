// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"context"

	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// ValidateSingleEntity runs the single-entity check over the entity file at path
// (validateops.ValidateEntity) and returns its failure with the account of the
// catalog files the check's catalog load refused, where one of them explains it.
//
// Every surface that checks one entity calls this or ValidateSingleEntityContent
// rather than the check directly, so a terminal, an assistant tool and an editor
// connection report a reference that resolved to nothing because its document
// was refused in the same words, and in the words a build reports it in. The
// check hands its refusals back raw, so a direct call would report that failure
// without naming the file; a test refuses any non-test reference to the check
// from outside this package.
func ValidateSingleEntity(ctx context.Context, wsRoot, cacheDir string, dirs, named []string, path string, suppressUnsupportedFieldWarnings bool, opts ...validateops.EntityOption) (validateops.EntityResult, error) {
	res, err := validateops.ValidateEntity(ctx, wsRoot, cacheDir, dirs, named, path, suppressUnsupportedFieldWarnings, opts...)
	return res, withEntityRefusals(err, res.Refused)
}

// ValidateSingleEntityContent is ValidateSingleEntity over data as the content of
// the entity file at path (validateops.ValidateEntityContent), which is what an
// editor holds before the file is saved.
func ValidateSingleEntityContent(ctx context.Context, wsRoot, cacheDir string, dirs, named []string, path string, data []byte, suppressUnsupportedFieldWarnings bool, opts ...validateops.EntityOption) (validateops.EntityResult, error) {
	res, err := validateops.ValidateEntityContent(ctx, wsRoot, cacheDir, dirs, named, path, data, suppressUnsupportedFieldWarnings, opts...)
	return res, withEntityRefusals(err, res.Refused)
}

// withEntityRefusals annotates a single-entity failure to find what the entity
// refers to with the refusals in refused (AnnotateEntitySkipContext), and returns
// every other failure unchanged.
//
// Only that failure is annotated, for the reason a build annotates only a
// resolution failure: a refused file explains a reference resolving to nothing,
// and a note about refused files beneath any other failure would name bystanders
// as its cause.
func withEntityRefusals(err error, refused []catalog.SkippedEntity) error {
	if _, ok := referenceOf(err); !ok {
		return err
	}
	return AnnotateEntitySkipContext(err, refused)
}
