// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package arm

import (
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// WriteWrapped drafts the arm parts of the Recipe that sources the published
// module m: the source block, its kind, url and version, the version left
// empty where m states none; a valuesMapping binding each input under the
// module's parameter name, from infra.overrides or the inject pipe, written
// as JSON so any value reads back as it was set, an input that is not required
// only where it is present; then each fixed parameter as written; and each
// output published as the expression
// [reference('<symbol>').outputs.<output>.value], the symbol being the
// instance's name in capitalized words, as the arm provider names the
// nested template it makes of the module. DraftWrapped completes it.
func WriteWrapped(m schemadraft.WrappedModule) (importer.RecipeDraft, []aggrega.Diagnostic) {
	d := importer.RecipeDraft{
		Header: []string{"The arm provider fetches the published module, nests its template as one nested template named " +
			"by locals.symbol, and binds the parameters valuesMapping writes; a parameter it leaves out takes the " +
			"module's own default."},
		Source: sourceBlock(m),
		Locals: []importer.Entry{{
			Key:   symbolLocal,
			Value: "{{ capitalizedWords .context.instance_id }}",
			Comment: []string{"The nested template's symbolic name: the instance's name in capitalized words, " +
				"letters and digits alone, so two instances in one template declare two nested templates."},
		}},
	}
	for _, p := range m.Outputs {
		output := p.Key
		if len(p.Attribute) > 0 {
			output = strings.Join(p.Attribute, ".")
		}
		d.Provides = append(d.Provides, importer.Entry{Key: p.Key,
			Value: "[reference('" + symbol + "').outputs." + output + ".value]"})
	}
	if len(d.Provides) > 0 {
		d.Provides[0].Comment = []string{"The String Pipe: each value is one whole ARM expression reading an output " +
			"of the module's nested template, piped as written into a consumer's context; ARM evaluates it when the " +
			"template is deployed."}
	}
	d.ValuesMapping = valuesMapping(m)
	return d, nil
}

// sourceBlock is the Recipe's spec.source: the module's kind, address and
// version, under a comment asking for the version where there is none.
func sourceBlock(m schemadraft.WrappedModule) string {
	lines := []string{"kind: " + yamlText(m.SourceKind), "url: " + yamlText(m.URL)}
	if m.Version == "" {
		lines = append(lines, "# Write the module's version here: a tag or a sha256: manifest digest.", `version: ""`)
	} else {
		lines = append(lines, "version: "+yamlText(m.Version))
	}
	return strings.Join(lines, "\n") + "\n"
}

// valuesMapping binds each input of m under its parameter name, then each fixed
// parameter.
func valuesMapping(m schemadraft.WrappedModule) string {
	var b strings.Builder
	for _, in := range m.Inputs {
		bind := in.Parameter() + ": {{ toJson " + in.Source.Template() + " }}\n"
		if in.Required {
			b.WriteString(bind)
			continue
		}
		b.WriteString("{{- if " + in.Source.Present() + " }}\n" + bind + "{{- end }}\n")
	}
	for _, e := range m.Fixed {
		b.WriteString(e.Key + ": " + e.Value + "\n")
	}
	return b.String()
}

// yamlText writes s as a plain YAML scalar where it reads back as the string s,
// and quoted otherwise.
func yamlText(s string) string {
	plain := s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._/:-", r))
	}) < 0 && !strings.HasSuffix(s, ":")
	if _, err := strconv.ParseFloat(s, 64); err == nil || !plain {
		return strconv.Quote(s)
	}
	return s
}
