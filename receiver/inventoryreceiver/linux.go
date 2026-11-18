// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package inventoryreceiver

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"math"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// linuxSource reads the facts from /proc and /sys, and the SMBIOS table, through
// fsys rooted at "/". It compiles on every OS so tests can run it on any host.
type linuxSource struct {
	fsys      fs.FS
	maxDrives int
	// cpuInfoCap replaces maxCPUInfo when set; tests use it.
	cpuInfoCap int64
}

const (
	dmiIDDir     = "sys/class/dmi/id"
	dmiTablePath = "sys/firmware/dmi/tables/DMI"
	// smbiosEntryPath holds the SMBIOS entry point, which carries the specification version.
	smbiosEntryPath = "sys/firmware/dmi/tables/smbios_entry_point"
	cpuSysDir       = "sys/devices/system/cpu"
	meminfoPath     = "proc/meminfo"
	maxMeminfo      = 64 << 10
	maxTextRead     = 4096
	maxCPUInfo      = 16 << 20
	maxEnviron      = 256 << 10
	maxCPUs         = 4096
	maxGPUs         = 32
)

// skippedBlock names block devices that are not drives: virtual and memory devices, and the boot
// and replay-protected partitions of an eMMC card, which share the card's identity.
var skippedBlock = regexp.MustCompile(`^((loop|ram|zram|dm-|sr|fd|md|nbd|rbd|zd|nullb|mtdblock)\d+|mmcblk\d+(boot\d+|rpmb))$`)

var nominalFromModel = regexp.MustCompile(`@\s*([0-9]+(?:\.[0-9]+)?)\s*GHz`)

func (s *linuxSource) read(ctx context.Context) inventory {
	inv := newInventory()
	cpu := s.parseCPUInfo()
	dmi, dmiDirOK := s.readDMIIDs()
	inv.host, inv.hostReason = s.detectHost(cpu, dmi, dmiDirOK)
	if inv.host == hostVirtual || inv.host == hostContainer || ctx.Err() != nil {
		return inv
	}

	s.addCPU(&inv, cpu)
	inv.status[srcBoard], inv.status[srcBIOS], inv.status[srcSystem] = dmiDirOK, dmiDirOK, dmiDirOK
	if dmiDirOK {
		dmi.addFacts(&inv)
	}
	if ctx.Err() != nil {
		return inv
	}
	s.addMemory(&inv)
	s.addMemoryTotal(&inv)
	if ctx.Err() != nil {
		return inv
	}
	s.addDrives(ctx, &inv)
	if ctx.Err() != nil {
		return inv
	}
	s.addGPUs(ctx, &inv)
	return inv
}

// readLimited stops at limit bytes so a huge file cannot exhaust memory.
func (s *linuxSource) readLimited(name string, limit int64) ([]byte, error) {
	f, err := s.fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, limit))
}

// text reads a small text file without its surrounding space.
func (s *linuxSource) text(name string) (string, error) {
	b, err := s.readLimited(name, maxTextRead)
	return strings.TrimSpace(string(b)), err
}

func (s *linuxSource) number(name string) (uint64, bool) {
	t, err := s.text(name)
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseUint(t, 10, 64)
	return n, err == nil
}

