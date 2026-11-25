// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package filestorage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"go.etcd.io/bbolt"
)

// bbolt starts a goroutine without a recover() while it opens a file, and a torn file makes that goroutine
// panic. A recover() in the caller cannot catch it, so the check runs in a child process: the binary starts
// itself with checkEnv set, and the init below does the check and exits.
const (
	checkEnv         = "OTELCOL_FILESTORAGE_CHECK"
	checkExitPanic   = 2 // the exit code of the Go runtime after an unrecovered panic
	checkExitCorrupt = 3
	checkExitBusy    = 4
	checkOpenTimeout = 5 * time.Second
	checkRunTimeout  = 2 * time.Minute
	detailMax        = 300
)

func init() {
	if path := os.Getenv(checkEnv); path != "" {
		os.Exit(runCheck(path, os.Stderr))
	}
}

// runCheck opens the file read-only the way the extension does and walks every page. It returns the exit code
// of the child: 0 for a sound file, checkExitCorrupt for a failed open or check, checkExitBusy for a lock
// held by another process. A panic ends the child with the runtime's exit code 2.
func runCheck(path string, stderr io.Writer) int {
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{
		ReadOnly:       true,
		Timeout:        checkOpenTimeout,
		NoFreelistSync: true,
		FreelistType:   bbolt.FreelistMapType,
	})
	if errors.Is(err, bbolt.ErrTimeout) {
		return checkExitBusy
	}
	if err != nil {
		fmt.Fprintf(stderr, "open: %v\n", err)
		return checkExitCorrupt
	}
	defer func() { _ = db.Close() }()

	code := 0
	if viewErr := db.View(func(tx *bbolt.Tx) error {
		for e := range tx.Check() {
			fmt.Fprintf(stderr, "check: %v\n", e)
			code = checkExitCorrupt
		}
		return nil
	}); viewErr != nil {
		fmt.Fprintf(stderr, "view: %v\n", viewErr)
		return checkExitCorrupt
	}
	return code
}

// checkBufferFile runs the check of one file in a child process. corrupt is true when the child failed the
// file or died; detail is the first line it printed. An error means the check could not run (no child
// started, a timeout, or a lock held elsewhere), and says nothing about the file. A missing or empty file
// has nothing to check.
func checkBufferFile(ctx context.Context, path string, timeout time.Duration) (corrupt bool, detail string, err error) {
	fileInfo, statErr := os.Stat(path)
	if errors.Is(statErr, os.ErrNotExist) {
		return false, "", nil
	}
	if statErr != nil {
		return false, "", fmt.Errorf("stat the file: %w", statErr)
	}
	if fileInfo.Size() == 0 {
		return false, "", nil
	}
	exe, err := os.Executable()
	if err != nil {
		return false, "", fmt.Errorf("find the executable: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	cmd.Env = append(os.Environ(), checkEnv+"="+path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	if runErr == nil {
		return false, "", nil
	}
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) || ctx.Err() != nil {
		return false, "", fmt.Errorf("run the check: %w", runErr)
	}
	switch exitErr.ExitCode() {
	case checkExitBusy:
		return false, "", errors.New("the file is locked by another process")
	case checkExitCorrupt, checkExitPanic:
		return true, firstLine(stderr.String()), nil
	}
	return false, "", fmt.Errorf("the check ended without a verdict: %w", runErr)
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if len(line) > detailMax {
		line = line[:detailMax]
	}
	return line
}
