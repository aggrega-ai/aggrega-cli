// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"strings"
	"testing"
	"text/template"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
	"go.yaml.in/yaml/v3"
)

// storedLocator is the ARN of a Secrets Manager secret a source names.
const storedLocator = "arn:aws:secretsmanager:eu-west-1:123456789012:secret:db-admin-AbCdEf"

// storedText is the dynamic reference reading that secret, as the source
// writes it.
const storedText = "{{resolve:secretsmanager:" + storedLocator + ":SecretString:password}}"

// storedSecret is the secret a secret store holds at storedLocator.
func storedSecret() Stored {
	return Stored{Store: "secretsmanager", Locator: storedLocator, Text: storedText}
}

// TestStoredAtASlotIsAReferencedSecret attaches a secret a store holds, placed
// in a credential slot, as the kit's referenced secret whose origin is the
// store's locator: on cloudformation a core/referenced-secret, which the
// drafted Manifest maps to the realization the kit's form names, and to none
// where the form names none. Its text is written nowhere.
func TestStoredAtASlotIsAReferencedSecret(t *testing.T) {
	in := edgeView()
	in.Target = "cloudformation"
	g := &Graph{Nodes: []*Node{mappedServer(leaf(storedSecret(), pathPassword...))}}
	_, aggregate, release, manifest, diags := released(t, in, releaseFormat(), g)
	wantBlock(t, release, "server", `    server:
      infra:
        location: "westeurope"
      features:
        admin_password:
          type: core/referenced-secret
          infra:
            # Attached by reference, never as a value: the secret the secretsmanager store holds, at the locator the source states.
            origin: "`+storedLocator+`"
`)
	form := importer.Secret{Referenced: true, Origin: storedLocator}.Form("cloudformation")
	if form.Type != "core/referenced-secret" {
		t.Fatalf("the kit's form of the secret on cloudformation is %+v, want core/referenced-secret", form)
	}
	switch {
	case form.Realization == "" && strings.Contains(manifest, "core/referenced-secret"):
		t.Errorf("the kit's form names no realization, and the Manifest maps one:\n%s", manifest)
	case form.Realization != "" && !strings.Contains(manifest, "core/referenced-secret: "+form.Realization):
		t.Errorf("the Manifest does not map the secret's type to %s:\n%s", form.Realization, manifest)
	}
	if strings.Contains(aggregate+release, storedText) {
		t.Errorf("a draft writes the secret's text:\n%s\n%s", aggregate, release)
	}
	onlyCode(t, diags, CodeValueNotCarried, 0)
	onlyCode(t, diags, CodeCredentialFlow, 0)
}

// TestStoredElsewhereIsDeployTimeText writes a secret a store holds, read
// anywhere but a credential slot, as the text the source writes for it where
// the import drafts for the format's own target, which resolves it when the
// source deploys: whole in the Release, and in an Aggregate value escaped so it
// reads back as written once the Aggregate is evaluated as a template. On any
// other target the Release leaves the key, its comment naming the store and the
// locator, and the Aggregate does not write the value.
func TestStoredElsewhereIsDeployTimeText(t *testing.T) {
	f := releaseFormat()
	f.Target, f.Text = "cloudformation", nil
	graph := func() *Graph {
		s := pgServer()
		s.Body = merged(leaf(text("westeurope"), pathLocation...), leaf(storedSecret(), pathVersion...),
			leaf(storedSecret(), pathSkuName...))
		return &Graph{Nodes: []*Node{s}}
	}

	t.Run("on the format's own target", func(t *testing.T) {
		in := edgeView()
		in.Target = "cloudformation"
		_, aggregate, release, _, diags := released(t, in, f, graph())
		wantBlock(t, release, "server", `    server:
      infra:
        location: "westeurope"
        sku: "`+storedText+`"
`)
		onlyCode(t, diags, CodeValueNotCarried, 0)

		tmpl, err := template.New("aggregate").Parse(aggregate)
		if err != nil {
			t.Fatalf("the drafted Aggregate does not parse as a template: %v\n%s", err, aggregate)
		}
		var evaluated strings.Builder
		if err := tmpl.Execute(&evaluated, nil); err != nil {
			t.Fatalf("the drafted Aggregate does not run as a template: %v\n%s", err, aggregate)
		}
		var doc struct {
			Spec struct {
				Components map[string]struct {
					Values map[string]any `yaml:"values"`
				} `yaml:"components"`
			} `yaml:"spec"`
		}
		if err := yaml.Unmarshal([]byte(evaluated.String()), &doc); err != nil {
			t.Fatalf("the evaluated Aggregate does not read as YAML: %v\n%s", err, evaluated.String())
		}
		if got := doc.Spec.Components["server"].Values["version"]; got != storedText {
			t.Errorf("values.version reads back as %q, want %q:\n%s", got, storedText, aggregate)
		}
	})

	t.Run("on another target", func(t *testing.T) {
		in := edgeView()
		in.Target = "compose"
		_, aggregate, release, _, diags := released(t, in, f, graph())
		block := instanceBlock(t, release, "server")
		for _, want := range []string{"infra.sku, the secret the secretsmanager store holds at " + storedLocator,
			"        sku: \"\"\n"} {
			if !strings.Contains(block, want) {
				t.Errorf("the entry lacks %q:\n%s", want, block)
			}
		}
		if strings.Contains(aggregate+release, storedText) {
			t.Errorf("a draft writes the secret's text on another target:\n%s\n%s", aggregate, release)
		}
		warnsAbout(t, diags, CodeValueNotCarried, "server", "values.version", "secretsmanager", "compose")
		for _, d := range diags {
			if strings.Contains(d.Message, storedLocator) {
				t.Errorf("a diagnostic quotes the locator: %+v", d)
			}
		}
	})
}
