// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package terraform

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// ociKind is the source kind of a module published to an OCI registry; any
// other kind of a wrapped module is read from a git repository.
const ociKind = "terraform-module-oci"

// WriteWrapped drafts the terraform parts of the Recipe that sources the
// published module m whole: the source block, its kind, url and ref, or version
// for a module in an OCI registry, with module_subdir where the module is
// inside the repository, and the ref left empty where m states no version; a
// valuesMapping binding each input under the module's own variable name, as the
// local module's line writer binds one, an input that is not required only where
// it is present; then each fixed parameter as written; and each output
// published as ${module.<instance>.<output>}, named by the first segment of its
// attribute, or by its key where it has none. No module file is written: the
// terraform provider fetches the module. DraftWrapped completes it.
func WriteWrapped(m schemadraft.WrappedModule) (importer.RecipeDraft, []aggrega.Diagnostic) {
	d := importer.RecipeDraft{
		Header: []string{"The terraform provider fetches the published module named by spec.source and calls it as " +
			"the module block of this instance, setting each argument valuesMapping writes; an argument it leaves " +
			"out takes the module's own default."},
		Source: sourceBlock(m),
	}
	if m.URL == "" && m.AddressOnly != "" {
		d.Header = append(d.Header, "The module is called at "+m.AddressOnly+" in the source, which this draft does not "+
			"read: write the address the module's files are fetched from as spec.source.url.")
	}
	for _, o := range m.Outputs {
		name := o.Key
		if len(o.Attribute) > 0 {
			name = strings.Join(o.Attribute, ".")
		}
		d.Provides = append(d.Provides, importer.Entry{Key: o.Key,
			Value: "${module.{{ .context.instance_id }}." + name + "}"})
	}
	if len(d.Provides) > 0 {
		d.Provides[0].Comment = []string{"Each value is the module's output of that name, which the root " +
			"configuration resolves where it declares the module; the String Pipe pipes it as written into a " +
			"consumer's context."}
	}
	names := make([]string, len(m.Inputs))
	for i, in := range m.Inputs {
		names[i] = in.Parameter()
	}
	mapping := valuesMapping(m.Inputs, names)
	for _, e := range m.Fixed {
		mapping += yamlKey(e.Key) + ": " + e.Value + "\n"
	}
	d.ValuesMapping = mapping
	return d, nil
}

// sourceBlock is the Recipe's spec.source: the module's kind, url and ref or
// version, under a comment asking for the version where there is none.
func sourceBlock(m schemadraft.WrappedModule) string {
	lines := []string{"kind: " + scalar(m.SourceKind), "url: " + scalar(m.URL)}
	field := "ref"
	if m.SourceKind == ociKind {
		field = "version"
	}
	if m.Version == "" {
		lines = append(lines, "# Write the module's version here: a tag, or for an OCI module a tag or a sha256: "+
			"manifest digest.", field+`: ""`)
	} else {
		lines = append(lines, field+": "+scalar(m.Version))
	}
	if m.Subdir != "" && m.SourceKind != ociKind {
		lines = append(lines, "module_subdir: "+scalar(m.Subdir))
	}
	return strings.Join(lines, "\n") + "\n"
}

// plainScalar matches text a YAML reader reads back as the string it is.
var plainScalar = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@+-]*$`)

// scalar writes s as a plain YAML scalar where it reads back as the string s,
// and quoted otherwise.
func scalar(s string) string {
	if _, err := strconv.ParseFloat(s, 64); err == nil || !plainScalar.MatchString(s) || strings.HasSuffix(s, ":") {
		return importer.QuotedString(s)
	}
	switch strings.ToLower(s) {
	case "true", "false", "null", "yes", "no", "on", "off":
		return importer.QuotedString(s)
	}
	return s
}
