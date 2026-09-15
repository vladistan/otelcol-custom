// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package arraytostring provides a stanza operator that converts array fields to joined strings.
// This is needed because journald can store multi-value fields as arrays (e.g., when Docker
// containers log atomic multi-line entries), and downstream operators like regex_parser
// expect string values.
package arraytostring

import (
	"fmt"
	"reflect"

	"go.opentelemetry.io/collector/component"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/entry"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/helper"
)

const operatorType = "array_to_string"

func init() {
	operator.Register(operatorType, func() operator.Builder { return NewConfig() })
}

// NewConfig creates a new array_to_string operator config with default values
func NewConfig() *Config {
	return NewConfigWithID(operatorType)
}

// NewConfigWithID creates a new array_to_string operator config with default values
func NewConfigWithID(operatorID string) *Config {
	return &Config{
		TransformerConfig: helper.NewTransformerConfig(operatorID, operatorType),
		Delimiter:         "\n",
	}
}

// Config is the configuration of an array_to_string operator.
// It converts array fields to joined strings using the specified delimiter.
type Config struct {
	helper.TransformerConfig `mapstructure:",squash"`

	// Field is the field to convert from array to string.
	// Supports body fields like "MESSAGE" or full paths like "body.MESSAGE".
	Field entry.Field `mapstructure:"field"`

	// Delimiter is the string used to join array elements (default: "\n")
	Delimiter string `mapstructure:"delimiter"`
}

// Build will build an array_to_string operator from the supplied configuration
func (c Config) Build(set component.TelemetrySettings) (operator.Operator, error) {
	transformerOperator, err := c.TransformerConfig.Build(set)
	if err != nil {
		return nil, err
	}

	// Check if field is empty/unset using reflection
	if reflect.ValueOf(c.Field).IsZero() {
		return nil, fmt.Errorf("array_to_string: missing field")
	}

	delimiter := c.Delimiter
	if delimiter == "" {
		delimiter = "\n"
	}

	return &Transformer{
		TransformerOperator: transformerOperator,
		Field:               c.Field,
		Delimiter:           delimiter,
	}, nil
}
