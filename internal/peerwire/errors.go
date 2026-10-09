package peerwire

import "errors"

// ErrMalformedFrame reports a received peer-wire frame or message payload that
// violates the wire format.
//
// It does not classify I/O failures, invalid local
// arguments, or well-formed messages that violate session state.
var ErrMalformedFrame = errors.New("invalid peer message")
