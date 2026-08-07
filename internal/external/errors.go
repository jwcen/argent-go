package external

import "errors"

var (
	ErrNotFound      = errors.New("external: not found")
	ErrInvalidType   = errors.New("external: invalid asset type")
	ErrInvalidAction = errors.New("external: invalid action")
	ErrNotPending    = errors.New("external: action is not pending")
)
