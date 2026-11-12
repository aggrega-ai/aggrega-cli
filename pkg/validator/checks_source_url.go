// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validator

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/netrc"
)

// sourceURLCredentialAdvice is what an author does about a source URL carrying a
// credential, for the host it names and the key it sits under. It names the host
// and the key and never the credential: the refusal is printed, logged and shown
// in an editor, and a refusal repeating the secret would put it in exactly the
// places the rule keeps it out of.
//
// Where the source is fetched over http or https, the credential belongs in the
// netrc file, which those fetches read for the host they request. Any other
// transport reads none, so its advice says only that the URL carries none.
func sourceURLCredentialAdvice(host, reference string, inURLKey bool) string {
	key := "spec.source"
	if inURLKey {
		key = "spec.source.url"
	}
	if _, readsNetrc := netrc.HostFor(reference); readsNetrc {
		return fmt.Sprintf("%s names a user or password for host %s; remove it from the URL and write the "+
			"credential in the netrc file instead, as an entry \"machine %s login <user> password <secret>\" "+
			"(the file NETRC names, else .netrc in your home directory, or _netrc in %%USERPROFILE%% on Windows)",
			key, host, host)
	}
	return fmt.Sprintf("%s names a password for host %s; remove it from the URL, which is written into the "+
		"lockfile and the cache as authored", key, host)
}

// recipeSourceURLCarriesNoCredential refuses a Recipe whose source URL carries a
// credential of its own — a user, or a user and password, written before the host.
//
// The URL is the text a fetch requests, and it is also the text every record of
// the fetch keeps as authored: the Release's lockfile, the lock beside the entity,
// the source cache, and every diagnostic naming the source. A credential in it
// would be sent, and written into each of those as well as into the document, which
// is committed and may be published. So the rule is asked as the document is read,
// on every surface that reads it, before anything is fetched.
//
// What counts as a credential is decided by netrc.URLCredential, the one answer
// every reader of the question shares. The reading is syntactic: it reads the URL's
// user-information part and never guesses whether a query parameter or a path
// segment is a token.
var recipeSourceURLCarriesNoCredential = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: netrc.ErrSourceURLCarriesCredential(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentSubject,
	) []constraint.Objection {
		reference, inURLKey := v.RecipeAuthoredSourceReference(s.SourceFile())
		host, carries := netrc.URLCredential(reference)
		if !carries {
			return nil
		}
		return []constraint.Objection{
			s.RaiseWithAdvice(netrc.ErrSourceURLCarriesCredential(),
				sourceURLCredentialAdvice(host, reference, inURLKey)),
		}
	},
}

// featureRecipeSourceURLCarriesNoCredential is the FeatureRecipe's rule over its
// source URL: the Recipe's rule, read through the FeatureRecipe's own facts,
// because a fact is filed under one document kind. Why it is asked where it is
// asked is what the Recipe's rule states.
var featureRecipeSourceURLCarriesNoCredential = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: netrc.ErrSourceURLCarriesCredential(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentSubject,
	) []constraint.Objection {
		reference, inURLKey := v.FeatureRecipeAuthoredSourceReference(s.SourceFile())
		host, carries := netrc.URLCredential(reference)
		if !carries {
			return nil
		}
		return []constraint.Objection{
			s.RaiseWithAdvice(netrc.ErrSourceURLCarriesCredential(),
				sourceURLCredentialAdvice(host, reference, inURLKey)),
		}
	},
}
