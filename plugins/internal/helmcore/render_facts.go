// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"encoding/json"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The render facts this provider reports for every node it renders, by the names
// its provider page documents. They are reported in this order, every one of them
// on every render, each a list in the order the render emitted what it describes.
const (
	podTemplateLabelsFact   = "pod_template_labels"
	controllerSelectorsFact = "controller_selectors"
	serviceSelectorsFact    = "service_selectors"
	containerPortsFact      = "container_ports"
	podTemplateVolumesFact  = "pod_template_volumes"
)

// ReportEmitted reads what one render emitted, once, and returns both things this
// provider reports about it: the names its resources are addressed by, per key
// space (see [DeclaredResourceNames]), and its render facts — the labels of every
// pod template it emitted, each controller's selector, each Service's selector,
// each container's ports and each pod template's volumes with the volume mounts of
// its containers, every entry with the namespace its document writes.
//
// Both come from the same walk over the same documents, so a resource is either
// read by both or by neither; neither reader parses a file of its own.
//
// fallbackNamespace is the names reader's alone, and the facts reader is not handed
// it. A namespaced resource's name is compared inside the namespace the resource
// will be applied into, so one writing none is compared where it would land by
// default; a cluster-scoped resource's name is compared with no namespace. A fact
// reports what the document says, and a resource writing no namespace lands wherever
// the tool applying it decides, which nothing at build time knows: a fallback in its
// entry would point a template at a namespace its pods may not be in, where an absent
// one lets the template tell that nothing was written.
//
// Every fact is reported on every render, as an empty list when the render emitted
// nothing it describes. An empty list says this provider read the render and found
// none — which a template can tell apart from a fact no provider reported at all,
// the answer for a component another provider rendered.
//
// It fails only if a fact cannot be written as JSON, which no value it builds can
// cause: every entry holds strings, integers and ordered string maps.
func ReportEmitted(files []rendercore.RenderedFile, fallbackNamespace string) (types.OrderedMap[[]string], []rendercore.RenderFact, error) {
	resources := emittedResources(files)
	facts, err := renderFactsOf(resources)
	if err != nil {
		return types.OrderedMap[[]string]{}, nil, err
	}
	return declaredNamesOf(resources, fallbackNamespace), facts, nil
}

// podTemplateLabels is one entry of pod_template_labels: one pod template the
// render emitted, named by the resource that carries it.
type podTemplateLabels struct {
	Kind      string                    `json:"kind"`
	Name      string                    `json:"name,omitempty"`
	Namespace string                    `json:"namespace,omitempty"`
	Labels    *types.OrderedMap[string] `json:"labels,omitempty"`
}

// controllerSelector is one entry of controller_selectors: the label selector one
// controller the render emitted selects its pods by.
type controllerSelector struct {
	Kind        string                    `json:"kind"`
	Name        string                    `json:"name,omitempty"`
	Namespace   string                    `json:"namespace,omitempty"`
	MatchLabels *types.OrderedMap[string] `json:"match_labels,omitempty"`
}

// serviceSelector is one entry of service_selectors: the pods one Service the
// render emitted sends its traffic to.
type serviceSelector struct {
	Name      string                    `json:"name,omitempty"`
	Namespace string                    `json:"namespace,omitempty"`
	Selector  *types.OrderedMap[string] `json:"selector,omitempty"`
}

// containerPorts is one entry of container_ports: the ports one container of a
// pod template declares, named by the resource that carries the template.
type containerPorts struct {
	Kind      string          `json:"kind"`
	Name      string          `json:"name,omitempty"`
	Namespace string          `json:"namespace,omitempty"`
	Container string          `json:"container,omitempty"`
	Ports     []containerPort `json:"ports"`
}

// containerPort is one port a container declares. ContainerPort is an integer
// where the document writes one and the document's own text otherwise.
type containerPort struct {
	Name          string `json:"name,omitempty"`
	ContainerPort any    `json:"container_port,omitempty"`
	Protocol      string `json:"protocol,omitempty"`
}

