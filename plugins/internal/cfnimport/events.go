// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
)

// eventKind is how the import reads the events of one Type a serverless
// function or state machine writes under its Events.
type eventKind struct {
	// typ is the event's Type.
	typ string
	// reads is the property naming the resource whose messages or records
	// invoke the resource writing the event, which uses that resource; empty
	// where the event names none.
	reads string
	// via is the property naming the API the event routes through, which uses
	// the resource writing the event, and implicit says what SAM writes where
	// the event names no API; both empty for an event routing through none.
	via, implicit string
	// writes says what SAM writes for the event.
	writes string
}

// eventKinds are the events the serverless transform writes resources for.
var eventKinds = []eventKind{
	{typ: "SQS", reads: "Queue", writes: "an SQS event, for which SAM writes an event source mapping polling the queue"},
	{typ: "Kinesis", reads: "Stream", writes: "a Kinesis event, for which SAM writes an event source mapping reading " +
		"the stream"},
	{typ: "DynamoDB", reads: "Stream", writes: "a DynamoDB event, for which SAM writes an event source mapping " +
		"reading the table's stream"},
	{typ: "MSK", reads: "Stream", writes: "an MSK event, for which SAM writes an event source mapping reading the " +
		"cluster's topic"},
	{typ: "MQ", reads: "Broker", writes: "an MQ event, for which SAM writes an event source mapping reading the " +
		"broker's queue"},
	{typ: "DocumentDB", reads: "Cluster", writes: "a DocumentDB event, for which SAM writes an event source mapping " +
		"reading the cluster's change stream"},
	{typ: "SelfManagedKafka", writes: "a SelfManagedKafka event, for which SAM writes an event source mapping " +
		"reading a Kafka cluster"},
	{typ: "S3", reads: "Bucket", writes: "an S3 event, for which SAM writes a notification into the bucket and the " +
		"permission letting the bucket invoke it"},
	{typ: "SNS", reads: "Topic", writes: "an SNS event, for which SAM writes the topic's subscription and the " +
		"permission letting the topic invoke it"},
	{typ: "EventBridgeRule", reads: "EventBusName", writes: "an EventBridgeRule event, for which SAM writes an " +
		"EventBridge rule targeting it"},
	{typ: "CloudWatchEvent", reads: "EventBusName", writes: "a CloudWatchEvent event, for which SAM writes an " +
		"EventBridge rule targeting it"},
	{typ: "Api", via: "RestApiId", implicit: "an Api event naming no RestApiId, for which SAM creates an API of its " +
		"own, ServerlessRestApi, with the event's route", writes: "an Api event, for which SAM writes its route into " +
		"the API and the permission letting the API invoke it"},
	{typ: "HttpApi", via: "ApiId", implicit: "an HttpApi event naming no ApiId, for which SAM creates an HTTP API of " +
		"its own, ServerlessHttpApi, with the event's route", writes: "an HttpApi event, for which SAM writes its " +
		"route into the API and the permission letting the API invoke it"},
	{typ: "Schedule", writes: "a Schedule event, for which SAM writes an EventBridge rule invoking it on a schedule"},
	{typ: "ScheduleV2", writes: "a ScheduleV2 event, for which SAM writes an EventBridge Scheduler schedule invoking " +
		"it"},
	{typ: "CloudWatchLogs", writes: "a CloudWatchLogs event, for which SAM writes a subscription filter on the log " +
		"group"},
	{typ: "Cognito", writes: "a Cognito event, for which SAM writes it into the user pool's triggers"},
	{typ: "IoTRule", writes: "an IoTRule event, for which SAM writes an AWS IoT topic rule invoking it"},
	{typ: "AlexaSkill", writes: "an AlexaSkill event, for which SAM writes the permission letting an Alexa skill " +
		"invoke it"},
}

// eventKindOf returns the kind of an event of Type typ: an event of a Type
// eventKinds does not list names no resource and routes through no API.
func eventKindOf(typ string) eventKind {
	for _, k := range eventKinds {
		if k.typ == typ {
			return k
		}
	}
	return eventKind{typ: typ, writes: "an event of the type " + typ + ", for which SAM writes what invokes it"}
}

