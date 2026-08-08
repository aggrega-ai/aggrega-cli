package errors

type Code string

type Meta struct{ Message string }

func Register(code Code, meta Meta) struct{} { return struct{}{} }