// podTemplateVolumes is one entry of pod_template_volumes: the volumes one pod
// template the render emitted declares, the claim templates its controller
// declares, and the volume mounts of each of its containers, named by the resource
// that carries the template.
//
// Volumes and VolumeClaimTemplates are pointers so that a part the document does
// not write is left out of the entry while a part it writes empty is reported
// empty. Containers is always a list: it holds the containers that carry a
// volumeMounts list, and is empty when none does.
type podTemplateVolumes struct {
	Kind                 string                  `json:"kind"`
	Name                 string                  `json:"name,omitempty"`
	Namespace            string                  `json:"namespace,omitempty"`
	Volumes              *[]podVolume            `json:"volumes,omitempty"`
	VolumeClaimTemplates *[]string               `json:"volume_claim_templates,omitempty"`
	Containers           []containerVolumeMounts `json:"containers"`
}

// podVolume is one volume a pod template declares: its name, and its type — the
// key naming where its content comes from: persistentVolumeClaim, emptyDir,
// configMap, secret and so on, as the document spells it.
//
// The type is reported under "type" rather than "source" because a FeatureRecipe
// template reads an entry's fields by name, and a field spelled like one of the
// template context's own pillars is read as that pillar.
type podVolume struct {
	Name string `json:"name,omitempty"`
	Type string `json:"type,omitempty"`
}

// containerVolumeMounts is the volume mounts one container of a pod template
// declares.
type containerVolumeMounts struct {
	Container    string        `json:"container,omitempty"`
	VolumeMounts []volumeMount `json:"volume_mounts"`
}

// volumeMount is one volume mount a container declares. ReadOnly is a boolean
// where the document writes one and the document's own text otherwise.
type volumeMount struct {
	Name      string `json:"name,omitempty"`
	MountPath string `json:"mount_path,omitempty"`
	SubPath   string `json:"sub_path,omitempty"`
	ReadOnly  any    `json:"read_only,omitempty"`
}

// workloadShape is where one kind of workload keeps its pod template and its
// selector, as paths from the document's root.
type workloadShape struct {
	// template is the path to the pod template's mapping — the one holding its
	// metadata and spec. Empty for a bare Pod, which is its own template.
	template []string
	// selector is the path to a controller's label selector. Empty for a bare
	// Pod, which selects nothing.
	selector []string
	// claimTemplates is the path to the list of persistent volume claims a
	// controller creates one of per pod, which its pods mount by name like a volume.
	// Empty for every workload but a StatefulSet.
	claimTemplates []string
}

// controller reports whether the shape is a controller's, one that creates its
// pods from a template and selects them by labels.
func (s workloadShape) controller() bool { return len(s.selector) > 0 }

// workloadShapeOf says whether a resource is a workload this provider reads, and
// where its pod template and selector sit.
//
// A workload is recognised by its kind AND its API group, so a custom resource
// that happens to share a kind name with one of these — a `Job` in some other
// group, say — is not read as one: its pods, if it has any, are created by a
// controller at runtime from whatever its own spec says, which nothing here can see.
// Any version of the group is read; the paths are the same in each.
func workloadShapeOf(apiVersion, kind string) (workloadShape, bool) {
	group, recognisable := apiGroup(apiVersion)
	if !recognisable {
		return workloadShape{}, false
	}
	switch group + "/" + kind {
	case "/Pod":
		return workloadShape{}, true
	case "apps/StatefulSet":
		return workloadShape{
			template:       []string{"spec", "template"},
			selector:       []string{"spec", "selector"},
			claimTemplates: []string{"spec", "volumeClaimTemplates"},
		}, true
	case "apps/Deployment", "apps/DaemonSet", "apps/ReplicaSet", "batch/Job":
		return workloadShape{
			template: []string{"spec", "template"},
			selector: []string{"spec", "selector"},
		}, true
	case "batch/CronJob":
		// A CronJob carries a whole Job template, and the pod template is that
		// Job's: one level further down than every other controller's.
		return workloadShape{
			template: []string{"spec", "jobTemplate", "spec", "template"},
			selector: []string{"spec", "jobTemplate", "spec", "selector"},
		}, true
	}
	return workloadShape{}, false
}

