// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwschema

import "slices"

// RedfishProfile lists the Redfish paths of one vendor and generation. The
// receiver reads the paths of the profile; a path the BMC does not serve is
// marked absent for that BMC and skipped from then on.
type RedfishProfile struct {
	Vendor     string
	Generation string
	System     string
	Chassis    string
	Manager    string
}

// Paths below the chassis and the system. A newer BMC serves the *Subsystem
// resources; the older Thermal and Power are the fallback.
func (p RedfishProfile) Thermal() string          { return p.Chassis + "/Thermal" }
func (p RedfishProfile) ThermalSubsystem() string { return p.Chassis + "/ThermalSubsystem" }
func (p RedfishProfile) Power() string            { return p.Chassis + "/Power" }
func (p RedfishProfile) PowerSubsystem() string   { return p.Chassis + "/PowerSubsystem" }
func (p RedfishProfile) Storage() string          { return p.System + "/Storage" }
func (p RedfishProfile) Memory() string           { return p.System + "/Memory" }
func (p RedfishProfile) Processors() string       { return p.System + "/Processors" }

var redfishProfiles = map[string]RedfishProfile{
	"dell": {
		Vendor:     "dell",
		Generation: "idrac9",
		System:     "/redfish/v1/Systems/System.Embedded.1",
		Chassis:    "/redfish/v1/Chassis/System.Embedded.1",
		Manager:    "/redfish/v1/Managers/iDRAC.Embedded.1",
	},
}

// RedfishProfileFor returns the profile of a vendor.
func RedfishProfileFor(vendor string) (RedfishProfile, bool) {
	p, ok := redfishProfiles[vendor]
	return p, ok
}

var redfishResources = []string{"system", "thermal", "power", "storage", "memory", "processors", "bmc"}

// RedfishResources lists the resource groups a target can select; the default is all.
func RedfishResources() []string { return slices.Clone(redfishResources) }

// IsRedfishResource reports whether name is a resource group.
func IsRedfishResource(name string) bool { return slices.Contains(redfishResources, name) }
