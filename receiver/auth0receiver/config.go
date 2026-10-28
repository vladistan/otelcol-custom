// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package auth0receiver

import (
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/collector/component"
)

const (
	maxWaitTimeSeconds     = 20
	maxNumberOfMessagesCap = 10
)

// Config holds no credential keys; credentials come from the AWS default chain.
type Config struct {
	QueueURL            string        `mapstructure:"queue_url"`
	Region              string        `mapstructure:"region"`
	Endpoint            string        `mapstructure:"endpoint"`
	RoleARN             string        `mapstructure:"role_arn"`
	ExternalID          string        `mapstructure:"external_id"`
	SourcesSuffix       []string      `mapstructure:"sources_suffix"`
	WaitTimeSeconds     int           `mapstructure:"wait_time_seconds"`
	MaxNumberOfMessages int           `mapstructure:"max_number_of_messages"`
	ErrorBackoff        time.Duration `mapstructure:"error_backoff"`
}

func (cfg *Config) Validate() error {
	var errs []error
	if cfg.QueueURL == "" {
		errs = append(errs, errors.New("queue_url is required"))
	}
	if cfg.Region == "" {
		errs = append(errs, errors.New("region is required"))
	}
	if cfg.WaitTimeSeconds < 0 || cfg.WaitTimeSeconds > maxWaitTimeSeconds {
		errs = append(errs, fmt.Errorf("wait_time_seconds must be 0..%d, got %d", maxWaitTimeSeconds, cfg.WaitTimeSeconds))
	}
	if cfg.MaxNumberOfMessages < 1 || cfg.MaxNumberOfMessages > maxNumberOfMessagesCap {
		errs = append(errs, fmt.Errorf("max_number_of_messages must be 1..%d, got %d", maxNumberOfMessagesCap, cfg.MaxNumberOfMessages))
	}
	if cfg.ErrorBackoff < 0 {
		errs = append(errs, errors.New("error_backoff must not be negative"))
	}
	return errors.Join(errs...)
}

var _ component.Config = (*Config)(nil)
