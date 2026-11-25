// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/alphanumeric"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// A secret is attached by reference and never written as a value. A value a
// mapped instance's row places in a credential slot is attached on that slot in
// the form the kit gives the target: a secure parameter the parameters file
// gives as a reference to a secret store is a core/referenced-secret whose
// origin is that secret's locator, one it gives none for takes the form
// Secret.Form gives the import's target, and a credential read from another
// resource is a core/referenced-secret whose origin is left for the Release. A
// literal the source writes there, as a parameter's default or the file's value,
// is a credential the empty_credentials setting judges, named in a warning
// that never shows it. A secret of a resource drafted from the source stays the
// drafted template's deploy-time parameter, and the entry's comment names it.

// credentialWhere completes the report of a credential the setting judges.
const credentialWhere = "in the draft, which attaches the secret by reference and writes no value"

// newGuidCall finds a call of newGuid in deploy-time text.
var newGuidCall = regexp.MustCompile(`(?i)newguid\s*\(`)

// variableOf returns the variable name a secret read from the template
// parameter name is written with: the name itself where it is one, else the
// kit's variable name for it.
func variableOf(name string) string {
	if regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(name) {
		return name
	}
	return "_" + importer.VariableName(name)
}

// judge applies the empty_credentials setting to a literal the source holds for
// the credential field of node, named name, and reports the decision: the value
// itself is not written either way.
func (w *aggregateWriter) judge(name, field string) {
	doc, err := json.Marshal(map[string]string{name: "literal"})
	if err != nil {
		return
	}
	located := []importer.Credential{{Node: name, Field: field, Pointer: importer.Pointer(name),
		Class: importer.CredentialMarked}}
	_, diags, err := importer.CarryCredentials(w.in.Settings, doc, credentialWhere, located)
	if err != nil {
		w.diags = append(w.diags, Report(CodeValueNotCarried, fmt.Sprintf("the literal %s of %s could not be "+
			"judged as a credential, so it is not written: write the secret's origin by hand", field, name)))
		return
	}
	w.diags = append(w.diags, diags...)
}

// literalHeld reports whether v is a literal that holds something to judge.
func literalHeld(v Value) bool {
	l, ok := v.(Literal)
	if !ok {
		return false
	}
	switch strings.TrimSpace(string(l.JSON)) {
	case `""`, "null", "":
		return false
	}
	return true
}

// paramSecret returns the secret the slot of an instance takes from the
// template parameter p, and judges what the template or the parameters file
// holds as its value.
func (w *aggregateWriter) paramSecret(path string, p *Parameter) importer.Secret {
	s := importer.Secret{Path: path, Variable: variableOf(p.Name),
		Text: "the template parameter " + draftyaml.Comment(p.Name)}
	w.noteSecretValue(p)
	if p.StoreRef != "" {
		s.Referenced, s.Origin = true, p.StoreRef
	}
	return s
}

// noteSecretValue judges the literals the secure parameter p holds, the
// parameters file's first, and warns of a default the template generates.
func (w *aggregateWriter) noteSecretValue(p *Parameter) {
	if w.noted[p.Name] {
		return
	}
	w.noted[p.Name] = true
	switch {
	case literalHeld(p.Supplied):
		w.judge(p.Name, "value")
	case literalHeld(p.Default):
		w.judge(p.Name, "defaultValue")
	}
	if o, ok := p.Default.(Opaque); ok && newGuidCall.MatchString(o.Text) {
		w.diags = append(w.diags, Report(CodeValueNotCarried, fmt.Sprintf("the default of the template parameter %s "+
			"is newGuid(), which the template evaluates when it deploys: the template generates no secret store entry, "+
			"so the secret is attached by its form and its value is kept where the attachment's origin names", p.Name)))
	}
}

