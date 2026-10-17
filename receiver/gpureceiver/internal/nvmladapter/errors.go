// Package nvmladapter wraps go-nvml behind the port contract on linux with
// cgo; every other build gets a constructor that reports the platform as
// unsupported.
package nvmladapter

import "errors"

// ErrPlatformUnsupported is returned by NewPort on builds without linux and cgo.
var ErrPlatformUnsupported = errors.New("gpu receiver: platform unsupported, NVML needs linux with cgo")
