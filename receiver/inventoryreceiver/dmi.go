// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package inventoryreceiver

import (
	"regexp"
	"strconv"
	"strings"
)

// board, bios and system build the three firmware series. A series with no
// data besides its identity is not returned.
func (d dmiFacts) board() (fact, bool) {
	f := newFact(metricBoard)
	f.setStr(attrVendor, d.boardVendor)
	f.setStr(attrModel, d.boardName)
	f.setStr(attrVersion, d.boardVersion)
	f.setStr(attrSerial, d.boardSerial)
	f.setStr(attrAssetTag, d.boardAsset)
	return f, f.hasData()
}

func (d dmiFacts) bios() (fact, bool) {
	f := newFact(metricBIOS)
	f.setStr(attrVendor, d.biosVendor)
	f.setStr(attrFirmware, d.biosVersion)
	f.setStr(attrBIOSDate, d.biosDate)
	return f, f.hasData()
}

func (d dmiFacts) system() (fact, bool) {
	f := newFact(metricSystem)
	f.setStr(attrVendor, d.sysVendor)
	f.setStr(attrModel, d.productName)
	f.setStr(attrVersion, d.productVersion)
	f.setStr(attrSerial, d.productSerial)
	f.setStr(attrSystemFamily, d.productFamily)
	f.setStr(attrChassisVendor, d.chassisVendor)
	f.setStr(attrChassisType, d.chassisType)
	f.setStr(attrChassisSerial, d.chassisSerial)
	f.setStr(attrChassisAssetTag, d.chassisAsset)
	return f, f.hasData()
}

func (d dmiFacts) addFacts(inv *inventory) {
	for _, build := range []func() (fact, bool){d.board, d.bios, d.system} {
		if f, ok := build(); ok {
			inv.facts = append(inv.facts, f)
		}
	}
}

// memoryFact falls back to the SMBIOS handle as hw.id when the module has no slot label.
func memoryFact(m memModule) fact {
	f := newFact(metricMemory)
	id := m.locator
	if id == "" {
		id = "handle/0x" + strconv.FormatUint(uint64(m.handle), 16)
	}
	f.attrs[attrID] = id
	f.setStr(attrVendor, m.manufacturer)
	f.setStr(attrModel, m.part)
	f.setStr(attrSerial, m.serial)
	f.setStr(attrAssetTag, m.asset)
	f.setStr(attrMemType, m.memType)
	f.setStr(attrMemForm, m.formFactor)
	f.setStr(attrMemBank, m.bank)
	f.setNum(attrMemSize, int64(min(m.size, 1<<62)))
	f.setNum(attrMemSpeed, int64(m.speed))
	f.setNum(attrMemConfigured, int64(m.configured))
	return f
}

// memoryTotalFact builds the one series of the host's physical RAM; like board and BIOS it has no hw.id.
func memoryTotalFact(bytes int64) fact {
	f := newFact(metricMemoryTotal)
	f.setNum(attrMemTotal, bytes)
	return f
}

// matroxServerVideo are the Matrox G200e devices (vendor 102b) that server boards use as the
// video output of the BMC.
var matroxServerVideo = map[uint64]bool{0x0522: true, 0x0524: true, 0x0530: true, 0x0532: true, 0x0533: true, 0x0534: true, 0x0536: true}

// isBMCVideo tells the video controller a server board wires to its BMC: any ASPEED (1a03) device,
// a Matrox G200e server variant, or a device bound to the driver of those. It is a display for the
// remote console, not a GPU of the host.
func isBMCVideo(vendor, device uint64, driver any) bool {
	if d, _ := driver.(string); d == "ast" || d == "mgag200" {
		return true
	}
	return vendor == 0x1a03 || (vendor == 0x102b && matroxServerVideo[device])
}

var xenWord = regexp.MustCompile(`\b` + `xen` + `\b`)

// virtualMarkers are lower-case substrings of the firmware vendor and product
// strings that only a virtual machine carries.
var virtualMarkers = []string{
	"qemu", "kvm", "vmware", "virtualbox", "innotek", "bochs", "parallels", "bhyve", "hyper-v",
	"virtual machine", "openstack", "google compute engine", "digitalocean", "alibaba cloud", "hvm domu",
	"seabios", "ovirt", "rhev", "oraclecloud", "cloud hypervisor",
}

// virtualFromDMI tells from the firmware strings whether the host is a virtual
// machine. An Amazon bare-metal type is physical.
func virtualFromDMI(sysVendor, product, boardVendor, biosVendor string) (bool, string) {
	all := strings.ToLower(strings.Join([]string{sysVendor, product, boardVendor, biosVendor}, "|"))
	for _, m := range virtualMarkers {
		if strings.Contains(all, m) {
			return true, "firmware strings name a hypervisor"
		}
	}
	if xenWord.MatchString(all) {
		return true, "firmware strings name a hypervisor"
	}
	if strings.Contains(strings.ToLower(sysVendor), "amazon ec2") && !strings.Contains(strings.ToLower(product), ".metal") {
		return true, "firmware strings name a cloud instance"
	}
	return false, ""
}
