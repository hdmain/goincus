package service

import "errors"

var (
	// ErrNotFound indicates a missing resource.
	ErrNotFound = errors.New("not found")
	// ErrInvalidInput indicates a bad client request.
	ErrInvalidInput = errors.New("invalid input")
	// ErrConflict indicates a uniqueness or allocation conflict.
	ErrConflict = errors.New("conflict")
)
