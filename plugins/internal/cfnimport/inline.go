// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cfnimport/cfnrules"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
)

// The types the rules below read beyond the cfnrules package's lists.
const (
	serviceType      = "AWS::ECS::Service"
	taskDefinition   = "AWS::ECS::TaskDefinition"
	eventSourceType  = "AWS::Lambda::EventSourceMapping"
	subscriptionType = "AWS::SNS::Subscription"
)

// relate reads, in g once its values are lowered, what the cfnrules package
// says of resources that are part of another, and the edges a resource
// relating two others draws between them:
//
//   - each task definition with several containers keeps its main container
//     first, as [reader.mainContainer] chooses it, and names each other
//     container not carried;
//   - each child an inline rule names is read in place of the reference each
//     parent referencing it writes at the rule's path ([reader.inline]);
//   - each child a parent link names is read as part of the one resource its
//     link names ([reader.link]);
//   - an event source mapping draws a dependency of its function on the
//     resource whose records invoke it, and a topic subscription one of its
//     endpoint on the topic;
//   - a serverless function records what the Recipe deploying a function does
//     not take of it, where a row maps it ([reader.serverlessFunctions]), and
//     an RDS instance or Aurora cluster what its Recipe makes of the master
//     password it writes ([masterPasswords]).
func (r *reader) relate(g *cloudimport.Graph) {
	r.serverlessFunctions(g)
	masterPasswords(g)
	for _, n := range g.Nodes {
		if strings.EqualFold(n.Type, taskDefinition) {
			r.mainContainer(g, n)
		}
	}
	r.inline(g)
	byID := map[string]*cloudimport.Node{}
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	links := cfnrules.ParentLinks()
	for _, n := range g.Nodes {
		for _, l := range links {
			if strings.EqualFold(n.Type, l.Child) {
				r.link(n, l, byID)
				break
			}
		}
	}
	for _, n := range g.Nodes {
		switch {
		case strings.EqualFold(n.Type, eventSourceType):
			depend(byID[n.Parent], named(n.Body, []string{"Properties", "EventSourceArn"}, byID))
		case strings.EqualFold(n.Type, subscriptionType) && n.Parent != "":
			for _, endpoint := range named(n.Body, []string{"Properties", "Endpoint"}, byID) {
				depend(byID[endpoint], []string{n.Parent})
			}
		}
	}
}

// depend adds each of ids that is not n's own and n does not depend on to
// n's dependencies; nothing where n is nil.
func depend(n *cloudimport.Node, ids []string) {
	if n == nil {
		return
	}
	for _, id := range ids {
		if id != n.ID && !slices.Contains(n.DependsOn, id) {
			n.DependsOn = append(slices.Clone(n.DependsOn), id)
		}
	}
}

// mainContainer keeps the main container of the task definition n first
// among the containers it defines, where it defines several: the one the
// load balancer of a service running n names, the first such service by ID,
// else the first essential one, else the first. Each other container is
// reported not carried, as the Amazon ECS Recipes run one container, and the
// decision on n names the main one.
func (r *reader) mainContainer(g *cloudimport.Graph, n *cloudimport.Node) {
	defs := propAt(n.Body, []string{"Properties", "ContainerDefinitions"})
	if defs == nil || len(defs.Items) < 2 {
		return
	}
	main := -1
	if wanted, ok := balancedContainer(g, n.ID); ok {
		main = slices.IndexFunc(defs.Items, func(c cloudimport.Prop) bool {
			name, _ := textAt(c.Props, "Name")
			return name == wanted
		})
	}
	if main < 0 {
		main = slices.IndexFunc(defs.Items, func(c cloudimport.Prop) bool {
			essential := propAt(c.Props, []string{"Essential"})
			lit, isLit := essential.valueOf().(cloudimport.Literal)
			return isLit && strings.TrimSpace(string(lit.JSON)) == "true"
		})
	}
	main = max(main, 0)
	mainName := containerName(defs.Items[main], main)
	var others []string
	for i, c := range defs.Items {
		if i == main {
			continue
		}
		name := containerName(c, i)
		others = append(others, name)
		d := cloudimport.Report(cloudimport.CodeResourceNotCarried, "the task definition "+n.ID+" defines the "+
			"container "+name+" beside its main container "+mainName+", and the Amazon ECS Recipes of the catalog "+
			"run one container, the main one — the one a service's load balancer names, else the first essential "+
			"one, else the first — so "+name+" is not carried: run it as a component of its own")
		d.SourceRef = n.Source + "/Properties/ContainerDefinitions/" + strconv.Itoa(i)
		r.note(d)
	}
	items := append([]cloudimport.Prop{defs.Items[main]}, slices.Delete(slices.Clone(defs.Items), main, main+1)...)
	defs.Items = items
	n.Says = append(n.Says, "its main container is "+mainName+", which the instance runs, and "+andList(others)+
		" are not carried")
}

