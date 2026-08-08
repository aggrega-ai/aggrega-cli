package errors

// The fixture's stand-in for the package that declares the identity type. Only
// its import path matters to the scan, which recognises a declaration by the
// package it is qualified with.

type Code string

type Meta struct{ Message string }

func Register(code Code, meta Meta) struct{} { return struct{}{} }
