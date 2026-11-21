// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore upid taskid vstan

package logshapeprocessor

import (
	"errors"
	"regexp"
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

// pbsBranch shapes Proxmox Backup Server task log lines: "<time>: text" in a file named by the
// task id. The datastore prefixes the body and the task type is the service.
type pbsBranch struct{}

func newPBSBranch() *pbsBranch { return &pbsBranch{} }

func (*pbsBranch) name() string { return "pbs" }

func (*pbsBranch) claims(r *rec) bool {
	_, ok := fileUPID(r)
	return ok && pbsShape.MatchString(r.body)
}

func (*pbsBranch) parse(r *rec) error {
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
