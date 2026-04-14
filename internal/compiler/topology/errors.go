package topology

import "errors"

var (
	ErrSubAggregateLoad = errors.New("sub-aggregate load failed")
	ErrInvalidUsesFormat = errors.New("invalid uses format")
	ErrMaxDepth          = errors.New("max sub-aggregate nesting depth exceeded")
)