// isService reports whether a resource is a Kubernetes Service in the core group.
func isService(apiVersion, kind string) bool {
	group, recognisable := apiGroup(apiVersion)
	return recognisable && group == "" && kind == "Service"
}

// apiGroup splits the group off an apiVersion: "apps/v1" is the group "apps", and
// "v1", which names no group, is the core group "". A document that names no
// apiVersion at all is not recognisable as any group's.
func apiGroup(apiVersion string) (string, bool) {
	if apiVersion == "" {
		return "", false
	}
	group, _, hasGroup := strings.Cut(apiVersion, "/")
	if !hasGroup {
		return "", true
	}
	return group, true
}

// renderFactsOf builds the render facts from the resource documents one walk over
// the emitted files read.
//
// Entries are in the order the walk met the documents, which is the order the
// render emitted them; a label map keeps the order the document writes its keys.
// A part a document does not carry is left out of its entry — a resource with no
// metadata.name has no name in its entry, one with no metadata.namespace no
// namespace, a controller written with no matchLabels has no match_labels — while
// a part it carries empty is reported empty, so an entry says exactly what the
// emitted document says.
//
// It is handed the documents and nothing else, so no namespace this provider would
// otherwise fall back to can reach an entry: see [ReportEmitted].
func renderFactsOf(resources []*yaml.Node) ([]rendercore.RenderFact, error) {
	templates := []podTemplateLabels{}
	controllers := []controllerSelector{}
	services := []serviceSelector{}
	ports := []containerPorts{}
	volumes := []podTemplateVolumes{}

	for _, root := range resources {
		apiVersion := scalarChildValue(root, "apiVersion")
		kind := scalarChildValue(root, "kind")
		metadata := childOf(root, "metadata")
		name := scalarChildValue(metadata, "name")
		namespace := writtenNamespace(metadata)

		if isService(apiVersion, kind) {
			services = append(services, serviceSelector{
				Name:      name,
				Namespace: namespace,
				Selector:  stringMapping(pathOf(root, "spec", "selector")),
			})
			continue
		}

		shape, workload := workloadShapeOf(apiVersion, kind)
		if !workload {
			continue
		}
		if shape.controller() {
			controllers = append(controllers, controllerSelector{
				Kind:        kind,
				Name:        name,
				Namespace:   namespace,
				MatchLabels: stringMapping(childOf(pathOf(root, shape.selector...), "matchLabels")),
			})
		}
		template := pathOf(root, shape.template...)
		if template == nil || template.Kind != yaml.MappingNode {
			continue
		}
		templates = append(templates, podTemplateLabels{
			Kind:      kind,
			Name:      name,
			Namespace: namespace,
			Labels:    stringMapping(pathOf(template, "metadata", "labels")),
		})
		ports = append(ports, containerPortsOf(kind, name, namespace, childOf(template, "spec"))...)
		volumes = append(volumes, podTemplateVolumesOf(kind, name, namespace, childOf(template, "spec"), shape.claimTemplates, root))
	}

	reported := []struct {
		name  string
		value any
	}{
		{podTemplateLabelsFact, templates},
		{controllerSelectorsFact, controllers},
		{serviceSelectorsFact, services},
		{containerPortsFact, ports},
		{podTemplateVolumesFact, volumes},
	}
	facts := make([]rendercore.RenderFact, 0, len(reported))
	for _, r := range reported {
		value, err := json.Marshal(r.value)
		if err != nil {
			return nil, fmt.Errorf("writing render fact %q: %w", r.name, err)
		}
		facts = append(facts, rendercore.RenderFact{Name: r.name, Value: value})
	}
	return facts, nil
}

