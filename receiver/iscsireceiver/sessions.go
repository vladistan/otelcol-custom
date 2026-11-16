// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package iscsireceiver

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.uber.org/zap"
)

// errNoSessions means the host has no iSCSI session class or no session in it.
var errNoSessions = errors.New("no iSCSI sessions found")

// session is one initiator session and its connections.
type session struct {
	target      string
	tpgt        string
	state       string
	portal      string // of the first connection; empty when there is none
	connections []connection
}

type connection struct {
	portal string
	state  string
}

// The only files the reader opens. The session directories also hold credential
// files (username, password, ...); none of them is a metric and none is read.
const (
	fileTarget   = "targetname"
	fileTPGT     = "tpgt"
	fileState    = "state"
	fileAddress  = "persistent_address"
	filePort     = "persistent_port"
	sessionClass = "iscsi_session"
	connClass    = "iscsi_connection"
)

var (
	sessionDir = regexp.MustCompile(`^session(\d+)$`)
	connDir    = regexp.MustCompile(`^connection(\d+):(\d+)$`)
)

type sysfsReader struct {
	logger   *zap.Logger
	root     string
	readFile func(string) ([]byte, error)
	readDir  func(string) ([]os.DirEntry, error)
}

func newSysfsReader(logger *zap.Logger, root string) *sysfsReader {
	return &sysfsReader{logger: logger, root: root, readFile: os.ReadFile, readDir: os.ReadDir}
}

// dirEntry is a class directory with the numbers parsed from its name.
type dirEntry struct {
	name          string
	session, conn int
}

// sessions reads every session, ordered by session number. A session that
// cannot be read is skipped with a debug line.
func (r *sysfsReader) sessions() ([]session, error) {
	dirs, err := r.list(sessionClass, sessionDir)
	if err != nil {
		return nil, fmt.Errorf("list the iSCSI sessions: %w", err)
	}
	conns := r.connections()

	var out []session
	for _, d := range dirs {
		s, err := r.readSession(filepath.Join(r.root, sessionClass, d.name))
		if err != nil {
			r.logger.Debug("Skipping an iSCSI session that cannot be read", zap.String("session", d.name), zap.Error(err))
			continue
		}
		s.connections = conns[d.session]
		if len(s.connections) > 0 {
			s.portal = s.connections[0].portal
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, errNoSessions
	}
	return out, nil
}

func (r *sysfsReader) readSession(dir string) (session, error) {
	var s session
	var err error
	if s.target, err = r.text(filepath.Join(dir, fileTarget)); err != nil {
		return session{}, err
	}
	if s.tpgt, err = r.text(filepath.Join(dir, fileTPGT)); err != nil {
		return session{}, err
	}
	if s.state, err = r.text(filepath.Join(dir, fileState)); err != nil {
		return session{}, err
	}
	return s, nil
}

// connections groups the connections by session number, each group in
// connection order. A connection that cannot be read is skipped.
func (r *sysfsReader) connections() map[int][]connection {
	out := map[int][]connection{}
	dirs, err := r.list(connClass, connDir)
	if err != nil {
		r.logger.Debug("Cannot list the iSCSI connections", zap.Error(err))
	}
	for _, d := range dirs {
		dir := filepath.Join(r.root, connClass, d.name)
		c, err := r.readConnection(dir)
		if err != nil {
			r.logger.Debug("Skipping an iSCSI connection that cannot be read", zap.String("connection", d.name), zap.Error(err))
			continue
		}
		out[d.session] = append(out[d.session], c)
	}
	return out
}

func (r *sysfsReader) readConnection(dir string) (connection, error) {
	addr, err := r.text(filepath.Join(dir, fileAddress))
	if err != nil {
		return connection{}, err
	}
	port, err := r.text(filepath.Join(dir, filePort))
	if err != nil {
		return connection{}, err
	}
	state, err := r.text(filepath.Join(dir, fileState))
	if err != nil {
		return connection{}, err
	}
	return connection{portal: net.JoinHostPort(addr, port), state: state}, nil
}

// list returns the directories of a class that match re, ordered by their
// numbers. A class that does not exist is empty; any other listing error is returned.
func (r *sysfsReader) list(class string, re *regexp.Regexp) ([]dirEntry, error) {
	entries, err := r.readDir(filepath.Join(r.root, class))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []dirEntry
	for _, e := range entries {
		m := re.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		d := dirEntry{name: e.Name()}
		d.session, _ = strconv.Atoi(m[1])
		if len(m) > 2 {
			d.conn, _ = strconv.Atoi(m[2])
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].session != out[j].session {
			return out[i].session < out[j].session
		}
		return out[i].conn < out[j].conn
	})
	return out, nil
}

func (r *sysfsReader) text(p string) (string, error) {
	b, err := r.readFile(p)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
