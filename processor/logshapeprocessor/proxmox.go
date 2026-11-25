// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"

	"github.com/vladistan/otelcol-custom/shape/body"
	"github.com/vladistan/otelcol-custom/shape/severity"
	"github.com/vladistan/otelcol-custom/shape/strip"
)

var (
	pbsShape   = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}[+-]\d{2}:\d{2}): (.*)$`)
	taskResult = regexp.MustCompile(`^TASK (OK|ERROR|WARNINGS)\b`)
)

// upid is the task id of a Proxmox task: UPID:node:pid:pstart:taskid:starttime:type:id:user:
type upid struct {
	task, id, user string
}

// parseUPID reads a task id from a file name or a path ending in one.
func parseUPID(name string) (upid, bool) {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	if !strings.HasPrefix(name, "UPID:") {
		return upid{}, false
	}
	f := strings.Split(name, ":")
	if len(f) < 9 {
		return upid{}, false
	}
	return upid{task: f[6], id: strip.Unescape(f[7]), user: f[8]}, true
}

// taskLevel reads a task result line: TASK OK is INFO, TASK WARNINGS is WARN, TASK ERROR is ERROR.
func taskLevel(line string) severity.Level {
	switch m := taskResult.FindStringSubmatch(line); {
	case m == nil:
		return severity.Level{}
	case m[1] == "OK":
		return wordLevel("info")
	case m[1] == "WARNINGS":
		return wordLevel("warn")
	}
	return wordLevel("error")
}

func fileUPID(r *rec) (upid, bool) {
	for _, key := range []string{"log.file.name", "log.file.path"} {
		if u, ok := parseUPID(attrText(r.lr.Attributes(), key)); ok {
			return u, true
		}
	}
	return upid{}, false
}

// pbsDaemons are the syslog identifiers of the Proxmox Backup Server daemons.
var pbsDaemons = []string{"proxmox-backup-proxy", "proxmox-backup-api", "proxmox-backup"}

// pbsPeer is the peer a daemon line starts with: "[[::ffff:192.0.2.10]:38835] text".
var pbsPeer = regexp.MustCompile(`^\[\[([^\]]+)\]:(\d+)\] (.+)$`)

// levelToken finds a level word in the text of a daemon line.
var levelToken = regexp.MustCompile(`(?i)\b(trace|debug|info|warn|warning|error|critical|fatal)\b`)

// pbsBranch shapes Proxmox Backup Server task log lines: "<time>: text" in a file named by the
// task id. The datastore prefixes the body and the task type is the service. A journald record of a
// daemon is claimed by its syslog identifier; a task log file name wins over the identifier.
type pbsBranch struct{}

func newPBSBranch() *pbsBranch { return &pbsBranch{} }

func (*pbsBranch) name() string { return "pbs" }

func (*pbsBranch) claims(r *rec) bool {
	if _, ok := fileUPID(r); ok {
		return pbsShape.MatchString(r.body)
	}
	return r.body != "" && slices.Contains(pbsDaemons, journalIdent(r.lr))
}

// parseDaemon shapes a daemon line: the identifier is the service, the journal pid is an integer
// attribute, a leading peer becomes the client address and port, and a level word in the text gives
// the severity.
func (*pbsBranch) parseDaemon(r *rec) {
	a := r.lr.Attributes()
	keepOriginal(a, r.body)
	if pid, ok := journalPID(r.lr); ok {
		a.PutInt("process.pid", pid)
	}
	text := r.body
	if m := pbsPeer.FindStringSubmatch(text); m != nil {
		a.PutStr("client.address", strings.TrimPrefix(m[1], "::ffff:"))
		if port, err := strconv.ParseInt(m[2], 10, 64); err == nil {
			a.PutInt("client.port", port)
		}
		text = m[3]
	}
	if m := levelToken.FindStringSubmatch(text); m != nil {
		applyLevel(r.lr, wordLevel(strings.ToLower(m[1])))
	}
	r.res.Attributes().PutStr("service.name", journalIdent(r.lr))
	r.lr.Body().SetStr(text)
}

func (b *pbsBranch) parse(r *rec) error {
	if _, ok := fileUPID(r); !ok && slices.Contains(pbsDaemons, journalIdent(r.lr)) {
		b.parseDaemon(r)
		return nil
	}
	u, ok := fileUPID(r)
	m := pbsShape.FindStringSubmatch(r.body)
	if !ok || m == nil {
		return errors.New("not a task log line")
	}
	ts, err := time.Parse(time.RFC3339, m[1])
	if err != nil {
		return err
	}
	a := r.lr.Attributes()
	a.PutStr("log.record.original", r.body)
	a.PutStr("vstan.pbs.task", u.task)
	if u.id != "" {
		a.PutStr("vstan.pbs.datastore", u.id)
	}
	if u.user != "" {
		a.PutStr("user.name", u.user)
	}
	r.res.Attributes().PutStr("service.name", u.task)
	r.lr.SetTimestamp(pcommon.NewTimestampFromTime(ts.UTC()))
	applyLevel(r.lr, taskLevel(m[2]))
	r.lr.Body().SetStr(body.Compose(u.id, m[2]))
	return nil
}
