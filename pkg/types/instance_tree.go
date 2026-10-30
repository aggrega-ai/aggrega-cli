// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

// InstanceTreeShape maps one container type onto the three axes a component walk
// reads: the child containers nested inside it, the component instances it holds
// directly, and whether the compiler synthesized it rather than the author
// writing it.
//
// Two container types carry that shape — [Subsystem] holding [ComponentInstance]
// in the Aggregate, and [Boundary] holding [BoundaryComponent] in the Release —
// and [SubsystemTree] and [BoundaryTree] are their descriptors. Only the field
// names and the element types differ between them, which is why one walk serves
// both.
//
// The shape is a struct of accessors rather than an interface so that a container
// type needs no methods to be walked, and so that the field-to-axis mapping for a
// tree is written once in one named place.
type InstanceTreeShape[C any, I any] struct {
	// Children returns the containers nested directly inside this one, in author
	// order.
	Children func(C) OrderedMap[C]

	// Instances returns the component instances this container holds directly, in
	// author order.
	Instances func(C) OrderedMap[I]

	// Synthesized reports whether the compiler created this container rather than
	// the author declaring it.
	Synthesized func(C) bool
}

// SubsystemTree describes the Aggregate's subsystem tree — [Subsystem]
// containers holding [ComponentInstance] components.
func SubsystemTree() InstanceTreeShape[Subsystem, ComponentInstance] {
	return InstanceTreeShape[Subsystem, ComponentInstance]{
		Children:    func(s Subsystem) OrderedMap[Subsystem] { return s.Subsystems },
		Instances:   func(s Subsystem) OrderedMap[ComponentInstance] { return s.Components },
		Synthesized: func(s Subsystem) bool { return s.Synthesized },
	}
}

// BoundaryTree describes the Release's boundary tree — [Boundary] containers
// holding [BoundaryComponent] components.
//
// A boundary names its components on two axes: directly, and by routing a whole
// sub-module in through [Boundary.Aggregates]. Both are components of this
// boundary, so the instance axis is the reconciled set [BoundaryComponents]
// returns, keyed by the id each component carries in the flattened graph. A
// consumer of this walk therefore sees a routed component exactly as it sees a
// directly-named one, and gets the routed axis without knowing it exists.
//
// A routed sub-module's components are known only once the module's document is
// loaded, so an entry contributes by itself only the components it names. A
// build loads the module before it reads this tree and names every other
// component under the routing entry, with an empty body, so the walk reaches it
// like any other routed component; a resolve that loads no module leaves the
// entry as written, and the components it does not name are then placed only
// later, keyed on the sub-module each node came from rather than on a name this
// tree could supply.
func BoundaryTree() InstanceTreeShape[Boundary, BoundaryComponent] {
	return InstanceTreeShape[Boundary, BoundaryComponent]{
		Children:    func(b Boundary) OrderedMap[Boundary] { return b.Boundaries },
		Instances:   BoundaryComponents,
		Synthesized: func(b Boundary) bool { return b.Synthesized },
	}
}

