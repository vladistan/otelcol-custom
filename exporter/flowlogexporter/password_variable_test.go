// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package flowlogexporter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// With password_variable set, the one warning for an empty password names that variable.
func TestStart_AnEmptyPassword_TheWarningNamesTheVariable(t *testing.T) {
	cfg := testConfig()
	cfg.Password = ""
	cfg.PasswordVariable = "FLOWLOG_PASSWORD" // pragma: allowlist secret
	r := started(t, cfg, &fakeConn{})

	got := warnings(r.logs)

	require.Equal(t, 1, countContaining(got, "password is empty"))
	assert.Equal(t, 1, countContaining(got, "FLOWLOG_PASSWORD"), "the warning names the variable")
	assert.Equal(t, map[string]int64{warnEmptyPassword: 1}, r.counter("otelcol_flowlog_config_warnings"))
}

// The name is also a field of the log entry, so a log filter can find it.
func TestStart_AnEmptyPassword_TheVariableIsAFieldOfTheEntry(t *testing.T) {
	cfg := testConfig()
	cfg.Password = ""
	cfg.PasswordVariable = "FLOWLOG_PASSWORD" // pragma: allowlist secret
	r := started(t, cfg, &fakeConn{})

	var fields []map[string]any
	for _, e := range r.logs.All() {
		if e.ContextMap()["variable"] != nil {
			fields = append(fields, e.ContextMap())
		}
	}

	require.Len(t, fields, 1)
	assert.Equal(t, "FLOWLOG_PASSWORD", fields[0]["variable"])
}

// Without the key the warning still points at the setting and says how to get the name into it.
func TestStart_AnEmptyPassword_WithoutTheKeyTheWarningPointsAtTheSettingAndTheKey(t *testing.T) {
	cfg := testConfig()
	cfg.Password = ""
	r := started(t, cfg, &fakeConn{})

	got := warnings(r.logs)

	require.Equal(t, 1, countContaining(got, "password is empty"))
	assert.Equal(t, 1, countContaining(got, "password_variable"))
}

// A password that is set raises no warning, whatever the variable key says.
func TestStart_APasswordWithTheVariableKey_RaisesNoWarning(t *testing.T) {
	cfg := testConfig()
	cfg.PasswordVariable = "FLOWLOG_PASSWORD" // pragma: allowlist secret
	r := started(t, cfg, &fakeConn{})

	assert.Zero(t, countContaining(warnings(r.logs), "password is empty"))
	assert.Empty(t, r.counter("otelcol_flowlog_config_warnings"))
}

// The key holds a variable name only. A text that is not a name is refused, and so is a value that
// equals the password, without printing it.
func TestValidate_PasswordVariableIsANameAndNeverThePassword(t *testing.T) {
	for _, bad := range []string{"has space", "1STARTS_WITH_DIGIT", "${env:X}", "a=b", "x;y"} {
		cfg := testConfig()
		cfg.PasswordVariable = bad // pragma: allowlist secret

		err := cfg.Validate()

		require.Error(t, err, bad)
		assert.Contains(t, err.Error(), "password_variable", bad)
	}
	for _, good := range []string{"FLOWLOG_PASSWORD", "_x", "A1"} {
		cfg := testConfig()
		cfg.PasswordVariable = good // pragma: allowlist secret

		assert.NoError(t, cfg.Validate(), good)
	}

	cfg := testConfig()
	cfg.Password = "Secret_Value_1" // pragma: allowlist secret
	// a password pasted into the name key
	cfg.PasswordVariable = "Secret_Value_1" // pragma: allowlist secret

	err := cfg.Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "password_variable")
	assert.NotContains(t, err.Error(), "Secret_Value_1", "the error never prints the value")
}
