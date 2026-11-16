// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// runRescrub applies the current scrub rules to a finished bundle and writes the result to a new
// directory. It touches no device and no sysfs, and every file keeps its length.
func runRescrub(args []string, random io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("megaraid-capture rescrub", flag.ContinueOnError)
	fs.SetOutput(stderr)
	salt := fs.String("salt", "", "salt for the replacement tokens (default: random)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		fmt.Fprintln(stderr, "usage: megaraid-capture rescrub [-salt SALT] IN_DIR OUT_DIR")
		return 2
	}
	in, out := fs.Arg(0), fs.Arg(1)
	saltBytes := []byte(*salt)
	if *salt == "" {
		saltBytes = make([]byte, 16)
		if _, err := io.ReadFull(random, saltBytes); err != nil {
			fmt.Fprintln(stderr, "megaraid-capture: cannot read random bytes for the salt:", err)
			return 1
		}
	}
	if _, err := os.Lstat(out); err == nil {
		fmt.Fprintf(stderr, "megaraid-capture: %s exists; refusing to write into it\n", out)
		return 1
	}
	if err := rescrubDir(in, out, saltBytes); err != nil {
		fmt.Fprintln(stderr, "megaraid-capture:", err)
		return 1
	}
	if problems := verifyDir(out); len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(stderr, "unscrubbed:", p)
		}
		fmt.Fprintf(stderr, "megaraid-capture: rescrub failed: %s was written but does not verify\n", out)
		return 1
	}
	fmt.Fprintln(stdout, out)
	return 0
}

// rescrubDir writes a rescrubbed copy of the bundle in dir to a new directory out.
func rescrubDir(dir, out string, salt []byte) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var bins []string
	manifestBody := []byte(nil)
	for _, e := range entries {
		switch {
		case !e.Type().IsRegular():
			return fmt.Errorf("%s is not a regular file", e.Name())
		case e.Name() == "manifest.json":
			if manifestBody, err = os.ReadFile(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		case strings.HasSuffix(e.Name(), ".bin"):
			bins = append(bins, e.Name())
		default:
			return fmt.Errorf("%s is not part of a bundle", e.Name())
		}
	}
	if manifestBody == nil {
		return fmt.Errorf("%s has no manifest.json", dir)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(manifestBody, &fields); err != nil {
		return fmt.Errorf("manifest.json: %w", err)
	}
	fields["rescrubbed"] = json.RawMessage("true")
	body, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return err
	}
	if err := os.Mkdir(out, 0o700); err != nil {
		return err
	}
	sc := newScrubber(salt, nil)
	sc.keep = true
	for _, name := range bins {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(out, name), sc.scrub(kindOf(name), data), 0o600); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(out, "manifest.json"), append(body, '\n'), 0o600)
}