func (s *linuxSource) exists(name string) bool {
	f, err := s.fsys.Open(name)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// cpuBlock is one processor entry of /proc/cpuinfo.
type cpuBlock struct {
	proc           int
	vendor, model  string
	physical, core string
	hypervisorFlag bool
}

type cpuInfo struct {
	blocks   []cpuBlock
	readable bool
	// truncated is true when the file or the block list was cut at its limit, so counts would be too low.
	truncated bool
}

func (c cpuInfo) hypervisor() bool {
	for _, b := range c.blocks {
		if b.hypervisorFlag {
			return true
		}
	}
	return false
}

func (s *linuxSource) parseCPUInfo() cpuInfo {
	limit := int64(maxCPUInfo)
	if s.cpuInfoCap > 0 {
		limit = s.cpuInfoCap
	}
	raw, err := s.readLimited("proc/cpuinfo", limit+1)
	if err != nil {
		return cpuInfo{}
	}
	info := cpuInfo{readable: true}
	if int64(len(raw)) > limit {
		raw, info.truncated = raw[:limit], true
	}
	cur := cpuBlock{proc: -1}
	flush := func() {
		if cur.proc >= 0 {
			if len(info.blocks) < maxCPUs {
				info.blocks = append(info.blocks, cur)
			} else {
				info.truncated = true
			}
		}
		cur = cpuBlock{proc: -1}
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			if strings.TrimSpace(line) == "" {
				flush()
			}
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		switch key {
		case "processor":
			if cur.proc >= 0 {
				flush()
			}
			if n, err := strconv.Atoi(val); err == nil && n >= 0 {
				cur.proc = n
			}
		case "vendor_id":
			cur.vendor = val
		case "model name", "Model Name":
			cur.model = val
		case "physical id":
			cur.physical = val
		case "core id":
			cur.core = val
		case "flags":
			for _, w := range strings.Fields(val) {
				if w == "hypervisor" {
					cur.hypervisorFlag = true
				}
			}
		}
	}
	flush()
	return info
}

// readDMIIDs reads the firmware text files; ok is false when the directory is not readable.
func (s *linuxSource) readDMIIDs() (dmiFacts, bool) {
	if _, err := fs.ReadDir(s.fsys, dmiIDDir); err != nil {
		return dmiFacts{}, false
	}
	get := func(name string) string {
		t, err := s.text(dmiIDDir + "/" + name)
		if err != nil {
			return ""
		}
		return cleanValue(t)
	}
	d := dmiFacts{
		sysVendor: get("sys_vendor"), productName: get("product_name"), productVersion: get("product_version"),
		productSerial: get("product_serial"), productFamily: get("product_family"),
		boardVendor: get("board_vendor"), boardName: get("board_name"), boardVersion: get("board_version"),
		boardSerial: get("board_serial"), boardAsset: get("board_asset_tag"),
		biosVendor: get("bios_vendor"), biosVersion: get("bios_version"), biosDate: get("bios_date"),
		chassisVendor: get("chassis_vendor"), chassisSerial: get("chassis_serial"), chassisAsset: get("chassis_asset_tag"),
	}
	if n, err := strconv.ParseUint(get("chassis_type"), 10, 8); err == nil {
		d.chassisType = chassisTypes[byte(n)&0x7F]
	}
	return d, true
}

var containerCgroupMarks = []string{"/docker", "/kubepods", "containerd", "/libpod", "/crio", "/lxc"}

var hypervisorCompatible = []string{"kvm", "xen", "vmware", "hyperv", "qemu"}

// containerReason returns why this process runs in a container, or "". The environment read is the
// collector's own (proc/self/environ); the environment of other processes is never read.
func (s *linuxSource) containerReason() string {
	if s.exists(".dockerenv") || s.exists("run/.containerenv") {
		return "container marker file"
	}
	if b, err := s.readLimited("run/systemd/container", maxTextRead); err == nil && strings.TrimSpace(string(b)) != "" {
		return "container runtime file"
	}
	if cg, err := s.readLimited("proc/1/cgroup", maxTextRead); err == nil {
		for _, mark := range containerCgroupMarks {
			if strings.Contains(string(cg), mark) {
				return "container cgroup"
			}
		}
	}
	if env, err := s.readLimited("proc/self/environ", maxEnviron); err == nil {
		for _, kv := range bytes.Split(env, []byte{0}) {
			if v, ok := bytes.CutPrefix(kv, []byte("container=")); ok && len(v) > 0 {
				return "container environment variable"
			}
		}
	}
	return ""
}

// xenControlDomain tells a Xen control domain (dom0), which runs on the real hardware. The flag is
// start_flags/initdomain under /sys/hypervisor; 1, true and yes (any case) mean dom0.
func (s *linuxSource) xenControlDomain() bool {
	kind, err := s.text("sys/hypervisor/type")
	if err != nil || kind != "xen" {
		return false
	}
	flag, _ := s.text("sys/hypervisor/start_flags/initdomain")
	switch strings.ToLower(flag) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// deviceTreeVirtual finds a hypervisor node, a fw-cfg node or the root name of a QEMU machine in
// /proc/device-tree; hosts without DMI (ARM) show a hypervisor only there. An ACPI-booted ARM
// guest has no device tree and is not detected.
func (s *linuxSource) deviceTreeVirtual() bool {
	const root = "proc/device-tree"
	if c, err := s.readLimited(root+"/hypervisor/compatible", maxTextRead); err == nil {
		low := strings.ToLower(string(c))
		for _, m := range hypervisorCompatible {
			if strings.Contains(low, m) {
				return true
			}
		}
	}
	if c, err := s.readLimited(root+"/compatible", maxTextRead); err == nil {
		low := strings.ToLower(string(c))
		if strings.Contains(low, "linux,dummy-virt") || strings.Contains(low, "qemu") {
			return true
		}
	}
	if entries, err := fs.ReadDir(s.fsys, root); err == nil {
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "fw-cfg") {
				return true
			}
		}
	}
	return false
}