// balancedContainer returns the container name the load balancer of the first
// service by ID running the task definition id names, false where none names
// one.
func balancedContainer(g *cloudimport.Graph, id string) (string, bool) {
	for _, s := range g.Nodes {
		if !strings.EqualFold(s.Type, serviceType) {
			continue
		}
		ref, ok := propAt(s.Body, []string{"Properties", "TaskDefinition"}).valueOf().(cloudimport.Ref)
		if !ok || ref.Node != id || len(ref.Attr) > 0 {
			continue
		}
		lbs := propAt(s.Body, []string{"Properties", "LoadBalancers"})
		if lbs == nil {
			continue
		}
		for _, lb := range lbs.Items {
			if name, ok := textAt(lb.Props, "ContainerName"); ok {
				return name, true
			}
		}
	}
	return "", false
}

// containerName names the container c, the ith a task definition defines: its
// Name, else its position.
func containerName(c cloudimport.Prop, i int) string {
	if name, ok := textAt(c.Props, "Name"); ok {
		return draftyaml.Comment(name)
	}
	return "at index " + strconv.Itoa(i)
}

// inline reads each child an inline rule of the cfnrules package names in
// place of the reference to it: where a parent of the rule's type references a
// node of the child's type, by its identifier alone, at the rule's path, the
// child's properties are written there in its place, each parent taking a
// copy of its own, and the parent takes the child's dependencies as its own.
// A child no other node references is then no node of its own: nothing is
// drafted for it, a resource-folded record names the parents it is written
// with, and the decision on each says what it read in place of the reference.
// A child another node references too stays a node, read in place in each
// parent all the same.
func (r *reader) inline(g *cloudimport.Graph) {
	byID := map[string]*cloudimport.Node{}
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	parents := map[string][]string{}
	var children []string
	for _, rule := range cfnrules.Inlines() {
		path := strings.Split(rule.Path, ".")
		for _, n := range g.Nodes {
			if !strings.EqualFold(n.Type, rule.Parent) {
				continue
			}
			at := propAt(n.Body, path)
			ref, ok := at.valueOf().(cloudimport.Ref)
			child := byID[ref.Node]
			if !ok || len(ref.Attr) > 0 || child == nil || child == n || !strings.EqualFold(child.Type, rule.Child) {
				continue
			}
			var props []cloudimport.Prop
			if p := propAt(child.Body, []string{"Properties"}); p != nil {
				props = cloneProps(p.Props)
			}
			at.Value, at.Props, at.Items = nil, props, nil
			depend(n, child.DependsOn)
			n.Says = append(n.Says, "it references "+child.ID+" ("+child.Type+") at "+rule.Path+", whose "+
				"properties are read there in place of the reference")
			n.Says = append(n.Says, child.Says...)
			if _, met := parents[child.ID]; !met {
				children = append(children, child.ID)
			}
			parents[child.ID] = append(parents[child.ID], n.ID+" at "+rule.Path)
		}
	}
	if len(children) == 0 {
		return
	}
	referenced := map[string]bool{}
	for _, e := range cloudimport.Edges(g) {
		referenced[e.To] = true
	}
	for _, id := range children {
		child := byID[id]
		stays := ", written with it, so nothing is drafted for it"
		if len(parents[id]) > 1 {
			stays = ", written with each, so nothing is drafted for it"
		}
		if referenced[id] {
			stays = ", and stays a resource of its own, as another resource reads it too"
		} else {
			g.Nodes = slices.DeleteFunc(g.Nodes, func(n *cloudimport.Node) bool { return n == child })
		}
		d := cloudimport.Report(cloudimport.CodeResourceFolded, child.ID+" ("+child.Type+") is read in place of "+
			"the reference to it of "+andList(parents[id])+stays)
		d.SourceRef = child.Source
		r.note(d)
	}
}