// containerPortsOf reads the ports each container of one pod spec declares: one
// entry per container that carries a ports list, init containers included, in the
// order the pod spec writes its container lists and each list its containers. A
// sidecar declared among the init containers serves on its ports like any other
// container, so leaving them out would hide a port the pod really opens.
func containerPortsOf(kind, name, namespace string, podSpec *yaml.Node) []containerPorts {
	if podSpec == nil || podSpec.Kind != yaml.MappingNode {
		return nil
	}
	var out []containerPorts
	for i := 0; i+1 < len(podSpec.Content); i += 2 {
		if key := podSpec.Content[i].Value; key != "initContainers" && key != "containers" {
			continue
		}
		list := rendercore.UnwrapAlias(podSpec.Content[i+1])
		if list == nil || list.Kind != yaml.SequenceNode {
			continue
		}
		for _, c := range list.Content {
			c = rendercore.UnwrapAlias(c)
			declared := childOf(c, "ports")
			if declared == nil || declared.Kind != yaml.SequenceNode {
				continue
			}
			entry := containerPorts{
				Kind:      kind,
				Name:      name,
				Namespace: namespace,
				Container: scalarChildValue(c, "name"),
				Ports:     make([]containerPort, 0, len(declared.Content)),
			}
			for _, p := range declared.Content {
				p = rendercore.UnwrapAlias(p)
				if p == nil || p.Kind != yaml.MappingNode {
					continue
				}
				entry.Ports = append(entry.Ports, containerPort{
					Name:          scalarChildValue(p, "name"),
					ContainerPort: portNumber(childOf(p, "containerPort")),
					Protocol:      scalarChildValue(p, "protocol"),
				})
			}
			out = append(out, entry)
		}
	}
	return out
}

// podTemplateVolumesOf reads one pod template's entry of pod_template_volumes: the
// volumes its pod spec writes, the claim templates its controller writes where its
// shape has them (claimTemplates, a path from the document's root), and, for each
// container carrying a volumeMounts list, its mounts — init containers included, in
// the order the pod spec writes its container lists and each list its containers,
// as containerPortsOf reads them. A container mounting a volume reads its data
// whichever list declares it.
func podTemplateVolumesOf(kind, name, namespace string, podSpec *yaml.Node, claimTemplates []string, root *yaml.Node) podTemplateVolumes {
	entry := podTemplateVolumes{
		Kind:       kind,
		Name:       name,
		Namespace:  namespace,
		Containers: []containerVolumeMounts{},
	}
	if declared := childOf(podSpec, "volumes"); declared != nil && declared.Kind == yaml.SequenceNode {
		list := make([]podVolume, 0, len(declared.Content))
		for _, v := range declared.Content {
			v = rendercore.UnwrapAlias(v)
			if v == nil || v.Kind != yaml.MappingNode {
				continue
			}
			list = append(list, podVolume{Name: scalarChildValue(v, "name"), Type: volumeTypeKey(v)})
		}
		entry.Volumes = &list
	}
	if len(claimTemplates) > 0 {
		if declared := pathOf(root, claimTemplates...); declared != nil && declared.Kind == yaml.SequenceNode {
			names := make([]string, 0, len(declared.Content))
			for _, c := range declared.Content {
				names = append(names, scalarChildValue(childOf(c, "metadata"), "name"))
			}
			entry.VolumeClaimTemplates = &names
		}
	}
	if podSpec == nil || podSpec.Kind != yaml.MappingNode {
		return entry
	}
	for i := 0; i+1 < len(podSpec.Content); i += 2 {
		if key := podSpec.Content[i].Value; key != "initContainers" && key != "containers" {
			continue
		}
		list := rendercore.UnwrapAlias(podSpec.Content[i+1])
		if list == nil || list.Kind != yaml.SequenceNode {
			continue
		}
		for _, c := range list.Content {
			c = rendercore.UnwrapAlias(c)
			declared := childOf(c, "volumeMounts")
			if declared == nil || declared.Kind != yaml.SequenceNode {
				continue
			}
			mounts := containerVolumeMounts{
				Container:    scalarChildValue(c, "name"),
				VolumeMounts: make([]volumeMount, 0, len(declared.Content)),
			}
			for _, m := range declared.Content {
				m = rendercore.UnwrapAlias(m)
				if m == nil || m.Kind != yaml.MappingNode {
					continue
				}
				mounts.VolumeMounts = append(mounts.VolumeMounts, volumeMount{
					Name:      scalarChildValue(m, "name"),
					MountPath: scalarChildValue(m, "mountPath"),
					SubPath:   scalarChildValue(m, "subPath"),
					ReadOnly:  writtenBool(childOf(m, "readOnly")),
				})
			}
			entry.Containers = append(entry.Containers, mounts)
		}
	}
	return entry
}