// serverless leaves out of the draft what the serverless transform writes
// for g's resources when the stack deploys, as no instance of the draft
// writes it, reading each event and connector as an edge of the graph — a
// dependency, the edge every other dependency is:
//
//   - a connector, a resource of type AWS::Serverless::Connector or one a
//     resource writes under its Connectors, is a dependency of its source on
//     each destination it names by its logical ID, a resource writing it
//     being its source; nothing is drafted for it — a connector resource is
//     no node, and a dependency on it is dropped —, and it is reported
//     resource-not-carried, naming the permissions SAM writes into IAM
//     policies for it;
//   - an event of a serverless function or state machine reading a resource
//     of the template ([eventKind]) is a dependency of the function or state
//     machine on that resource, and one routing through an API of the
//     template is a dependency of that API on it; each event is left out of
//     its body and reported resource-not-carried, naming what SAM writes for
//     it, an Api or HttpApi event naming no API saying SAM creates one.
//
// The resource an event reads, or the API it routes through, is the node its
// value references, lowered as [reader.lower] lowers a value and reporting
// nothing, as the event is not carried. g is the graph [reader.flatten]
// flattened, before its values are lowered.
func (r *reader) serverless(g *cloudimport.Graph) {
	byID := map[string]*cloudimport.Node{}
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	var dropped []string
	for _, n := range g.Nodes {
		h := r.home[n]
		if n.Type == connectorType {
			r.connector(h, byID)
			dropped = append(dropped, n.ID)
			continue
		}
		r.embeddedConnectors(h, byID)
		if n.Type == functionType || n.Type == stateMachineType {
			r.events(h, byID)
		}
	}
	if len(dropped) == 0 {
		return
	}
	isDropped := func(id string) bool { return slices.Contains(dropped, id) }
	g.Nodes = slices.DeleteFunc(g.Nodes, func(n *cloudimport.Node) bool { return isDropped(n.ID) })
	for _, n := range g.Nodes {
		if slices.ContainsFunc(n.DependsOn, isDropped) {
			n.DependsOn = slices.DeleteFunc(slices.Clone(n.DependsOn), isDropped)
		}
	}
	for _, id := range dropped {
		delete(r.nodeIDs, id)
	}
}

// connect makes each node of from depend on each node of to.
func connect(byID map[string]*cloudimport.Node, from, to []string) {
	for _, id := range from {
		n := byID[id]
		if n == nil {
			continue
		}
		n.DependsOn = addNew(slices.Clone(n.DependsOn), slices.DeleteFunc(slices.Clone(to), func(dep string) bool {
			return dep == id
		})...)
	}
}

// connects says the edges the draft draws from each node of from to each
// node of to.
func connects(from, to []string) string {
	return "the draft connects " + strings.Join(from, ", ") + " to " + strings.Join(to, ", ")
}

// connector reads the connector resource h as [reader.serverless] says.
func (r *reader) connector(h home, byID map[string]*cloudimport.Node) {
	res, _ := objectOf(h.s.t.resources.get(h.id))
	props, _ := objectOf(res.get("Properties"))
	at := jsonPointer("Resources", h.id)
	r.connectorRead(h, r.connectorEnds(h.s, props.get("Source")), props.get("Destination"), at,
		"the connector "+h.id+" ("+h.n.Type+") at "+at, byID)
}

// embeddedConnectors reads each connector the resource h writes under its
// Connectors as [reader.serverless] says, and leaves them out of its body.
func (r *reader) embeddedConnectors(h home, byID map[string]*cloudimport.Node) {
	res, _ := objectOf(h.s.t.resources.get(h.id))
	raw := res.get("Connectors")
	if raw == nil {
		return
	}
	h.n.Body = slices.DeleteFunc(h.n.Body, func(p cloudimport.Prop) bool { return p.Name == "Connectors" })
	connectors, _ := objectOf(raw)
	for _, e := range connectors {
		conn, _ := objectOf(e.Value)
		props, _ := objectOf(conn.get("Properties"))
		at := jsonPointer("Resources", h.id, "Connectors", e.Key)
		r.connectorRead(h, []string{h.n.ID}, props.get("Destination"), at, "the connector "+e.Key+" of "+h.id+
			" at "+at, byID)
	}
}

// connectorRead draws the edges of a connector written at the JSON pointer
// at of h's template, named as what names it, from each node of sources to
// each node destination names, and reports it.
func (r *reader) connectorRead(h home, sources []string, destination json.RawMessage, at, what string,
	byID map[string]*cloudimport.Node) {
	destinations := r.connectorEnds(h.s, destination)
	edge := "the draft connects nothing for it, as it names no resource of the template as both its source and a " +
		"destination"
	if len(sources) > 0 && len(destinations) > 0 {
		connect(byID, sources, destinations)
		edge = connects(sources, destinations)
	}
	r.warnAt(cloudimport.CodeResourceNotCarried, h.s.t.file, at, "the template "+h.s.t.file+" writes "+what+
		", whose permissions SAM writes into IAM policies when the stack deploys, which the draft does not carry, "+
		"and nothing is drafted for it; "+edge+": grant the permissions in the policies the operator supplies")
}

// connectorEnds returns the IDs of the nodes raw, a connector's Source or
// Destination of a resource of s's template, names: one object or a list of
// objects, each naming a resource by its logical ID as its Id; a resource
// read as its template's resources names each node that template
// contributes. An end named otherwise, by its ARN, names no node.
func (r *reader) connectorEnds(s *stack, raw json.RawMessage) []string {
	ends := []json.RawMessage{raw}
	if items, isList := itemsOf(raw); isList {
		ends = items
	}
	var out []string
	for _, end := range ends {
		o, _ := objectOf(end)
		id, ok := schemadraft.JSONText(o.get("Id"))
		if !ok {
			continue
		}
		for _, dep := range r.dependencies(s, []string{id}) {
			if r.isNode(dep) {
				out = addNew(out, dep)
			}
		}
	}
	return out
}

