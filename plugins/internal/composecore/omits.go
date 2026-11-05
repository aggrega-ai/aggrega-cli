// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// omitKind is one kind of top-level unit a compose file declares that an omit entry
// can leave out: the word an entry's kind spells it with, and the top-level section
// whose keys name the units of that kind.
type omitKind struct {
	kind    string
	section string
}

// omitKinds are the kinds a sourced compose render leaves out, in the order a
// refusal names them. It is this provider's one home of what an entry's kind may
// say. A compose file's top-level models: section is carried into the assembled
// file and is not among them, so an entry naming a model is refused like one
// naming any other kind; every other top-level section either is not carried into
// the assembled file or is derived by the assembly itself, so an entry naming one
// has nothing it could leave out.
var omitKinds = []omitKind{
	{kind: "service", section: "services"},
	{kind: "volume", section: "volumes"},
	{kind: "config", section: "configs"},
}

// omitKindsAccepted is the clause a refusal of an entry ends with: the kinds an
// entry may name and where each is keyed, composed from omitKinds so the refusal
// and the matching cannot name different sets.
func omitKindsAccepted() string {
	kinds := make([]string, len(omitKinds))
	sections := make([]string, len(omitKinds))
	for i, k := range omitKinds {
		kinds[i] = "a " + k.kind
		sections[i] = k.section + ":"
	}
	return "compose leaves out " + joinAlternatives(kinds) + ", each named by its key under " +
		joinAlternatives(sections) + ", and no other kind"
}

// joinAlternatives joins a list as "a, b or c".
func joinAlternatives(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}

// leftOutUnit is how a report may name one unit an entry left out: whether the
// entry that left it out withheld its kind or its name. A unit two entries left out
// is withheld where either entry withheld it.
type leftOutUnit struct {
	withheldKind bool
	withheldName bool
}

// leftOutUnits records every unit a render left out, by kind and then by key.
type leftOutUnits map[string]map[string]leftOutUnit

// add records one unit an entry left out.
func (l leftOutUnits) add(kind, name string, withheld rendercore.OmitFieldsWithheld) {
	if l[kind] == nil {
		l[kind] = map[string]leftOutUnit{}
	}
	u := l[kind][name]
	u.withheldKind = u.withheldKind || withheld.Kind
	u.withheldName = u.withheldName || withheld.Name
	l[kind][name] = u
}

// text names one left-out unit for a report.
func (u leftOutUnit) text(kind, name string) string {
	k := kind
	if u.withheldKind {
		k = rendercore.OmitFieldText(kind, true)
	}
	return k + " " + rendercore.OmitFieldText(name, u.withheldName)
}

// leaveOut applies a sourced render's omit entries to the merged compose document,
// in place, and returns one count per entry in the order the entries arrived — nil
// for a render handed none, so its output carries no counts and the document is
// left exactly as it was.
//
// It runs on the document the values overlay has already been merged into, so a
// unit the overlay added is matched like one the source declared, and before
// anything the render adds itself, so an entry never matches a unit the provider
// put there.
//
// An entry names a unit by its kind — service, volume or config — and its name, the
// unit's exact key under services:, volumes: or configs:. Every entry is checked
// before any is matched, and the first in the order they arrived that names any
// other kind, a namespace or a subchart is refused: a compose file has none of them,
// so there is nothing such an entry could leave out. Each entry's count is the number of units
// it names that the document declares, taken over the document before anything is
// removed, so two entries naming one unit each count it. A section every unit of
// which was left out is removed with them, so the document reads as one that never
// declared it.
//
// Once the units are out, every reference the remaining document still makes to one
// of them halts the render (see requireNoReferenceToLeftOut): the file would name a
// service, a volume or a config it no longer declares.
//
// A field of an entry is never quoted where the entry withholds it, here or in the
// reference halt.
func leaveOut(doc map[string]any, entries []rendercore.ResolvedOmit, nodeID string) ([]int, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	kinds := make([]omitKind, len(entries))
	for i, entry := range entries {
		kind, err := omitKindOf(entry, nodeID)
		if err != nil {
			return nil, err
		}
		kinds[i] = kind
	}

	counts := make([]int, len(entries))
	left := leftOutUnits{}
	for i, entry := range entries {
		units, _ := asStringMap(doc[kinds[i].section])
		if _, declared := units[entry.Entry.Name]; !declared {
			continue
		}
		counts[i] = 1
		left.add(kinds[i].kind, entry.Entry.Name, entry.Withheld)
	}
	for _, k := range omitKinds {
		removeUnits(doc, k.section, left[k.kind])
	}

	if err := requireNoReferenceToLeftOut(doc, left, nodeID); err != nil {
		return nil, err
	}
	return counts, nil
}