// detectHost tells a container, a virtual machine and a physical host apart
// from the container markers, the cgroup of process 1, the hypervisor flag,
// /sys/hypervisor, the device tree and the firmware strings. A Xen control
// domain is physical whatever its CPU flags say.
func (s *linuxSource) detectHost(cpu cpuInfo, dmi dmiFacts, dmiOK bool) (hostKind, string) {
	if why := s.containerReason(); why != "" {
		return hostContainer, why
	}
	if !s.xenControlDomain() {
		if cpu.hypervisor() {
			return hostVirtual, "hypervisor flag in the CPU flags"
		}
		if kind, err := s.text("sys/hypervisor/type"); err == nil && kind != "" {
			return hostVirtual, "hypervisor interface in sysfs"
		}
		if s.deviceTreeVirtual() {
			return hostVirtual, "hypervisor in the device tree"
		}
	}
	if virtual, why := virtualFromDMI(dmi.sysVendor, dmi.productName, dmi.boardVendor, dmi.biosVendor); virtual {
		return hostVirtual, why
	}
	if cpu.readable || dmiOK {
		return hostPhysical, ""
	}
	return hostUndecided, "no CPU or firmware data to decide from"
}

type socketGroup struct {
	key      string
	first    int
	model    string
	vendor   string
	cores    map[string]bool
	threads  int
	coresOK  bool
	socketID int64
}

func (s *linuxSource) addCPU(inv *inventory, cpu cpuInfo) {
	inv.status[srcCPU] = cpu.readable && len(cpu.blocks) > 0
	if !inv.status[srcCPU] {
		return
	}
	blocks := cpu.blocks
	if cpu.truncated {
		// A cut list undercounts every number; keep the identity of the first processor only.
		blocks = blocks[:1]
		inv.notes = append(inv.notes, "the CPU list was cut at the read limit; counts are left out")
	}
	groups := map[string]*socketGroup{}
	socketsKnown := !cpu.truncated
	for _, b := range blocks {
		topo := func(file string) string {
			t, _ := s.text(cpuSysDir + "/cpu" + strconv.Itoa(b.proc) + "/topology/" + file)
			return t
		}
		physical, core := b.physical, b.core
		if physical == "" {
			physical = topo("physical_package_id")
		}
		if core == "" {
			core = topo("core_id")
		}
		if physical == "" {
			socketsKnown = false
		}
		g := groups[physical]
		if g == nil {
			g = &socketGroup{key: physical, first: b.proc, model: b.model, vendor: b.vendor, cores: map[string]bool{}, coresOK: true, socketID: -1}
			if n, err := strconv.ParseInt(physical, 10, 64); err == nil && n >= 0 {
				g.socketID = n
			}
			groups[physical] = g
		}
		g.threads++
		if core == "" {
			g.coresOK = false
		} else {
			g.cores[core] = true
		}
	}
	list := make([]*socketGroup, 0, len(groups))
	for _, g := range groups {
		list = append(list, g)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].socketID != list[j].socketID {
			return list[i].socketID < list[j].socketID
		}
		return list[i].first < list[j].first
	})
	for i, g := range list {
		f := newFact(metricCPU)
		id := g.socketID
		if id < 0 {
			id = int64(i)
		}
		f.attrs[attrID] = "cpu/" + strconv.FormatInt(id, 10)
		f.setStr(attrVendor, g.vendor)
		f.setStr(attrModel, g.model)
		if socketsKnown {
			f.attrs[attrCPUSocket] = id
			f.setNum(attrCPUSockets, int64(len(list)))
		}
		if g.coresOK && !cpu.truncated {
			f.setNum(attrCPUCores, int64(len(g.cores)))
		}
		if !cpu.truncated {
			f.setNum(attrCPUThreads, int64(g.threads))
		}
		freq := func(file string) int64 {
			n, ok := s.number(cpuSysDir + "/cpu" + strconv.Itoa(g.first) + "/cpufreq/" + file)
			if !ok || n > 1<<40 {
				return 0
			}
			return int64(n) * 1000
		}
		nominal := freq("base_frequency")
		if nominal == 0 {
			if m := nominalFromModel.FindStringSubmatch(g.model); m != nil {
				if ghz, err := strconv.ParseFloat(m[1], 64); err == nil && ghz > 0 && ghz < 100 {
					nominal = int64(ghz * 1e9)
				}
			}
		}
		f.setNum(attrCPUNominal, nominal)
		f.setNum(attrCPUMax, freq("cpuinfo_max_freq"))
		inv.facts = append(inv.facts, f)
	}
}

