// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package auth0receiver

import "testing"

func validConfig() *Config {
	cfg := NewFactory().CreateDefaultConfig().(*Config)
	cfg.QueueURL = "https://sqs.eu-central-1.amazonaws.com/111122223333/auth0-events"
	cfg.Region = "eu-central-1"
	return cfg
}

func TestValidateAcceptsMinimalConfig(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("minimal config rejected: %v", err)
	}
}

func TestValidateAcceptsOptionalRoleFields(t *testing.T) {
	cfg := validConfig()
	cfg.RoleARN = "arn:aws:iam::111122223333:role/auth0-reader"
	cfg.ExternalID = "ext-id"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("role config rejected: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	tests := map[string]func(*Config){
		"missing queue_url":      func(c *Config) { c.QueueURL = "" },
		"missing region":         func(c *Config) { c.Region = "" },
		"wait_time below range":  func(c *Config) { c.WaitTimeSeconds = -1 },
		"wait_time above range":  func(c *Config) { c.WaitTimeSeconds = 21 },
		"max_messages below":     func(c *Config) { c.MaxNumberOfMessages = 0 },
		"max_messages above":     func(c *Config) { c.MaxNumberOfMessages = 11 },
		"negative error_backoff": func(c *Config) { c.ErrorBackoff = -1 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			mutate(cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate accepted an invalid config")
			}
		})
	}
}

func TestValidateAcceptsRangeEdges(t *testing.T) {
	for _, wait := range []int{0, 20} {
		for _, max := range []int{1, 10} {
			cfg := validConfig()
			cfg.WaitTimeSeconds, cfg.MaxNumberOfMessages = wait, max
			if err := cfg.Validate(); err != nil {
				t.Fatalf("wait=%d max=%d rejected: %v", wait, max, err)
			}
		}
	}
}