// omitKindOf resolves the kind one entry names, refusing an entry that names a kind
// compose has no section for or that carries a namespace or a subchart.
func omitKindOf(entry rendercore.ResolvedOmit, nodeID string) (omitKind, error) {
	kindText := rendercore.OmitFieldText(entry.Entry.Kind, entry.Withheld.Kind)
	nameText := rendercore.OmitFieldText(entry.Entry.Name, entry.Withheld.Name)
	if entry.Entry.Namespace != "" {
		return omitKind{}, cerrors.Wrap(plugincondition.ErrPluginCannotExpressInput(),
			cerrors.TranslationError, cerrors.SourceLocation{Instance: nodeID},
			fmt.Sprintf("compose: an omits entry (kind %s, name %s) names namespace %s, and a compose file has no namespaces: %s; remove the namespace from the entry",
				kindText, nameText, rendercore.OmitFieldText(entry.Entry.Namespace, entry.Withheld.Namespace), omitKindsAccepted()))
	}
	if entry.Entry.Subchart != "" {
		return omitKind{}, cerrors.Wrap(plugincondition.ErrPluginCannotExpressInput(),
			cerrors.TranslationError, cerrors.SourceLocation{Instance: nodeID},
			fmt.Sprintf("compose: an omits entry (kind %s, name %s) names subchart %s, and a compose file has no subcharts: %s; remove the subchart from the entry",
				kindText, nameText, rendercore.OmitFieldText(entry.Entry.Subchart, entry.Withheld.Subchart), omitKindsAccepted()))
	}
	for _, k := range omitKinds {
		if entry.Entry.Kind == k.kind {
			return k, nil
		}
	}
	return omitKind{}, cerrors.Wrap(plugincondition.ErrPluginCannotExpressInput(),
		cerrors.TranslationError, cerrors.SourceLocation{Instance: nodeID},
		fmt.Sprintf("compose: an omits entry (name %s) names kind %s: %s",
			nameText, kindText, omitKindsAccepted()))
}

// removeUnits removes the named units from one top-level section, replacing the
// section rather than editing it, because a section the merge did not have to
// rebuild is the very map the source or the values carried, and a render must not
// change its inputs. A section left with no unit is removed.
func removeUnits(doc map[string]any, section string, names map[string]leftOutUnit) {
	if len(names) == 0 {
		return
	}
	units, ok := asStringMap(doc[section])
	if !ok {
		return
	}
	kept := make(map[string]any, len(units))
	for name, unit := range units {
		if _, out := names[name]; !out {
			kept[name] = unit
		}
	}
	if len(kept) == 0 {
		delete(doc, section)
		return
	}
	doc[section] = kept
}

// unitReference is one place an entry of a compose file names another unit: the
// kind and name it names, and where it names it, spelled as the path of the field
// below the entry naming it.
type unitReference struct {
	kind string
	name string
	at   string
}

// referrer is one body in a compose file that can name another unit, the label a
// report names it by, and which of the specification's attribute sets it carries.
type referrer struct {
	label string
	body  map[string]any
	// workload is set for a service and a job, which carry build and depends_on.
	workload bool
	// service is set for a service alone, which carries extends and links.
	service bool
}

