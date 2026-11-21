// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore upid vstan

package logshapeprocessor

import (
	"errors"
	"strings"
)

// pveBranch shapes a whole Proxmox VE task log sent as one record: the severity comes from the
// result on the last line and the task type and user from the task id. The body is kept whole.
type pveBranch struct{}

func newPVEBranch() *pveBranch { return &pveBranch{} }

func (*pveBranch) name() string { return "pve" }

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, " \t\r\n"), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func (*pveBranch) claims(r *rec) bool {
	_, ok := fileUPID(r)
	return ok && taskResult.MatchString(lastLine(r.body))
}

func (*pveBranch) parse(r *rec) error {
	u, ok := fileUPID(r)
	if !ok {
		return errors.New("no task id for the task log")
	}
	a := r.lr.Attributes()
	a.PutStr("vstan.pve.task", u.task)
	if u.user != "" {
		a.PutStr("user.name", u.user)
	}
	applyLevel(r.lr, taskLevel(lastLine(r.body)))
	return nil
}
