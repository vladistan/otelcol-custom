package port

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKindSetMatchesContract(t *testing.T) {
	want := []string{
		"NotSupported", "GPULost", "LibraryNotFound", "DriverNotLoaded",
		"NoPermission", "VersionMismatch", "Other",
	}
	var got []string
	for _, k := range Kinds() {
		got = append(got, k.String())
	}
	assert.Equal(t, want, got)
}

func TestKindsAreDistinct(t *testing.T) {
	seen := map[Kind]bool{}
	for _, k := range Kinds() {
		assert.False(t, seen[k], "duplicate kind %v", k)
		seen[k] = true
	}
}

func TestKindOf(t *testing.T) {
	cause := errors.New("boom")
	tests := []struct {
		name string
		err  error
		want Kind
	}{
		{"nil", nil, 0},
		{"direct", NewError(GPULost, cause), GPULost},
		{"wrapped", fmt.Errorf("ctx: %w", NewError(NotSupported, cause)), NotSupported},
		{"foreign", cause, Other},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, KindOf(tt.err))
		})
	}
}

func TestErrorUnwrapAndText(t *testing.T) {
	cause := errors.New("lib missing")
	err := NewError(LibraryNotFound, cause)
	assert.ErrorIs(t, err, cause)
	assert.Equal(t, "LibraryNotFound: lib missing", err.Error())
	assert.Equal(t, "Other", NewError(Other, nil).Error())
	assert.Equal(t, "Kind(99)", Kind(99).String())
}