// requireNoReferenceToLeftOut halts when the document still names a unit the
// render left out, naming every such reference, the entry that makes it, and the
// unit it names.
//
// What counts as a reference is what the Compose Specification defines as one entry
// of a file naming a service, a volume or a config of the same file — each a name the
// reference loader compose reads a file with refuses where that unit is not declared:
//
//   - a service named by depends_on (the list and the mapping), links (SERVICE or
//     SERVICE:ALIAS), volumes_from (SERVICE or SERVICE:MODE; a container: entry names a
//     container, not a service), network_mode, ipc, pid or uts set to service:NAME,
//     extends naming a service of the same file (the bare name, or service: with no
//     file:), and build.additional_contexts set to service:NAME;
//   - a volume named as the source of a volumes entry: the short form SOURCE:TARGET
//     where SOURCE is a name rather than a path, and the long form of type volume;
//   - a config named by a configs entry, the short form and the long form's source.
//
// The entries that make them are the file's services, each pre_start hook of a
// service, and its jobs — each carries the attributes the specification gives it.
// external_links (a container outside the application), extends with a file: (a
// service of another file) and credential_spec.config (which the loader accepts
// naming a config the file does not declare) are not references to a unit here.
//
// Every value is compared as written: an interpolated value is never resolved here,
// so a reference spelled through a variable is not one this check can see. A
// reference to a unit the file never declared is not reported: only what an entry
// left out is the render's to answer for.
//
// References are reported services first and then jobs, each in key order, each
// entry's hooks after the entry, and an entry's references in a fixed order of the
// fields above, so one document yields one message.
func requireNoReferenceToLeftOut(doc map[string]any, left leftOutUnits, nodeID string) error {
	if len(left) == 0 {
		return nil
	}
	var findings []string
	for _, r := range referrers(doc) {
		for _, ref := range unitReferences(r) {
			unit, out := left[ref.kind][ref.name]
			if !out {
				continue
			}
			findings = append(findings, fmt.Sprintf("%s names %s at %s", r.label, unit.text(ref.kind, ref.name), ref.at))
		}
	}
	if len(findings) == 0 {
		return nil
	}
	return cerrors.Wrap(plugincondition.ErrPluginRequiredInputUnresolved(),
		cerrors.TranslationError, cerrors.SourceLocation{Instance: nodeID},
		fmt.Sprintf("compose: what an omits entry left out is still named by the file: %s; compose refuses a file naming a service, a volume or a config it does not declare: leave out what names it as well, change the reference through valuesMapping, or remove the entry",
			strings.Join(findings, "; ")))
}

// referrers lists every body of the document that can name another unit, in the
// order references are reported in.
func referrers(doc map[string]any) []referrer {
	var out []referrer
	services, _ := asStringMap(doc["services"])
	for _, name := range sortedNames(services) {
		body, ok := asStringMap(services[name])
		if !ok {
			continue
		}
		label := fmt.Sprintf("service %q", name)
		out = append(out, referrer{label: label, body: body, workload: true, service: true})
		hooks, _ := body["pre_start"].([]any)
		for i, h := range hooks {
			if hook, ok := asStringMap(h); ok {
				out = append(out, referrer{label: fmt.Sprintf("%s pre_start[%d]", label, i), body: hook})
			}
		}
	}
	jobs, _ := asStringMap(doc["jobs"])
	for _, name := range sortedNames(jobs) {
		if body, ok := asStringMap(jobs[name]); ok {
			out = append(out, referrer{label: fmt.Sprintf("job %q", name), body: body, workload: true})
		}
	}
	return out
}

// sortedNames returns a section's keys in lexical order.
func sortedNames(m map[string]any) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// servicePrefix opens a value naming a service in the attributes that also accept
// other kinds of value.
const servicePrefix = "service:"

// unitReferences lists every reference one body makes to a service, a volume or a
// config, in the order they are reported.
func unitReferences(r referrer) []unitReference {
	var refs []unitReference
	body := r.body
	service := func(name, at string) {
		if name != "" {
			refs = append(refs, unitReference{kind: "service", name: name, at: at})
		}
	}

	if r.service {
		if name, ok := body["extends"].(string); ok {
			service(name, "extends")
		} else if ext, ok := asStringMap(body["extends"]); ok {
			if file, named := ext["file"]; !named || file == nil {
				if name, ok := ext["service"].(string); ok {
					service(name, "extends")
				}
			}
		}
	}
	if r.workload {
		switch deps := body["depends_on"].(type) {
		case []any:
			for i, d := range deps {
				if name, ok := d.(string); ok {
					service(name, fmt.Sprintf("depends_on[%d]", i))
				}
			}
		default:
			if m, ok := asStringMap(deps); ok {
				for _, name := range sortedNames(m) {
					service(name, "depends_on."+name)
				}
			}
		}
	}
	if r.service {
		links, _ := body["links"].([]any)
		for i, l := range links {
			if link, ok := l.(string); ok {
				service(linkedService(link), fmt.Sprintf("links[%d]", i))
			}
		}
	}
	from, _ := body["volumes_from"].([]any)
	for i, f := range from {
		if v, ok := f.(string); ok && !strings.HasPrefix(v, "container:") {
			name, _, _ := strings.Cut(v, ":")
			service(name, fmt.Sprintf("volumes_from[%d]", i))
		}
	}
	for _, key := range []string{"network_mode", "ipc", "pid", "uts"} {
		if v, ok := body[key].(string); ok {
			if name, isService := strings.CutPrefix(v, servicePrefix); isService {
				service(name, key)
			}
		}
	}
	if r.workload {
		refs = append(refs, additionalContextReferences(body)...)
	}

	volumes, _ := body["volumes"].([]any)
	for i, v := range volumes {
		if name, ok := namedVolumeSource(v); ok {
			refs = append(refs, unitReference{kind: "volume", name: name, at: fmt.Sprintf("volumes[%d]", i)})
		}
	}
	configs, _ := body["configs"].([]any)
	for i, c := range configs {
		if name, ok := configSource(c); ok {
			refs = append(refs, unitReference{kind: "config", name: name, at: fmt.Sprintf("configs[%d]", i)})
		}
	}
	return refs
}