func (s *linuxSource) addMemory(inv *inventory) {
	raw, err := s.readLimited(dmiTablePath, maxTableBytes)
	if err != nil {
		inv.dmiUnreadable = true
		inv.status[srcMemory] = false
		return
	}
	inv.status[srcMemory] = true
	var ver smbiosVersion
	if ep, err := s.readLimited(smbiosEntryPath, 64); err == nil {
		ver = entryPointVersion(ep)
	}
	for _, m := range parseSMBIOSVersion(raw, ver).modules {
		inv.facts = append(inv.facts, memoryFact(m))
	}
}

// addMemoryTotal adds the physical RAM from /proc/meminfo; it needs neither root nor SMBIOS.
func (s *linuxSource) addMemoryTotal(inv *inventory) {
	raw, err := s.readLimited(meminfoPath, maxMeminfo)
	if err != nil {
		inv.status[srcMemoryTotal] = false
		return
	}
	total, ok := parseMemTotal(raw)
	inv.status[srcMemoryTotal] = ok
	if ok {
		inv.facts = append(inv.facts, memoryTotalFact(total))
	}
}

// parseMemTotal reads the MemTotal line (kB) as bytes. A missing line, a bad or zero number, another
// unit or a value that does not fit in int64 gives false.
func parseMemTotal(raw []byte) (int64, bool) {
	for _, line := range strings.Split(string(raw), "\n") {
		rest, found := strings.CutPrefix(line, "MemTotal:")
		if !found {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) != 2 || fields[1] != "kB" {
			return 0, false
		}
		kb, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil || kb == 0 || kb > math.MaxInt64/1024 {
			return 0, false
		}
		return int64(kb) * 1024, true
	}
	return 0, false
}

func (s *linuxSource) addDrives(ctx context.Context, inv *inventory) {
	entries, err := fs.ReadDir(s.fsys, "sys/block")
	if err != nil {
		inv.status[srcDrive] = false
		return
	}
	inv.status[srcDrive] = true
	count := 0
	for _, e := range entries {
		if ctx.Err() != nil {
			return
		}
		name := e.Name()
		if skippedBlock.MatchString(name) {
			continue
		}
		f, ok := s.driveFact(name)
		if !ok {
			continue
		}
		if count >= s.maxDrives {
			inv.drivesCapped = true
			return
		}
		count++
		inv.facts = append(inv.facts, f)
	}
}

