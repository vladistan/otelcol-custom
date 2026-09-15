// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package ansistrip provides a stanza operator that removes ANSI escape sequences from string fields.
// ANSI escape sequences are commonly used for terminal coloring (e.g., \x1b[92m for green text).
// This operator strips all ANSI sequences, making log messages cleaner for indexing and searching.
package ansistrip

import (
	"fmt"
	"reflect"

	"go.opentelemetry.io/collector/component"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/entry"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/helper"
)

const operatorType = "ansi_strip"

func init() {
	operator.Register(operatorType, func() operator.Builder { return NewConfig() })
}

// NewConfig creates a new ansi_strip operator config with default values
func NewConfig() *Config {
	return NewConfigWithID(operatorType)
}

// NewConfigWithID creates a new ansi_strip operator config with default values
func NewConfigWithID(operatorID string) *Config {
	return &Config{
		TransformerConfig: helper.NewTransformerConfig(operatorID, operatorType),
	}
}

// Config is the configuration of an ansi_strip operator.
// It removes ANSI escape sequences from the specified field.
type Config struct {
	helper.TransformerConfig `mapstructure:",squash"`

	// Field is the field to strip ANSI codes from.
	// Supports body fields like "MESSAGE" or full paths like "body.MESSAGE".
	Field entry.Field `mapstructure:"field"`
}

// Build will build an ansi_strip operator from the supplied configuration
func (c Config) Build(set component.TelemetrySettings) (operator.Operator, error) {
	transformerOperator, err := c.TransformerConfig.Build(set)
	if err != nil {
		return nil, err
	}

	// Check if field is empty/unset using reflection
	if reflect.ValueOf(c.Field).IsZero() {
		return nil, fmt.Errorf("ansi_strip: missing field")
	}

	return &Transformer{
		TransformerOperator: transformerOperator,
		Field:               c.Field,
	}, nil
}
