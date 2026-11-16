// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartreceiver

import (
	"io"
	"time"

	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
)

// bus lists the disks and opens a transport to one. The Linux build reads /sys and sends SG_IO and NVMe
// admin ioctls; tests give fakes.
type bus interface {
	discover() ([]device, error)
	openATA(name string, timeout time.Duration) (smartdecode.ATATransport, io.Closer, error)
	openNVMe(name string, timeout time.Duration) (smartdecode.NVMeTransport, io.Closer, error)
}