func (s *linuxSource) driveFact(name string) (fact, bool) {
	base := "sys/block/" + name
	sectors, ok := s.number(base + "/size")
	if !ok || sectors == 0 || sectors > 1<<50 {
		return fact{}, false
	}
	get := func(file string) string {
		t, err := s.text(base + "/device/" + file)
		if err != nil {
			return ""
		}
		return cleanValue(t)
	}
	f := newFact(metricDrive)
	f.setStr(attrModel, get("model"))
	if v := get("vendor"); !strings.EqualFold(v, "ATA") {
		f.setStr(attrVendor, v)
	}
	fw := get("rev")
	if fw == "" {
		fw = get("firmware_rev")
	}
	f.setStr(attrFirmware, fw)
	serial := get("serial")
	if serial == "" {
		serial = s.vpdSerial(base + "/device/vpd_pg80")
	}
	f.setStr(attrSerial, serial)
	f.setStr(attrWWID, get("wwid"))
	if !f.hasData() {
		return fact{}, false
	}
	f.setNum(attrDriveSize, int64(sectors*512))
	if rot, ok := s.number(base + "/queue/rotational"); ok {
		switch rot {
		case 1:
			f.attrs[attrDriveType] = "HDD"
		case 0:
			f.attrs[attrDriveType] = "SSD"
		}
	}
	switch {
	case strings.HasPrefix(name, "nvme"):
		f.attrs[attrDriveBus] = "nvme"
	case strings.HasPrefix(name, "mmcblk"):
		f.attrs[attrDriveBus] = "mmc"
	case s.exists(base + "/device/vpd_pg89"):
		f.attrs[attrDriveBus] = "sata"
	case s.exists(base + "/device/sas_address"):
		f.attrs[attrDriveBus] = "sas"
	}
	id, _ := f.attrs[attrSerial].(string)
	if id == "" {
		id, _ = f.attrs[attrWWID].(string)
	}
	if id == "" {
		// The kernel name can change between boots; the place on the bus does not.
		id = s.busPath(name)
	}
	if id == "" {
		id = name
	}
	f.attrs[attrID] = id
	return f, true
}

var nvmeController = regexp.MustCompile(`^nvme\d+$`)

// busPath gives the place of a block device in the device tree, such as the PCI address of its
// controller and the host and target below it, without the kernel names that change at boot. It
// is "" when the link is not readable or the device has no bus (virtual devices).
func (s *linuxSource) busPath(name string) string {
	target, err := fs.ReadLink(s.fsys, "sys/block/"+name)
	if err != nil {
		return ""
	}
	_, rest, ok := strings.Cut(target, "devices/")
	if !ok || strings.HasPrefix(rest, "virtual/") {
		return ""
	}
	var keep []string
	for _, part := range strings.Split(strings.Trim(rest, "/"), "/") {
		if part == "" || part == "block" || part == "nvme" || part == name || nvmeController.MatchString(part) {
			continue
		}
		keep = append(keep, part)
	}
	return cleanValue(strings.Join(keep, "/"))
}

// vpdSerial reads the unit serial number page (0x80) of a SCSI device.
func (s *linuxSource) vpdSerial(name string) string {
	b, err := s.readLimited(name, 256)
	if err != nil || len(b) < 4 || b[1] != 0x80 {
		return ""
	}
	end := min(4+(int(b[2])<<8|int(b[3])), len(b))
	return string(bytes.TrimRight(b[4:end], "\x00 "))
}

func (s *linuxSource) addGPUs(ctx context.Context, inv *inventory) {
	const dir = "sys/bus/pci/devices"
	entries, err := fs.ReadDir(s.fsys, dir)
	if err != nil {
		inv.status[srcGPU] = false
		return
	}
	inv.status[srcGPU] = true
	count := 0
	for _, e := range entries {
		if ctx.Err() != nil || count >= maxGPUs {
			return
		}
		base := dir + "/" + e.Name()
		class, ok := s.hex(base + "/class")
		if !ok || (class>>16)&0xFF != 0x03 {
			continue
		}
		f := newFact(metricGPU)
		f.attrs[attrID] = e.Name()
		vendor, vendorOK := s.hex(base + "/vendor")
		device, deviceOK := s.hex(base + "/device")
		if vendorOK {
			f.attrs[attrGPUVendorID] = hex4(vendor)
		}
		if deviceOK {
			f.attrs[attrGPUDeviceID] = hex4(device)
		}
		if target, err := fs.ReadLink(s.fsys, base+"/driver"); err == nil {
			f.setStr(attrDriver, path.Base(target))
		}
		if isBMCVideo(vendor, device, f.attrs[attrDriver]) {
			continue
		}
		if n, ok := s.number(base + "/mem_info_vram_total"); ok && n < 1<<50 {
			f.setNum(attrGPUMemory, int64(n))
		}
		count++
		inv.facts = append(inv.facts, f)
	}
}

func (s *linuxSource) hex(name string) (uint64, bool) {
	t, err := s.text(name)
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(t), "0x"), 16, 32)
	return n, err == nil
}

func hex4(v uint64) string {
	s := strconv.FormatUint(v, 16)
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}