// InstanceTreeVisitor carries the callbacks [WalkInstanceTree] invokes as it
// descends. Every field is optional; a nil callback is skipped.
//
// The accumulator type A is the caller's own per-container state — an inherited
// annotation cascade, an accumulated feature list, anything a container must hand
// to its components and to its children. [InstanceTreeVisitor.Enter] produces a
// container's accumulator from its parent's, so state flows outer-to-inner and
// never sideways between siblings.
//
// The walk mutates nothing. A visitor that needs to write — into a map it owns, a
// slice it appends to, or back into the tree it is walking — closes over its own
// state and writes there.
type InstanceTreeVisitor[C any, I any, A any] struct {
	// Path composes a container's path from its parent's path, its own name, and
	// whether it is synthesized. The walk never interprets the result: it hands the
	// composed string to the other callbacks and passes it down as the parent path
	// of the container's children.
	//
	// Path composition is the caller's because the authoring locator a diagnostic
	// needs, the dotted scope path a rendered artifact needs, and the
	// slash-separated boundary path an output directory needs are all different
	// spellings of the same descent. Leaving it here keeps each spelling in one
	// place instead of open-coding it per walk.
	//
	// When nil, every container's path is the root path unchanged.
	Path func(parentPath, name string, synthesized bool) string

	// Enter runs on a container before any of its component instances, and returns
	// the accumulator its instances and its children receive. The argument is the
	// parent's accumulator — the root path's accumulator for a top-level container.
	//
	// It runs before the instance loop because a container's own contribution to
	// the state its components read must be in place before they are visited, and
	// it runs whether or not the container holds any components — a container
	// carrying only features and no components still enters.
	//
	// When nil, the parent's accumulator flows down unchanged.
	Enter func(container C, name, path string, parent A) A

	// Visit runs once per component instance the container holds directly, in
	// author order, with the container's path, the container's synthesized flag,
	// and the container's accumulator.
	//
	// A non-nil error aborts the whole walk at once: no further instance, no
	// further container, and the error surfaces from [WalkInstanceTree] unwrapped.
	Visit func(instance I, name, path string, synthesized bool, acc A) error

	// Leave runs on a container after its component instances and before its child
	// containers. It is where container-level work that must observe the whole
	// instance loop belongs, and keeping it ordered there is what makes a walk's
	// emission order independent of whether it uses this walk or open-codes the
	// descent.
	Leave func(container C, name, path string, acc A)
}

// JoinSubsystemPath composes the dotted path of a subsystem from its parent's
// dotted path and its own name: "a" for a top-level subsystem, "a.b" for a
// subsystem nested inside it. It is the spelling
// [ComponentInstanceRef.SubsystemPath] carries, and the one a rendered artifact's
// scope path is built from.
//
// The synthesized flag is accepted so the function is usable directly as an
// [InstanceTreeVisitor.Path]; the dotted path does not vary with it, because a
// synthesized container is named like any other and consumers that must render it
// differently key on the flag rather than on the path string.
func JoinSubsystemPath(parentPath, name string, _ bool) string {
	if parentPath == "" {
		return name
	}
	return parentPath + "." + name
}

// WalkInstanceTree descends a tree of containers holding named component
// instances, invoking the visitor's callbacks at each level.
//
// Per container the order is fixed: compose the path, Enter, every component
// instance in author order, Leave, then each child container in author order —
// each child receiving the container's path as its parent path and the container's
// accumulator as its parent accumulator. Roots are walked in the author order of
// the supplied map, each with rootPath as its parent path and acc as its parent
// accumulator.
//
// Every level iterates an [OrderedMap], so the traversal is in author order
// throughout and no step depends on Go's map iteration. That is what lets a
// consumer feed rendered output straight from the walk: identical input yields an
// identical visit sequence, and therefore byte-identical output.
//
// The walk applies no sort. A consumer that wants a different order sorts what it
// collected.
//
// Returns the first error a Visit callback returns, or nil.
func WalkInstanceTree[C any, I any, A any](
	roots OrderedMap[C],
	shape InstanceTreeShape[C, I],
	rootPath string,
	acc A,
	visitor InstanceTreeVisitor[C, I, A],
) error {
	for name, container := range roots.All() {
		if err := walkInstanceContainer(container, name, rootPath, acc, shape, visitor); err != nil {
			return err
		}
	}
	return nil
}

func walkInstanceContainer[C any, I any, A any](
	container C,
	name, parentPath string,
	parent A,
	shape InstanceTreeShape[C, I],
	visitor InstanceTreeVisitor[C, I, A],
) error {
	synthesized := shape.Synthesized(container)

	path := parentPath
	if visitor.Path != nil {
		path = visitor.Path(parentPath, name, synthesized)
	}

	acc := parent
	if visitor.Enter != nil {
		acc = visitor.Enter(container, name, path, parent)
	}

	if visitor.Visit != nil {
		instances := shape.Instances(container)
		for instanceName, instance := range instances.All() {
			if err := visitor.Visit(instance, instanceName, path, synthesized, acc); err != nil {
				return err
			}
		}
	}

	if visitor.Leave != nil {
		visitor.Leave(container, name, path, acc)
	}

	children := shape.Children(container)
	for childName, child := range children.All() {
		if err := walkInstanceContainer(child, childName, path, acc, shape, visitor); err != nil {
			return err
		}
	}
	return nil
}
