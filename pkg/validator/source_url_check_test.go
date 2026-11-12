// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validator

import (
	"errors"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcekind"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/netrc"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// sourceURLUser and sourceURLSecret are the user and password the refused rows
// write into their URLs. No refusal may repeat either.
const (
	sourceURLUser   = "deploy-bot"
	sourceURLSecret = "s3cr3t-not-in-any-refusal"
)

// netrcAdvice and noNetrcAdvice open the two pieces of advice the rule gives: the
// netrc file for a URL fetched over http or https, and nothing but the removal
// for any other transport, which reads no netrc file.
const (
	netrcAdvice   = "write the credential in the netrc file instead"
	noNetrcAdvice = "remove it from the URL, which is written into the lockfile"
)

// sourceURLRows are the authored sources the source-url rule is held to, one fault
// per row, with the credential-free forms and an SSH user as the controls.
var sourceURLRows = []struct {
	name   string
	source any
	// refused is whether the rule refuses the source; host, key and advice are
	// what the refusal names and says when it does.
	refused           bool
	host, key, advice string
}{
	{name: "a structured https url with no credential (control)",
		source: map[string]any{"kind": sourcekind.HelmHTTPSChart, "url": "https://charts.example.com/repo", "chart": "c", "version": "1.0.0"}},
	{name: "a scalar https URI with no credential (control)", source: "https://charts.example.com/c-1.0.0.tgz"},
	{name: "a Git repository over SSH naming its user (control)",
		source: map[string]any{"kind": sourcekind.GitRepository, "url": "ssh://" + sourceURLUser + "@git.example.com/org/repo.git", "ref": "v1"}},
	{name: "a token in the query string is not read (control)",
		source: map[string]any{"kind": sourcekind.ComposeHTTPSFragment, "url": "https://raw.example.com/web.yaml?token=abc", "version": "1"}},
	{name: "a source with no url (control)", source: map[string]any{"kind": sourcekind.HelmOCIChart, "chart": "c"}},

	{name: "a user and password in a helm chart repository url",
		source:  map[string]any{"kind": sourcekind.HelmHTTPSChart, "url": "https://" + sourceURLUser + ":" + sourceURLSecret + "@charts.example.com/repo", "chart": "c", "version": "1.0.0"},
		refused: true, host: "charts.example.com", key: "spec.source.url", advice: netrcAdvice},
	{name: "a token written as the user of a compose fragment url",
		source:  map[string]any{"kind": sourcekind.ComposeHTTPSFragment, "url": "http://" + sourceURLSecret + "@raw.example.com:8080/web.yaml", "version": "1"},
		refused: true, host: "raw.example.com", key: "spec.source.url", advice: netrcAdvice},
	{name: "a user and password in a Git repository url over https",
		source:  map[string]any{"kind": sourcekind.GitRepository, "url": "https://" + sourceURLUser + ":" + sourceURLSecret + "@git.example.com/org/repo.git", "ref": "v1"},
		refused: true, host: "git.example.com", key: "spec.source.url", advice: netrcAdvice},
	{name: "a password in a Git repository url over SSH",
		source:  map[string]any{"kind": sourcekind.GitRepository, "url": "ssh://" + sourceURLUser + ":" + sourceURLSecret + "@git.example.com/org/repo.git", "ref": "v1"},
		refused: true, host: "git.example.com", key: "spec.source.url", advice: noNetrcAdvice},
	{name: "a user and password in a scalar URI",
		source:  "https://" + sourceURLUser + ":" + sourceURLSecret + "@charts.example.com/c-1.0.0.tgz",
		refused: true, host: "charts.example.com", key: "spec.source", advice: netrcAdvice},
}

// TestValidateRecipe_RefusesASourceURLCarryingACredential drives the rule through
// the Recipe's load-time entry, the one every load and the single-entity check
// reach.
//
// INVARIANT: a source whose URL carries a credential before its host is refused
// under the source-url condition, naming the entity, the key and the host, saying
// where the credential goes, and repeating neither the user nor the password;
// every other source passes the rule.
// CONSEQUENCE: were a row to pass, the credential would be sent, and written into
// the lockfile, the lock beside the entity, the source cache and every
// diagnostic naming the source.
//
// A planted defect makes this fail: have the rule's predicate answer no
// credential for every URL.
func TestValidateRecipe_RefusesASourceURLCarryingACredential(t *testing.T) {
	t.Parallel()
	for _, tc := range sourceURLRows {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := &types.Recipe{}
			r.Metadata.Name = "svc"
			r.Metadata.Version = "1.0.0"
			r.Spec.Capability = "helm"
			r.Spec.Implements = "test/svc"
			r.Spec.Source = tc.source
			err := ValidateRecipe(r, "recipe.yaml")
			assertSourceURLOutcome(t, err, tc.refused, "Recipe svc:1.0.0", tc.key, tc.host, tc.advice)
		})
	}
}

// TestValidateFeatureRecipe_RefusesASourceURLCarryingACredential is the
// FeatureRecipe's leg of the same rule, through the FeatureRecipe's load-time
// entry.
func TestValidateFeatureRecipe_RefusesASourceURLCarryingACredential(t *testing.T) {
	t.Parallel()
	for _, tc := range sourceURLRows {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fr := &types.FeatureRecipe{}
			fr.Metadata.Name = "cred"
			fr.Metadata.Version = "1.0.0"
			fr.Spec.Capability = "helm"
			fr.Spec.Implements = "test/cred"
			fr.Spec.Source = tc.source
			err := ValidateFeatureRecipe(fr, "featurerecipe.yaml")
			assertSourceURLOutcome(t, err, tc.refused, "FeatureRecipe cred:1.0.0", tc.key, tc.host, tc.advice)
		})
	}
}

func assertSourceURLOutcome(t *testing.T, err error, refused bool, entity, key, host, advice string) {
	t.Helper()
	if !refused {
		if errors.Is(err, netrc.ErrSourceURLCarriesCredential()) {
			t.Fatalf("a source carrying no credential was refused: %v", err)
		}
		return
	}
	if !errors.Is(err, netrc.ErrSourceURLCarriesCredential()) {
		t.Fatalf("want the source-url condition; got %v", err)
	}
	if code, _ := cerrors.ShortCodeOf(err); code != "E0361" {
		t.Errorf("the refusal carries short code %q; want E0361", code)
	}
	msg := err.Error()
	for _, want := range []string{entity, "rule source-url", key + " names", "host " + host, advice} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not say %q: %q", want, msg)
		}
	}
	for _, secret := range []string{sourceURLSecret, sourceURLUser} {
		if strings.Contains(msg, secret) {
			t.Errorf("the refusal repeats %q from the URL: %q", secret, msg)
		}
	}
}
