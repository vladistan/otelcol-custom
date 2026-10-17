// Package port is the domain-typed boundary to NVML. It imports no go-nvml
// types, so it compiles with CGO_ENABLED=0.
package port

import (
	"errors"
	"fmt"
)

// Kind classifies a failed NVML call.
type Kind int

// Error kinds (C-6). Init returns LibraryNotFound, DriverNotLoaded,
// NoPermission, VersionMismatch or Other. Per-metric calls return
// NotSupported, GPULost or Other.
const (
	NotSupported Kind = iota + 1
	GPULost
	LibraryNotFound
	DriverNotLoaded
	NoPermission
	VersionMismatch
	Other
)

var kindNames = map[Kind]string{
	NotSupported:    "NotSupported",
	GPULost:         "GPULost",
	LibraryNotFound: "LibraryNotFound",
	DriverNotLoaded: "DriverNotLoaded",
	NoPermission:    "NoPermission",
	VersionMismatch: "VersionMismatch",
	Other:           "Other",
}

// Kinds returns every error kind in declaration order.
func Kinds() []Kind {
	return []Kind{
		NotSupported, GPULost, LibraryNotFound, DriverNotLoaded,
		NoPermission, VersionMismatch, Other,
	}
}

func (k Kind) String() string {
	if name, ok := kindNames[k]; ok {
		return name
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Error is the only error type adapters return; callers branch on Kind and
// log Cause once.
type Error struct {
	Kind  Kind
	Cause error
}

func (e *Error) Error() string {
	if e.Cause == nil {
		return e.Kind.String()
	}
	return e.Kind.String() + ": " + e.Cause.Error()
}

func (e *Error) Unwrap() error { return e.Cause }

func NewError(kind Kind, cause error) *Error {
	return &Error{Kind: kind, Cause: cause}
}

// KindOf returns the Kind of err, or Other when err carries none.
// A nil err returns the zero Kind.
func KindOf(err error) Kind {
	if err == nil {
		return 0
	}
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Kind
	}
	return Other
}