// volumeTypeKey is the key a volume's mapping names its content's source by: the
// first key the document writes other than name. Empty when it writes none.
func volumeTypeKey(volume *yaml.Node) string {
	for i := 0; i+1 < len(volume.Content); i += 2 {
		k := rendercore.UnwrapAlias(volume.Content[i])
		if k == nil || k.Kind != yaml.ScalarNode || k.Value == "name" {
			continue
		}
		return k.Value
	}
	return ""
}

// writtenBool reads a value as the boolean the document writes, or as the
// document's own text when it writes something else; nil when it writes nothing.
func writtenBool(n *yaml.Node) any {
	if n == nil || n.Kind != yaml.ScalarNode {
		return nil
	}
	if n.Tag == "!!bool" {
		var b bool
		if err := n.Decode(&b); err == nil {
			return b
		}
	}
	return n.Value
}

// writtenNamespace is the namespace a resource's metadata writes, or "" where it
// writes none. A namespace written empty or null names no namespace — the resource
// lands where the tool applying it decides, exactly as one writing no namespace
// does — so it reads as none rather than as a value, and a null is never read as
// the text of its spelling.
func writtenNamespace(metadata *yaml.Node) string {
	n := findMapChild(metadata, "namespace")
	if n == nil || n.Kind != yaml.ScalarNode || n.Tag == "!!null" {
		return ""
	}
	return n.Value
}

// portNumber reads a containerPort as the integer the document writes, or as the
// document's own text when it writes something else; nil when it writes nothing.
func portNumber(n *yaml.Node) any {
	if n == nil || n.Kind != yaml.ScalarNode {
		return nil
	}
	if n.Tag == "!!int" {
		var port int64
		if err := n.Decode(&port); err == nil {
			return port
		}
	}
	return n.Value
}

// stringMapping reads a mapping of names to values in the order the document
// writes its keys. Nil when the node is absent or not a mapping, so an entry
// leaves out a part the document does not carry; an empty mapping reads as an
// empty map. A pair whose key or value is not a single value is skipped, and a
// value written as null reads as the empty string.
func stringMapping(n *yaml.Node) *types.OrderedMap[string] {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	out := types.NewOrderedMap[string]()
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := rendercore.UnwrapAlias(n.Content[i]), rendercore.UnwrapAlias(n.Content[i+1])
		if k == nil || v == nil || k.Kind != yaml.ScalarNode || v.Kind != yaml.ScalarNode {
			continue
		}
		value := v.Value
		if v.Tag == "!!null" {
			value = ""
		}
		out.Set(k.Value, value)
	}
	return out
}

// pathOf follows keys down from a mapping node, nil as soon as one is absent.
// With no keys it is the node itself.
func pathOf(n *yaml.Node, keys ...string) *yaml.Node {
	for _, k := range keys {
		n = childOf(n, k)
	}
	return rendercore.UnwrapAlias(n)
}

// childOf is the value under key in a mapping node, read through an alias, or nil.
func childOf(n *yaml.Node, key string) *yaml.Node {
	return rendercore.UnwrapAlias(findMapChild(rendercore.UnwrapAlias(n), key))
}
