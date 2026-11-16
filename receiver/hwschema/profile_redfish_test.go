// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwschema

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedfishProfileForDell(t *testing.T) {
	p, ok := RedfishProfileFor("dell")

	require.True(t, ok)
	assert.Equal(t, "dell", p.Vendor)
	assert.Equal(t, "/redfish/v1/Systems/System.Embedded.1", p.System)
	assert.Equal(t, "/redfish/v1/Chassis/System.Embedded.1", p.Chassis)
	assert.Equal(t, "/redfish/v1/Managers/iDRAC.Embedded.1", p.Manager)
}

func TestRedfishProfileForAnUnknownVendor(t *testing.T) {
	_, ok := RedfishProfileFor("acme")

	assert.False(t, ok)
}

func TestRedfishProfileSubPaths(t *testing.T) {
	p, _ := RedfishProfileFor("dell")

	assert.Equal(t, "/redfish/v1/Chassis/System.Embedded.1/Thermal", p.Thermal())
	assert.Equal(t, "/redfish/v1/Chassis/System.Embedded.1/ThermalSubsystem", p.ThermalSubsystem())
	assert.Equal(t, "/redfish/v1/Chassis/System.Embedded.1/Power", p.Power())
	assert.Equal(t, "/redfish/v1/Chassis/System.Embedded.1/PowerSubsystem", p.PowerSubsystem())
	assert.Equal(t, "/redfish/v1/Systems/System.Embedded.1/Storage", p.Storage())
	assert.Equal(t, "/redfish/v1/Systems/System.Embedded.1/Memory", p.Memory())
	assert.Equal(t, "/redfish/v1/Systems/System.Embedded.1/Processors", p.Processors())
}

func TestRedfishResourceNames(t *testing.T) {
	assert.ElementsMatch(t, []string{"system", "thermal", "power", "storage", "memory", "processors", "bmc"}, RedfishResources())
	assert.True(t, IsRedfishResource("thermal"))
	assert.False(t, IsRedfishResource("gpu"))
}