// linkedService returns the service a links entry names: SERVICE, or the SERVICE
// half of SERVICE:ALIAS, read as the reference loader reads it.
func linkedService(link string) string {
	if parts := strings.Split(link, ":"); len(parts) == 2 {
		return parts[0]
	}
	return link
}

// additionalContextReferences returns the services a build's additional contexts
// name, each a context whose value is service:NAME. The contexts are a mapping of
// context name to value, or a list of NAME=VALUE entries; a mapping is read in key
// order and a list in its own.
func additionalContextReferences(body map[string]any) []unitReference {
	build, ok := asStringMap(body["build"])
	if !ok {
		return nil
	}
	var refs []unitReference
	add := func(value any, at string) {
		v, ok := value.(string)
		if !ok {
			return
		}
		if name, isService := strings.CutPrefix(v, servicePrefix); isService && name != "" {
			refs = append(refs, unitReference{kind: "service", name: name, at: at})
		}
	}
	switch contexts := build["additional_contexts"].(type) {
	case []any:
		for i, c := range contexts {
			if entry, ok := c.(string); ok {
				if _, value, found := strings.Cut(entry, "="); found {
					add(value, fmt.Sprintf("build.additional_contexts[%d]", i))
				}
			}
		}
	default:
		if m, ok := asStringMap(contexts); ok {
			for _, name := range sortedNames(m) {
				add(m[name], "build.additional_contexts."+name)
			}
		}
	}
	return refs
}

// namedVolumeSource returns the named volume one volumes entry mounts, and false for
// an entry mounting a path, an image, a temporary filesystem or nothing at all.
//
// The long form names one where its type is volume and it carries a source. The
// short form is SOURCE:TARGET[:MODE], where SOURCE is a path where it begins with ".",
// "/", "~" or "\\" or is a single drive letter, and a volume name otherwise — the
// reading the reference loader gives it; an entry with no colon, or of one or two
// characters, is the container path of an anonymous volume and names none.
func namedVolumeSource(entry any) (string, bool) {
	switch v := entry.(type) {
	case string:
		if len(v) <= 2 {
			return "", false
		}
		source, _, found := strings.Cut(v, ":")
		if !found || isMountPath(source) {
			return "", false
		}
		return source, true
	default:
		long, ok := asStringMap(v)
		if !ok {
			return "", false
		}
		if kind, _ := long["type"].(string); kind != "volume" {
			return "", false
		}
		source, _ := long["source"].(string)
		return source, source != ""
	}
}

// isMountPath reports whether the source of a short-form volumes entry is a path on
// the host rather than a volume name.
func isMountPath(source string) bool {
	if source == "" {
		return true
	}
	switch source[0] {
	case '.', '/', '~':
		return true
	}
	if strings.HasPrefix(source, `\\`) {
		return true
	}
	runes := []rune(source)
	return len(runes) == 1 && unicode.IsLetter(runes[0])
}

// configSource returns the config one configs entry names: the short form is the
// name itself, the long form names it as its source.
func configSource(entry any) (string, bool) {
	if name, ok := entry.(string); ok {
		return name, name != ""
	}
	long, ok := asStringMap(entry)
	if !ok {
		return "", false
	}
	source, _ := long["source"].(string)
	return source, source != ""
}
