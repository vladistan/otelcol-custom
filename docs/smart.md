# Disk health with the `smart` receiver

The `smart` receiver reads the health data of NVMe and SATA disks from the collector process and emits
`hw.*` metrics. It is in the Linux edge and the static Linux edge builds only. It sends `SG_IO` and NVMe
admin ioctls itself, one disk at a time, with `golang.org/x/sys/unix`: no `smartctl`, no external
command, no cgo.

```yaml
receivers:
  smart:
    collection_interval: 5m        # default
    command_timeout: 5s            # default; one command to one disk
    include: []                    # regular expressions on the kernel name; empty reads every disk
    exclude: []
    skip_if_standby: true          # default
    attributes: default            # default | all | [5, 9, 194]
    value_types: [normalized, raw] # default; worst and threshold are opt-in
    max_devices: 64                # default

service:
  pipelines:
    metrics:
      receivers: [smart]
```

`test/configs/edge-smart.yaml` is a complete sample.

## What it reads

Discovery lists `/sys/block` and `/sys/class/nvme` and keeps whole SATA-class disks (`sd*`, `hd*`, `vd*`,
`xvd*`) and NVMe controllers. Partitions, `loop`, `dm-*`, `md*`, `zd*`, `ram*`, optical devices and the
namespace devices of an NVMe controller are skipped. Start sends no command to any disk; the first read
comes five seconds after start, then every `collection_interval`.

The commands are fixed and read only:

- NVMe, to the controller device `/dev/nvmeN`: Identify Controller (opcode 06h, CNS 01h) and Get Log Page
  (opcode 02h, log 02h).
- SATA, as ATA PASS-THROUGH(16): IDENTIFY DEVICE, SMART READ DATA, SMART READ THRESHOLDS, SMART RETURN
  STATUS, CHECK POWER MODE and READ LOG EXT of the Device Statistics log.

Every command passes an allow list before it is sent. SMART ENABLE, DISABLE, AUTOSAVE and the off-line
tests are not on it, and the receiver never switches SMART on: a disk with SMART off is skipped.

## Metrics

Every series carries `hw.id` (the serial number, else the WWN, else the kernel name) and `hw.name` (the
kernel name). `hw.model`, `hw.serial_number`, `hw.firmware_version` and `hw.physical_disk.type` (`SSD`,
`HDD` or `NVMe`) are set when the disk reports them. `hw.parent` is not set. Sums start at the time the
receiver started.

| Metric | Type | Unit | Attributes | Source |
|---|---|---|---|---|
| `hw.status` | gauge | `1` | `hw.type` (`physical_disk`), `hw.state` (`ok`, `degraded`, `predicted_failure`, `failed`; one point each, 1 or 0) | NVMe critical warning; SATA SMART RETURN STATUS |
| `hw.temperature` | gauge | `Cel` | `hw.sensor_location` on NVMe (`composite`, `sensor1` to `sensor8`) | NVMe log; SATA attribute 194, else 190, else Device Statistics |
| `hw.physical_disk.endurance_utilization` | gauge | `1` | `hw.physical_disk.state` = `remaining` | NVMe: 1 minus percentage used; SATA SSD: attribute 231, else 233, else 202, divided by 100 |
| `hw.errors` | sum | `{error}` | `error.type` (`media`, `bad_sector`, `crc`), `hw.physical_disk.smart_attribute` on SATA | NVMe media errors; SATA attributes 5, 197, 198 (`bad_sector`) and 199 (`crc`) |
| `hw.physical_disk.size` | gauge | `By` | | Identify |
| `hw.physical_disk.power_on_time` | gauge | `s` | | NVMe hours; SATA attribute 9 (the low 32 bits of the raw value, in hours) |
| `hw.physical_disk.power_cycles` | sum | `{cycle}` | | NVMe; SATA attribute 12 |
| `hw.physical_disk.io` | sum | `By` | `disk.io.direction` (`read`, `write`) | NVMe data units of 512,000 bytes; SATA attributes 241 and 242 |
| `hw.physical_disk.smart` | gauge | `1` | `hw.physical_disk.smart_attribute` (the name), `hw.physical_disk.smart_value_type` | The SATA attribute table; on NVMe `Available_Spare`, `Available_Spare_Threshold` and `Unsafe_Shutdowns`, with `raw` only |

`hw.physical_disk.smart_value_type` is `normalized`, `worst`, `threshold` or `raw`. It is our own attribute:
it keeps the four values of one SMART attribute in the one semantic-convention gauge. `worst` and
`threshold` are emitted only when listed in `value_types`. The raw value is its 48-bit number. An
attribute id the table does not name is called `Attribute_<id>`. The three `hw.physical_disk` metrics
for power-on time, power cycles and data moved have names of our own, because the semantic conventions
have none.

What the SATA numbers assume. Vendors differ, and the receiver does not know the vendor:

- Attributes 241 and 242 are counted in units of 512 bytes. Some vendors use 32 MiB, 1 MiB or 4 KiB, and
  their `hw.physical_disk.io` is then wrong by that factor.
