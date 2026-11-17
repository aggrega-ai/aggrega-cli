// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validator

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/sourceref"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcekind"
)

// ociModuleSourceFaults reads an OCI module source's coordinates and returns one
// piece of advice per fault, in a fixed order: the url, the version, then the keys
// the kind has no use for (`chart`, `ref`, and `module_subdir` where the kind takes
// none). A source naming exactly one artifact returns none.
//
// It is the one reader of those coordinates for both authored kinds that write a
// source. The grammar of the url and of the version is pkg/oci's; each piece of
// advice opens with what was written and names what to write.
//
// A version that still carries template text is not judged: it names nothing until
// it is evaluated, and the pull judges what it evaluates to.
func ociModuleSourceFaults(url string, takesModuleSubdir bool, field func(key string) (string, bool)) []string {
	var advice []string
	if err := oci.RepositoryOnly(url); err != nil {
		advice = append(advice, fmt.Sprintf("the source writes url: %q, which %s; write url: "+
			"oci://<registry>/<repository> and put the tag or digest under version:", url, err))
	}
	version, written := field("version")
	switch {
	case !written || version == "":
		advice = append(advice, "the source writes no version; write version: <tag or sha256 manifest digest>")
	case !sourceref.IsTemplated(version):
		if err := oci.TagOrDigest(version); err != nil {
			advice = append(advice, fmt.Sprintf("the source writes version: %q, which %s; "+
				"write version: <tag or sha256 manifest digest>", version, err))
		}
	}
	if _, written := field("chart"); written {
		advice = append(advice, "the source writes chart:, which an OCI module has no use for; remove chart:")
	}
	if _, written := field("ref"); written {
		advice = append(advice, "the source writes ref:, which an OCI module has no use for; "+
			"remove ref: and name the artifact with version:")
	}
	if _, written := field("module_subdir"); written && !takesModuleSubdir {
		advice = append(advice, "the source writes module_subdir:, which this kind does not take; remove module_subdir:")
	}
	return advice
}

// ociModuleSourceReference is the reference an OCI module source is checked
// against: one written under `url:` that names something to fetch rather than
// something the workspace holds.
func ociModuleSourceReference(reference string, underURL bool) (string, bool) {
	return reference, underURL && !sourceref.IsLocalURL(reference)
}

// recipeOCIModuleSourceNamesOneArtifact refuses a Recipe whose source of an OCI
// module kind does not name exactly one artifact. It is asked as the document is
// read, before anything is fetched, so every surface that reads the Recipe reports
// it ahead of a pull. It reads only what the author wrote and the kinds' one
// declaration, and raises one objection per fault.
var recipeOCIModuleSourceNamesOneArtifact = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: sourcekind.ErrOCIModuleSourceMalformed(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentSubject,
	) []constraint.Objection {
		kind, written := v.RecipeAuthoredSourceKind(s.SourceFile())
		if !written {
			return nil
		}
		takesModuleSubdir, isModuleKind := v.RecipeSourceKindOciModule(kind)
		if !isModuleKind {
			return nil
		}
		url, applies := ociModuleSourceReference(v.RecipeAuthoredSourceReference(s.SourceFile()))
		if !applies {
			return nil
		}
		field := func(key string) (string, bool) { return v.RecipeAuthoredSourceField(s.SourceFile(), key) }
		return raiseOCIModuleSourceFaults(s, ociModuleSourceFaults(url, takesModuleSubdir, field))
	},
}

// featureRecipeOCIModuleSourceNamesOneArtifact is the FeatureRecipe's rule over its
// OCI module source: the Recipe's rule, read through the FeatureRecipe's own facts,
// because a fact is filed under one document kind.
var featureRecipeOCIModuleSourceNamesOneArtifact = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: sourcekind.ErrOCIModuleSourceMalformed(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentSubject,
	) []constraint.Objection {
		kind, written := v.FeatureRecipeAuthoredSourceKind(s.SourceFile())
		if !written {
			return nil
		}
		takesModuleSubdir, isModuleKind := v.FeatureRecipeSourceKindOciModule(kind)
		if !isModuleKind {
			return nil
		}
		url, applies := ociModuleSourceReference(v.FeatureRecipeAuthoredSourceReference(s.SourceFile()))
		if !applies {
			return nil
		}
		field := func(key string) (string, bool) { return v.FeatureRecipeAuthoredSourceField(s.SourceFile(), key) }
		return raiseOCIModuleSourceFaults(s, ociModuleSourceFaults(url, takesModuleSubdir, field))
	},
}

// raiseOCIModuleSourceFaults raises one objection for each piece of advice.
func raiseOCIModuleSourceFaults(s constraint.DocumentSubject, advice []string) []constraint.Objection {
	var objections []constraint.Objection
	for _, a := range advice {
		objections = append(objections, s.RaiseWithAdvice(sourcekind.ErrOCIModuleSourceMalformed(), a))
	}
	return objections
}