// events reads each event the serverless function or state machine h writes
// under its Events as [reader.serverless] says, and leaves them out of its
// body.
func (r *reader) events(h home, byID map[string]*cloudimport.Node) {
	res, _ := objectOf(h.s.t.resources.get(h.id))
	props, _ := objectOf(res.get("Properties"))
	raw := props.get("Events")
	if raw == nil || isNull(raw) {
		return
	}
	h.n.Body = withoutProperty(h.n.Body, "Events")
	file := h.s.t.file
	at := jsonPointer("Resources", h.id, "Properties", "Events")
	events, isObject := objectOf(raw)
	if !isObject || isIntrinsic(events) {
		r.warnAt(cloudimport.CodeResourceNotCarried, file, at, fmt.Sprintf("the template %s writes the Events of %s "+
			"(%s) at %s as no object of events by their names, so no event is read and none is carried: write each "+
			"event under its name", file, h.id, h.n.Type, at))
		return
	}
	for _, e := range events {
		r.event(h, e.Key, e.Value, at+jsonPointer(e.Key), byID)
	}
}

// event reads the event name the serverless function or state machine h
// writes as raw at the JSON pointer at, as [reader.serverless] says.
func (r *reader) event(h home, name string, raw json.RawMessage, at string, byID map[string]*cloudimport.Node) {
	file := h.s.t.file
	written := fmt.Sprintf("the template %s writes the event %s of %s (%s) at %s", file, name, h.id, h.n.Type, at)
	o, _ := objectOf(raw)
	typ, typed := schemadraft.JSONText(o.get("Type"))
	if !typed || typ == "" {
		r.warnAt(cloudimport.CodeResourceNotCarried, file, at, written+", declaring no Type as text, which SAM "+
			"refuses when the stack deploys, so the event is not carried: write the event's Type")
		return
	}
	k := eventKindOf(typ)
	props, _ := objectOf(o.get("Properties"))
	writes, fix, edge := k.writes, "write what invokes "+h.n.ID+" by hand where the stack deploys", ""
	switch {
	case k.reads != "":
		if read := r.eventReads(h, props.get(k.reads), at+jsonPointer("Properties", k.reads)); len(read) > 0 {
			connect(byID, []string{h.n.ID}, read)
			edge = "; " + connects([]string{h.n.ID}, read)
		}
	case k.via != "":
		api := props.get(k.via)
		if api == nil || isNull(api) {
			writes, fix = k.implicit, "declare the API as a resource of the template, name it in "+k.via+", and "+
				"write the route in its definition"
			break
		}
		fix = "write the route, and the permission letting the API invoke " + h.n.ID + ", in the API's definition " +
			"by hand"
		if apis := r.eventReads(h, api, at+jsonPointer("Properties", k.via)); len(apis) > 0 {
			connect(byID, apis, []string{h.n.ID})
			edge = "; " + connects(apis, []string{h.n.ID})
		}
	}
	r.warnAt(cloudimport.CodeResourceNotCarried, file, at, written+", "+writes+" when the stack deploys, which no "+
		"instance of the draft writes, so the event is not carried"+edge+": "+fix)
}

// eventReads returns the IDs of the nodes, other than h's own, the value raw
// of an event of h, written at the JSON pointer at, references, lowered in
// h's template; what lowering it reports is dropped, as the event is not
// carried.
func (r *reader) eventReads(h home, raw json.RawMessage, at string) []string {
	if raw == nil {
		return nil
	}
	l, _ := r.scope(h.s).trial()
	p, present := l.prop(raw, at)
	if !present {
		return nil
	}
	var out []string
	held := &cloudimport.Graph{Nodes: []*cloudimport.Node{{ID: h.n.ID, Body: []cloudimport.Prop{p}}}}
	for _, e := range cloudimport.Edges(held) {
		if e.Kind == cloudimport.EdgeRef && e.To != h.n.ID && r.isNode(e.To) {
			out = addNew(out, e.To)
		}
	}
	return out
}

// withoutProperty returns body with the member named name of its Properties
// left out; Properties left with no member is the empty object.
func withoutProperty(body []cloudimport.Prop, name string) []cloudimport.Prop {
	out := slices.Clone(body)
	for i, p := range out {
		if p.Name != "Properties" || len(p.Props) == 0 {
			continue
		}
		p.Props = slices.DeleteFunc(slices.Clone(p.Props), func(m cloudimport.Prop) bool { return m.Name == name })
		if len(p.Props) == 0 {
			p.Props, p.Value = nil, literal(json.RawMessage("{}"))
		}
		out[i] = p
	}
	return out
}