- Attribute 9 is taken as hours, from the low 32 bits of the raw value. A vendor that stores minutes or
  half-minutes gives a wrong `power_on_time`.
- The raw values of attributes 1, 7 and 195 are composite numbers of the vendor, not counts of events. They
  are emitted as the 48-bit number, and only in `hw.physical_disk.smart`.
- Attributes 5, 197 and 198 feed the monotonic sum `hw.errors`. A pending or uncorrectable count can fall
  when the disk reallocates a sector, and a backend then sees a counter reset.
- `hw.vendor` comes from a short table: the PCI vendor id of Identify Controller for an NVMe disk and the start of
  the model string for an ATA disk. An id or model that is not in the table leaves the attribute out, so a
  missing `hw.vendor` does not mean the disk has no maker.
- An NVMe controller that reports no capacity (common on consumer drives) gives `hw.physical_disk.size` from the
  sum of its namespaces in `/sys/class/nvme`; with neither, the series is left out.

The receiver also records `otelcol_smart_devices_skipped` with the attribute `reason`: `timeout`,
`unsupported`, `smart_disabled`, `standby`, `permission` or `error`.

## Privileges

Reading a disk needs two things: read access to its device file, and a capability for the command.
Root has both. Any other user needs both:

| Disks | Device file, as usually created | Capability for the command |
|---|---|---|
| NVMe admin commands | `/dev/nvmeN`, owner `root`, mode `0600` | `CAP_SYS_ADMIN` |
| SATA ATA pass-through | `/dev/sdX`, group `disk`, mode `0660` | `CAP_SYS_RAWIO` |

The capabilities do not open a file the user cannot read, and read access to the file does not allow the
command. For the file, put the collector's user in the `disk` group (SATA), give it read access to
`/dev/nvmeN` with a udev rule or an ACL, or give the process `CAP_DAC_READ_SEARCH` (or `CAP_DAC_OVERRIDE`).
Under systemd, set `User=` and grant the capabilities with `AmbientCapabilities=`.

A disk that cannot be read gives one error in the log, once while the failure lasts, and the other disks
are still read. Both kinds of refusal are counted with reason `permission`; the log tells them apart. A
refused open says "Cannot open the device file" and asks for read access. A refused command carries
`missing_capability`, with the capability the disk kind needs. With neither, no disk is read and no `hw.*`
series appears.

Under systemd the unit must not hide the devices. Leave `PrivateDevices` unset, and if the unit sets a
`DeviceAllow` list, include `block-sd` and `char-nvme`. With `PrivateDevices=yes` the device nodes are
missing and the open fails with a message that names the path.

Temperatures that need no privilege come from the `hwmon` receiver, which reads the host sensors; this
receiver does not read `hwmon`.

## Series budget

With the default 20 attributes and the two default value types, a SATA disk gives about 40
`hw.physical_disk.smart` series, plus about 15 from the typed metrics. `attributes: all` with all four
value types can give about 120 series for a disk that reports 30 attributes. An NVMe controller gives
about 25 series. At 64 disks and the defaults that is under 4,000 series. Keep `include` narrow on a
host with many disks.

## What it does not read

- **Disks behind a RAID controller.** On a PERC or MegaRAID virtual disk `/dev/sdX` is the virtual drive
  and has no SMART data of its own. The receiver skips it with reason `unsupported`. The decoders sit
  in a separate module behind a transport interface, so a later controller layer can supply the same
  buffers.
- **SAS disks.** This receiver does not read SCSI `LOG SENSE`; a SAS disk behind an HBA is skipped as
  `unsupported`. SAS drives behind a MegaRAID controller are read by the `megaraid` receiver.
- **USB bridges that refuse the SAT command**, and virtual disks (`virtio_blk`, VMware): skipped as
  `unsupported`.
- **A disk in standby**, with `skip_if_standby` on: CHECK POWER MODE runs, no SMART command follows, and
  the skip is counted with reason `standby`.

A command that does not finish in `command_timeout` skips that disk for the interval and counts reason
`timeout`. More disks than `max_devices` logs one warning, and the first `max_devices` are read.

## Failure modes

| Case | Result |
|---|---|
| No read access to a device file | One error naming the device and asking for read access; counted as `permission`; the other devices are read |
| The kernel refuses the command | One error naming the device and the missing capability; counted as `permission`; the other devices are read |
| A command that never returns | The disk is skipped with reason `timeout`, and is not opened again until that command returns, so a hung disk holds one goroutine and no more |
| Shutdown during a read | The receiver reads no further disk and waits for the one in hand, at most until the shutdown context ends |
| `PrivateDevices` set, or a `DeviceAllow` list without `block-sd` and `char-nvme` | The open fails, naming the path; the device is counted as `permission` or `error` |
| Disk with SMART off | Skipped with reason `smart_disabled` and one info line for each episode; SMART is never enabled |
| Disk without SMART | Skipped with reason `unsupported` and one info line for each episode |
| Command blocks | Skipped for the interval with reason `timeout`; one disk is read at a time |
| Disk replaced under the same kernel name | A new `hw.id` (the serial number) starts a new series |
| Unknown attribute id | Named `Attribute_<id>`; its normalized and raw values are emitted |
