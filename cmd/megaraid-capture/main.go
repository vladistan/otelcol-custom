// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Command megaraid-capture reads the state of MegaRAID controllers through the megaraid_sas
// ioctl node and writes a scrubbed bundle of the raw answers, for use as test fixtures. It sends
// only the read commands of the allow-list in allow.go, runs no other program and writes nothing
// to a controller. Run it as root: the driver refuses the open without CAP_SYS_ADMIN. The node
// is the first character device of the probe list, or the path given with -dev. The manifest
// records the node and the controller's PCI address.
//
//	sudo ./megaraid-capture -out /tmp/mr-capture -label lab-a
//	./megaraid-capture verify /tmp/mr-capture/lab-a-host0
package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// environment holds what the tool reads from the host, so tests can replace it.
type environment struct {
	sysfs    string
	hostname func() (string, error)
	random   io.Reader
	open     func(devPath string, hostNo int) (device, func(), error)
}

func defaultEnvironment() *environment {
	return &environment{sysfs: "/sys", hostname: os.Hostname, random: rand.Reader, open: openNode}
}

var labelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func main() {
	os.Exit(run(os.Args[1:], defaultEnvironment(), os.Stdout, os.Stderr))
}

// run returns 0 on success, 1 when a capture or a check fails and 2 on a usage error.
func run(args []string, env *environment, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "verify" {
		return runVerify(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "rescrub" {
		return runRescrub(args[1:], env.random, stdout, stderr)
	}
	fs := flag.NewFlagSet("megaraid-capture", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "", "directory for the scrubbed bundles (required)")
	label := fs.String("label", "controller", "name for the bundle directories, letters, digits, dot, dash and underscore")
	host := fs.Int("host", -1, "capture only this SCSI host number (default: every megaraid_sas controller)")
	events := fs.Int("events", 100, fmt.Sprintf("newest controller events to read, 0 for none, at most %d", maxEvents))
	salt := fs.String("salt", "", "salt for the replacement tokens (default: random, so output differs per run); a salt given here shows in ps, and a short one makes the tokens guessable")
	dev := fs.String("dev", "", "ioctl device path (default: the first character device of "+strings.Join(probePaths, ", ")+")")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "megaraid-capture", version)
		return 0
	}
	switch {
	case *out == "":
		fmt.Fprintln(stderr, "megaraid-capture: -out is required")
		fs.Usage()
		return 2
	case !labelPattern.MatchString(*label):
		fmt.Fprintf(stderr, "megaraid-capture: -label %q is not allowed\n", *label)
		return 2
	case *events < 0 || *events > maxEvents:
		fmt.Fprintf(stderr, "megaraid-capture: -events must be 0 to %d\n", maxEvents)
		return 2
	}

	controllers, err := findControllers(env.sysfs)
	if err != nil {
		fmt.Fprintln(stderr, "megaraid-capture:", err)
		return 1
	}
	if *host >= 0 {
		controllers = filterHost(controllers, *host)
		if len(controllers) == 0 {
			fmt.Fprintf(stderr, "megaraid-capture: host %d is not a megaraid_sas controller\n", *host)
			return 1
		}
	}
	if len(controllers) == 0 {
		fmt.Fprintln(stderr, "megaraid-capture: no megaraid_sas controller found in sysfs")
		return 1
	}

	saltBytes := []byte(*salt)
	if *salt == "" {
		saltBytes = make([]byte, 16)
		if _, err := io.ReadFull(env.random, saltBytes); err != nil {
			fmt.Fprintln(stderr, "megaraid-capture: cannot read random bytes for the salt:", err)
			return 1
		}
	}
	sc := newScrubber(saltBytes, hostnames(env))

	failed := false
	for _, c := range controllers {
		if err := captureOne(env, c, *out, *dev, captureOptions{label: *label, events: *events, scrub: sc}, stdout); err != nil {
			fmt.Fprintf(stderr, "megaraid-capture: host %d: %v\n", c.HostNo, err)
			failed = true
		}
	}
	if failed {
		return 1
	}
	return 0
}

func filterHost(all []controller, host int) []controller {
	var out []controller
	for _, c := range all {
		if c.HostNo == host {
			out = append(out, c)
		}
	}
	return out
}

// hostnames lists this machine's name and its short form, so the scrubber can remove them from event text.
func hostnames(env *environment) []string {
	name, err := env.hostname()
	if err != nil || name == "" {
		return nil
	}
	short, _, _ := strings.Cut(name, ".")
	return []string{name, short}
}

func captureOne(env *environment, c controller, out, devPath string, opt captureOptions, stdout io.Writer) error {
	dev, closeDev, err := env.open(devPath, c.HostNo)
	if err != nil {
		return err
	}
	defer closeDev()
	b, err := captureController(dev, c.HostNo, opt)
	if err != nil {
		return err
	}
	b.manifest.PCI = c.PCI
	if n, ok := dev.(interface{ nodePath() string }); ok {
		b.manifest.Device = n.nodePath()
	}
	dir, err := writeBundle(out, b)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, dir)
	return nil
}

func runVerify(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: megaraid-capture verify BUNDLE_DIR")
		return 2
	}
	problems := verifyDir(args[0])
	for _, p := range problems {
		fmt.Fprintln(stderr, "unscrubbed:", p)
	}
	if len(problems) > 0 {
		return 1
	}
	fmt.Fprintln(stdout, "ok:", args[0], "(event text is checked by token shape)")
	return 0
}
