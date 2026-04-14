package analyzer

import "errors"

var (
	ErrSelectorViolation = errors.New("selector violation")
	ErrCatalogLookup     = errors.New("catalog lookup failed")
)
