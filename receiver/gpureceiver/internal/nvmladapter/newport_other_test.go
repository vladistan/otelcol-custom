//go:build !(linux && cgo)

package nvmladapter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewPortReportsPlatformUnsupported(t *testing.T) {
	p, err := NewPort()

	assert.Nil(t, p)
	assert.ErrorIs(t, err, ErrPlatformUnsupported)
}