// link reads the child n as part of the one node its parent link l names, n's
// Parent; where the link names several nodes, or none the graph holds, n
// stays a node of its own, and the decision on it says why.
func (r *reader) link(n *cloudimport.Node, l cfnrules.ParentLink, byID map[string]*cloudimport.Node) {
	path := strings.Split(l.Path, ".")
	if !holdsAt(n.Body, path) {
		return
	}
	ids := named(n.Body, path, byID)
	switch {
	case len(ids) == 1 && ids[0] != n.ID:
		n.Parent = ids[0]
	case len(ids) > 1:
		n.Says = append(n.Says, "its "+l.Path+" names "+andList(ids)+", so it is read as part of none of them")
	default:
		n.Says = append(n.Says, "its "+l.Path+" names no resource the template declares, so it is read as part "+
			"of none")
	}
}

// named returns the IDs of the nodes of byID the values of body at path read,
// a segment * standing for each item of a list, in the order read, each once.
func named(body []cloudimport.Prop, path []string, byID map[string]*cloudimport.Node) []string {
	var out []string
	for _, v := range valuesAt(body, path) {
		for _, ref := range refsIn(v) {
			if byID[ref.Node] != nil && !slices.Contains(out, ref.Node) {
				out = append(out, ref.Node)
			}
		}
	}
	return out
}

// refsIn returns the references v reads, a part of composed text included.
func refsIn(v cloudimport.Value) []cloudimport.Ref {
	switch v := v.(type) {
	case cloudimport.Ref:
		return []cloudimport.Ref{v}
	case cloudimport.Compose:
		var out []cloudimport.Ref
		for _, p := range v.Parts {
			out = append(out, refsIn(p)...)
		}
		return out
	}
	return nil
}

// holdsAt reports whether body writes a value at path, a segment * standing
// for any item of a list.
func holdsAt(body []cloudimport.Prop, path []string) bool {
	found := false
	walkAt(body, path, func(cloudimport.Prop) { found = true })
	return found
}

// valuesAt returns the leaf values body writes at path, a segment * standing
// for each item of a list, in order.
func valuesAt(body []cloudimport.Prop, path []string) []cloudimport.Value {
	var out []cloudimport.Value
	walkAt(body, path, func(p cloudimport.Prop) {
		if p.Value != nil {
			out = append(out, p.Value)
		}
	})
	return out
}

// walkAt calls visit with each property of body at path, a segment * standing
// for each item of a list and a digit run for the item of that index.
func walkAt(props []cloudimport.Prop, path []string, visit func(cloudimport.Prop)) {
	if len(path) == 0 {
		return
	}
	seg, rest := path[0], path[1:]
	for i, p := range props {
		if p.Name != seg && (p.Name != "" || seg != "*" && seg != strconv.Itoa(i)) {
			continue
		}
		if len(rest) == 0 {
			visit(p)
			continue
		}
		walkAt(append(slices.Clone(p.Props), p.Items...), rest, visit)
	}
}

// propAt returns the property of body at path, its members named exactly and
// a list's items by index, nil where body writes none there; the property is
// body's own, so a change to it is a change to body.
func propAt(body []cloudimport.Prop, path []string) *propRef {
	props := body
	var at *cloudimport.Prop
	for _, seg := range path {
		at = nil
		if i, err := strconv.Atoi(seg); err == nil && len(props) > 0 && props[0].Name == "" {
			if i >= 0 && i < len(props) {
				at = &props[i]
			}
		} else {
			for i := range props {
				if props[i].Name == seg {
					at = &props[i]
					break
				}
			}
		}
		if at == nil {
			return nil
		}
		props = at.Props
		if at.Items != nil {
			props = at.Items
		}
	}
	return (*propRef)(at)
}

// propRef is a property of a node's body, read and written in place.
type propRef cloudimport.Prop

// valueOf returns the value p holds, nil where p is nil or holds a subtree.
func (p *propRef) valueOf() cloudimport.Value {
	if p == nil {
		return nil
	}
	return p.Value
}

// textAt returns the literal text of the member name of props, false where it
// writes none.
func textAt(props []cloudimport.Prop, name string) (string, bool) {
	lit, ok := propAt(props, []string{name}).valueOf().(cloudimport.Literal)
	if !ok {
		return "", false
	}
	return schemadraft.JSONText(lit.JSON)
}

// cloneProps returns a copy of props sharing no list with them.
func cloneProps(props []cloudimport.Prop) []cloudimport.Prop {
	if props == nil {
		return nil
	}
	out := make([]cloudimport.Prop, len(props))
	for i, p := range props {
		p.Props, p.Items = cloneProps(p.Props), cloneProps(p.Items)
		out[i] = p
	}
	return out
}

// andList joins items with commas and a final "and".
func andList(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}
