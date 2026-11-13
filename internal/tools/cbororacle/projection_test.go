// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cbororacle

import (
	"reflect"
	"strings"
	"sync"

	abi "github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
)

// The reference encoder walks a value by reflection, and an order-keeping
// container is a struct whose entries sit behind unexported state: handed one
// as it is, the encoder sees no exported field and writes an empty map, silently,
// whatever the container holds. So it is handed a VIEW instead, built here, in
// which every order-keeping container is the list of its entries in the order it
// holds — each a two-element array of the key and the value — and every struct
// that holds one, at any depth, is rebuilt as a struct with the same field names
// and the same tags around the viewed field. The reference encoder then writes the
// order-preserving array form natively, as it writes any list, and the comparison
// with the generated codec stays a comparison with an encoder that codec had no
// hand in.
//
// The view reads each container only through its exported methods, Keys and Get,
// so it observes exactly the order a guest observes. It is built for the reference
// encoder and for nothing else: no generated codec reads it.

var (
	viewMu    sync.Mutex
	viewTypes = map[reflect.Type]reflect.Type{}
	building  = map[reflect.Type]bool{}

	orderedMapPkg = reflect.TypeOf(abi.OrderedMap[string]{}).PkgPath()
	entryType     = reflect.TypeOf([2]any{})
	entryList     = reflect.SliceOf(entryType)
)

// referenceView returns v as the reference encoder must be handed it.
func referenceView(v any) any {
	return viewValue(reflect.ValueOf(v)).Interface()
}

// isOrderKeeping reports whether t is an instantiation of the plugin binding's
// order-keeping container.
func isOrderKeeping(t reflect.Type) bool {
	return t.Kind() == reflect.Struct && t.PkgPath() == orderedMapPkg && strings.HasPrefix(t.Name(), "OrderedMap[")
}

// holdsOrderKeeping reports whether a value of type t holds an order-keeping
// container at any depth. A type reached again while it is being decided is a
// recursion through a pointer and is answered false for that path, which cannot
// change the answer: whatever the recursion holds, the outer visit of the same
// type finds through its other fields. Nothing is memoized, because an answer
// computed with a recursion cut short is only right for the outermost type asked.
func holdsOrderKeeping(t reflect.Type, visiting map[reflect.Type]bool) bool {
	if visiting[t] {
		return false
	}
	visiting[t] = true
	defer delete(visiting, t)
	switch {
	case isOrderKeeping(t):
		return true
	case t.Kind() == reflect.Pointer, t.Kind() == reflect.Slice, t.Kind() == reflect.Array, t.Kind() == reflect.Map:
		return holdsOrderKeeping(t.Elem(), visiting)
	case t.Kind() == reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if holdsOrderKeeping(t.Field(i).Type, visiting) {
				return true
			}
		}
	}
	return false
}

// viewType returns the type a value of type t is viewed as.
func viewType(t reflect.Type) reflect.Type {
	viewMu.Lock()
	defer viewMu.Unlock()
	return viewTypeLocked(t)
}

func viewTypeLocked(t reflect.Type) reflect.Type {
	if vt, ok := viewTypes[t]; ok {
		return vt
	}
	// A struct built by reflection cannot refer to itself, so a recursive type
	// that holds an order-keeping container has no view. No payload is one; this
	// names the type if one ever is, rather than recursing without end.
	if building[t] {
		panic("cbororacle: " + t.String() + " is recursive and holds an order-keeping container, so the reference view cannot be built for it")
	}
	building[t] = true
	defer delete(building, t)
	vt := t
	switch {
	case !holdsOrderKeeping(t, map[reflect.Type]bool{}):
	case isOrderKeeping(t):
		vt = entryList
	case t.Kind() == reflect.Pointer:
		vt = reflect.PointerTo(viewTypeLocked(t.Elem()))
	case t.Kind() == reflect.Slice:
		vt = reflect.SliceOf(viewTypeLocked(t.Elem()))
	case t.Kind() == reflect.Map:
		vt = reflect.MapOf(t.Key(), viewTypeLocked(t.Elem()))
	case t.Kind() == reflect.Struct:
		fields := make([]reflect.StructField, t.NumField())
		for i := range fields {
			f := t.Field(i)
			fields[i] = reflect.StructField{Name: f.Name, Type: viewTypeLocked(f.Type), Tag: f.Tag}
		}
		vt = reflect.StructOf(fields)
	}
	viewTypes[t] = vt
	return vt
}

// viewValue returns v viewed. A value whose type holds no order-keeping container
// is returned as it is, so everything the view does not need to change reaches
// the reference encoder untouched.
func viewValue(v reflect.Value) reflect.Value {
	vt := viewType(v.Type())
	if vt == v.Type() {
		return v
	}
	switch {
	case isOrderKeeping(v.Type()):
		keys := v.MethodByName("Keys").Call(nil)[0].Interface().([]string)
		get := v.MethodByName("Get")
		// Never nil, so an empty container is an empty list and not the encoder's
		// null: an empty required container is on the wire as an empty array.
		out := reflect.MakeSlice(entryList, 0, len(keys))
		for _, k := range keys {
			value := get.Call([]reflect.Value{reflect.ValueOf(k)})[0]
			out = reflect.Append(out, reflect.ValueOf([2]any{k, viewValue(value).Interface()}))
		}
		return out
	case v.Kind() == reflect.Pointer:
		if v.IsNil() {
			return reflect.Zero(vt)
		}
		p := reflect.New(vt.Elem())
		p.Elem().Set(viewValue(v.Elem()))
		return p
	case v.Kind() == reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(vt)
		}
		out := reflect.MakeSlice(vt, v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(viewValue(v.Index(i)))
		}
		return out
	case v.Kind() == reflect.Map:
		if v.IsNil() {
			return reflect.Zero(vt)
		}
		out := reflect.MakeMapWithSize(vt, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), viewValue(iter.Value()))
		}
		return out
	default: // a struct that holds an order-keeping container
		out := reflect.New(vt).Elem()
		for i := 0; i < v.NumField(); i++ {
			out.Field(i).Set(viewValue(v.Field(i)))
		}
		return out
	}
}
