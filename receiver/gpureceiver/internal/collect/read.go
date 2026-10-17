package collect

import (
	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/semconv"
)

var directions = []struct {
	dir   port.Direction
	call  string
	value string
}{
	{port.Receive, "PCIeReceive", semconv.DirReceive},
	{port.Transmit, "PCIeTransmit", semconv.DirTransmit},
}

var eccKinds = []struct {
	kind  port.ECCKind
	call  string
	value string
}{
	{port.Corrected, "ECCCorrected", semconv.ErrorCorrected},
	{port.Uncorrected, "ECCUncorrected", semconv.ErrorUncorrected},
}

var clockDomains = []struct {
	domain port.ClockDomain
	call   string
	value  string
}{
	{port.ClockGraphics, "ClockGraphics", semconv.ClockGraphics},
	{port.ClockSM, "ClockSM", semconv.ClockSM},
	{port.ClockMemory, "ClockMemory", semconv.ClockMemory},
	{port.ClockVideo, "ClockVideo", semconv.ClockVideo},
}

// readDevice reads in schema order. After GPU_IS_LOST the remaining reads
// are skipped by query; points already added stay in the batch (D-13).
func (c *Collector) readDevice(s *scrape, dev port.Device) {
	c.readUtilization(s, dev)
	c.readMemory(s, dev)
	c.readPCIe(s, dev)
	c.readECC(s, dev)
	c.readTemperature(s, dev)
	c.readPower(s, dev)
	c.readClocks(s, dev)
}

func (c *Collector) readUtilization(s *scrape, dev port.Device) {
	if !c.on(semconv.MetricUtilization) {
		return
	}
	emit := func(task string, percent uint32) {
		s.b.addDouble(semconv.MetricUtilization, s.d.id, float64(percent)/100, attr{semconv.AttrGPUTask, task})
	}
	if rates, ok := query(s, "Utilization", dev.Utilization); ok {
		emit(semconv.TaskGeneral, rates.GPU)
	}
	if percent, ok := query(s, "EncoderUtil", dev.EncoderUtil); ok {
		emit(semconv.TaskEncoder, percent)
	}
	if percent, ok := query(s, "DecoderUtil", dev.DecoderUtil); ok {
		emit(semconv.TaskDecoder, percent)
	}
}

func (c *Collector) readMemory(s *scrape, dev port.Device) {
	usage, limit, ratio := c.on(semconv.MetricMemoryUsage), c.on(semconv.MetricMemoryLimit), c.on(semconv.MetricMemoryUtilization)
	if !usage && !limit && !ratio {
		return
	}
	mem, ok := query(s, "MemoryInfo", dev.MemoryInfo)
	if !ok {
		return
	}
	if usage {
		s.b.addInt(semconv.MetricMemoryUsage, s.d.id, int64(mem.Used))
	}
	if limit {
		s.b.addInt(semconv.MetricMemoryLimit, s.d.id, int64(mem.Total))
	}
	if ratio && mem.Total > 0 {
		s.b.addDouble(semconv.MetricMemoryUtilization, s.d.id, float64(mem.Used)/float64(mem.Total))
	}
}

// readPCIe takes one sample per direction; it feeds both io and throughput.
func (c *Collector) readPCIe(s *scrape, dev port.Device) {
	io, throughput := c.on(semconv.MetricIO), c.on(semconv.MetricPCIeThroughput)
	if !io && !throughput {
		return
	}
	maxGap := maxGapIntervals * c.interval
	for _, d := range directions {
		kilobytesPerSecond, ok := query(s, d.call, func() (uint32, error) { return dev.PCIeThroughput(d.dir) })
		if !ok {
			continue
		}
		dirAttr := attr{semconv.AttrNetworkDir, d.value}
		if throughput {
			s.b.addInt(semconv.MetricPCIeThroughput, s.d.id, int64(kilobytesPerSecond)*1024, dirAttr)
		}
		if io {
			total := s.d.io[d.dir].sample(s.now, kilobytesPerSecond, maxGap)
			s.b.addInt(semconv.MetricIO, s.d.id, int64(total), dirAttr)
		}
	}
}

func (c *Collector) readECC(s *scrape, dev port.Device) {
	if !c.on(semconv.MetricErrors) {
		return
	}
	for _, k := range eccKinds {
		raw, ok := query(s, k.call, func() (uint64, error) { return dev.ECCCount(k.kind) })
		if !ok {
			continue
		}
		total := s.d.ecc[k.kind].sample(raw)
		s.b.addInt(semconv.MetricErrors, s.d.id, int64(total), attr{semconv.AttrErrorType, k.value})
	}
}

func (c *Collector) readTemperature(s *scrape, dev port.Device) {
	if !c.on(semconv.MetricTemperature) {
		return
	}
	if celsius, ok := query(s, "Temperature", dev.Temperature); ok {
		s.b.addTemperature(s.d.id, float64(celsius))
	}
}

func (c *Collector) readPower(s *scrape, dev port.Device) {
	if c.on(semconv.MetricPower) {
		if milliwatts, ok := query(s, "PowerUsage", dev.PowerUsage); ok {
			s.b.addDouble(semconv.MetricPower, s.d.id, float64(milliwatts)/1000)
		}
	}
	if c.on(semconv.MetricPowerLimit) {
		if milliwatts, ok := query(s, "EnforcedPowerLimit", dev.EnforcedPowerLimit); ok {
			s.b.addDouble(semconv.MetricPowerLimit, s.d.id, float64(milliwatts)/1000)
		}
	}
}

func (c *Collector) readClocks(s *scrape, dev port.Device) {
	if !c.on(semconv.MetricClock) {
		return
	}
	for _, d := range clockDomains {
		mhz, ok := query(s, d.call, func() (uint32, error) { return dev.Clock(d.domain) })
		if !ok {
			continue
		}
		s.b.addInt(semconv.MetricClock, s.d.id, int64(mhz)*1_000_000, attr{semconv.AttrClockDomain, d.value})
	}
}