// mappedSecrets returns the secrets the credential slots of the instance cl
// take, off the properties the row's Secrets place there, each read as the
// row's placements are read, each slot once.
func (w *aggregateWriter) mappedSecrets(cl *Classified) []importer.Secret {
	if cl.Row == nil {
		return nil
	}
	var out []importer.Secret
	taken := map[string]bool{}
	for _, pl := range cl.Row.Secrets {
		r := w.readOff(cl, pl.From)
		if !r.ok || taken[pl.Key] {
			continue
		}
		path := "features." + pl.Key
		if s, ok := w.secretOf(path, r.node, pl, r.prop); ok {
			taken[pl.Key] = true
			out = append(out, s)
		}
	}
	return out
}

// secretOf returns the secret attached at path for the value p of source's
// property placed in the slot pl, false where the source holds none there.
func (w *aggregateWriter) secretOf(path string, source *Node, pl Placement, p Prop) (importer.Secret, bool) {
	v := p.Value
	if c, ok := v.(Compose); ok {
		v = firstSecretPart(c)
	}
	switch v := v.(type) {
	case Param:
		if declared := w.param(v.Name); declared != nil {
			return w.paramSecret(path, declared), true
		}
	case Credential:
		return importer.Secret{Path: path, Referenced: true,
			Text: "the credential of " + draftyaml.Comment(v.Node) + " the source reads"}, true
	case Literal:
		if !literalHeld(v) {
			return importer.Secret{}, false
		}
		w.judge(pl.Key, pathText(pl.From))
		return importer.Secret{Path: path, Variable: variableOf(pl.Key),
			Text: "the " + draftyaml.Comment(pl.Key) + " the source writes"}, true
	}
	w.diags = append(w.diags, Report(CodeValueNotCarried, fmt.Sprintf("%s writes %s as a value the draft does not "+
		"read as a credential or a secure parameter, so it is not written: the Release attaches the secret on the slot "+
		"%s by its form", source.ID, pathText(pl.From), pl.Key)))
	return importer.Secret{Path: path, Variable: variableOf(pl.Key),
		Text: "the " + draftyaml.Comment(pl.Key) + " of " + draftyaml.Comment(source.ID)}, true
}

// firstSecretPart returns the first credential, else the first parameter, c
// composes; c itself where it composes neither.
func firstSecretPart(c Compose) Value {
	for _, part := range c.Parts {
		if cr, ok := part.(Credential); ok {
			return cr
		}
	}
	for _, part := range c.Parts {
		if p, ok := part.(Param); ok {
			return p
		}
	}
	return c
}

// draftedSecrets returns the comment lines naming the secrets of the resource
// cl is drafted from: each stays the deploy-time parameter the drafted Recipe
// declares, which no attachment carries.
func (w *aggregateWriter) draftedSecrets(cl *Classified) []string {
	var out []string
	for _, in := range cl.Inputs {
		if in.Source.Pillar != schemadraft.PillarSecret || in.Property == nil {
			continue
		}
		deploy := alphanumeric.CapitalizedWords(cl.Key) + schemadraft.SecretParameter(in.Property)
		line := fmt.Sprintf("The secret at %s is the deploy-time parameter %s of the drafted template, so no "+
			"attachment carries it: pass its value when the template is deployed", pathText(in.Property.Path),
			draftyaml.Comment(deploy))
		if p, ok := nodeProp(cl.Node, in.Property.Path); ok {
			v := p.Value
			if c, isCompose := v.(Compose); isCompose {
				v = firstSecretPart(c)
			}
			if pr, isParam := v.(Param); isParam {
				if declared := w.param(pr.Name); declared != nil {
					w.noteSecretValue(declared)
					if declared.StoreRef != "" {
						line += "; the parameters file gives the template parameter " + draftyaml.Comment(declared.Name) +
							" as a Key Vault reference, so pass that reference in the parameters file the template is deployed with"
					}
				}
			} else if literalHeld(v) {
				w.judge(in.Source.Key, pathText(in.Property.Path))
			}
		}
		out = append(out, line+".")
	}
	return out
}
