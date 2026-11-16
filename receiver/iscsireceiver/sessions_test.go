// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package iscsireceiver

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// classTree is a fake /sys/class with the two iSCSI classes.
type classTree struct {
	t    *testing.T
	root string
}

func newClassTree(t *testing.T) *classTree { return &classTree{t: t, root: t.TempDir()} }

func (c *classTree) write(rel, content string) {
	c.t.Helper()
	p := filepath.Join(c.root, rel)
	require.NoError(c.t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(c.t, os.WriteFile(p, []byte(content+"\n"), 0o644))
}

// session adds session<n> with the files a logged-in session has, and the
// credential files that must never be opened.
func (c *classTree) session(n, target, tpgt, state string) {
	c.t.Helper()
	dir := "iscsi_session/session" + n + "/"
	c.write(dir+"targetname", target)
	c.write(dir+"tpgt", tpgt)
	c.write(dir+"state", state)
	c.write(dir+"initiatorname", "iqn.1993-08.org.debian:01:abc")
	c.write(dir+"ifacename", "default")
	c.write(dir+"username", "chap-user")
	c.write(dir+"password", "chap-secret")
	c.write(dir+"password_in", "chap-secret-in")
}

func (c *classTree) connection(session, conn, addr, port, state string) {
	c.t.Helper()
	dir := "iscsi_connection/connection" + session + ":" + conn + "/"
	c.write(dir+"persistent_address", addr)
	c.write(dir+"persistent_port", port)
	c.write(dir+"state", state)
	c.write(dir+"ping_tmo", "5")
}

func newTestReader(c *classTree) *sysfsReader {
	return newSysfsReader(zap.NewNop(), c.root)
}

func TestReadsASessionWithItsConnection(t *testing.T) {
	c := newClassTree(t)
	c.session("1", "iqn.2004-04.com.example:nas:iscsi.lun1.abc", "1", "LOGGED_IN")
	c.connection("1", "0", "192.0.2.10", "3260", "LOGGED_IN")

	got, err := newTestReader(c).sessions()

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, session{
		target: "iqn.2004-04.com.example:nas:iscsi.lun1.abc", tpgt: "1", state: "LOGGED_IN", portal: "192.0.2.10:3260",
		connections: []connection{{portal: "192.0.2.10:3260", state: "LOGGED_IN"}},
	}, got[0])
}

func TestAnIPv6PortalIsBracketed(t *testing.T) {
	c := newClassTree(t)
	c.session("1", "iqn.x", "1", "LOGGED_IN")
	c.connection("1", "0", "2001:db8::10", "3260", "LOGGED_IN")

	got, err := newTestReader(c).sessions()

	require.NoError(t, err)
	assert.Equal(t, "[2001:db8::10]:3260", got[0].portal)
}

func TestSessionStatesPassThrough(t *testing.T) {
	c := newClassTree(t)
	c.session("1", "iqn.up", "1", "LOGGED_IN")
	c.connection("1", "0", "192.0.2.10", "3260", "LOGGED_IN")
	c.session("2", "iqn.down", "2", "FAILED")
	c.connection("2", "0", "192.0.2.11", "3260", "FAILED")

	got, err := newTestReader(c).sessions()

	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "LOGGED_IN", got[0].state)
	assert.Equal(t, "FAILED", got[1].state)
	assert.Equal(t, "2", got[1].tpgt)
}

func TestConnectionsJoinTheirSessionByNumber(t *testing.T) {
	c := newClassTree(t)
	c.session("1", "iqn.a", "1", "LOGGED_IN")
	c.session("12", "iqn.b", "1", "LOGGED_IN")
	c.connection("1", "0", "192.0.2.1", "3260", "LOGGED_IN")
	c.connection("12", "0", "192.0.2.12", "3260", "LOGGED_IN")
	c.connection("12", "1", "192.0.2.13", "3260", "FAILED")

	got, err := newTestReader(c).sessions()

	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Len(t, got[0].connections, 1)
	assert.Len(t, got[1].connections, 2)
	assert.Equal(t, "192.0.2.12:3260", got[1].portal)
}

func TestASessionWithoutAConnectionHasNoPortal(t *testing.T) {
	c := newClassTree(t)
	c.session("1", "iqn.a", "1", "FREE")

	got, err := newTestReader(c).sessions()

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Empty(t, got[0].portal)
	assert.Empty(t, got[0].connections)
}

func TestNeverOpensAFileOutsideTheAllowList(t *testing.T) {
	c := newClassTree(t)
	c.session("1", "iqn.a", "1", "LOGGED_IN")
	c.connection("1", "0", "192.0.2.10", "3260", "LOGGED_IN")
	r := newTestReader(c)
	allowed := map[string]bool{
		"targetname": true, "tpgt": true, "state": true, "initiatorname": true, "ifacename": true,
		"persistent_address": true, "persistent_port": true,
	}
	var opened []string
	inner := r.readFile
	r.readFile = func(p string) ([]byte, error) {
		opened = append(opened, filepath.Base(p))
		if !allowed[filepath.Base(p)] {
			return nil, errors.New("opened a file outside the allow-list: " + p)
		}
		return inner(p)
	}

	got, err := r.sessions()

	require.NoError(t, err)
	require.Len(t, got, 1)
	for _, name := range opened {
		assert.True(t, allowed[name], name)
	}
	assert.NotContains(t, opened, "username")
	assert.NotContains(t, opened, "password")
	assert.NotContains(t, opened, "password_in")
}

func TestASessionThatCannotBeReadIsSkipped(t *testing.T) {
	c := newClassTree(t)
	c.session("1", "iqn.a", "1", "LOGGED_IN")
	c.session("2", "iqn.b", "1", "LOGGED_IN")
	r := newTestReader(c)
	inner := r.readFile
	r.readFile = func(p string) ([]byte, error) {
		if filepath.Base(filepath.Dir(p)) == "session1" {
			return nil, errors.New("input/output error")
		}
		return inner(p)
	}

	got, err := r.sessions()

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "iqn.b", got[0].target)
}

func TestNoSessionClassGivesNoSessions(t *testing.T) {
	c := newClassTree(t)

	got, err := newTestReader(c).sessions()

	assert.ErrorIs(t, err, errNoSessions)
	assert.Empty(t, got)
}

func TestEmptySessionClassGivesNoSessions(t *testing.T) {
	c := newClassTree(t)
	require.NoError(t, os.MkdirAll(filepath.Join(c.root, "iscsi_session"), 0o755))

	_, err := newTestReader(c).sessions()

	assert.ErrorIs(t, err, errNoSessions)
}

func TestAConnectionThatCannotBeReadIsSkippedAndTheSessionKeepsTheOthers(t *testing.T) {
	c := newClassTree(t)
	c.session("1", "iqn.a", "1", "LOGGED_IN")
	c.connection("1", "0", "192.0.2.10", "3260", "LOGGED_IN")
	c.connection("1", "1", "192.0.2.11", "3260", "LOGGED_IN")
	r := newTestReader(c)
	inner := r.readFile
	r.readFile = func(p string) ([]byte, error) {
		if filepath.Base(filepath.Dir(p)) == "connection1:0" {
			return nil, errors.New("input/output error")
		}
		return inner(p)
	}

	got, err := r.sessions()

	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Len(t, got[0].connections, 1)
	assert.Equal(t, "192.0.2.11:3260", got[0].portal)
}

func TestASessionMissingAFileIsSkipped(t *testing.T) {
	for _, missing := range []string{"targetname", "tpgt", "state"} {
		t.Run(missing, func(t *testing.T) {
			c := newClassTree(t)
			c.session("1", "iqn.a", "1", "LOGGED_IN")
			c.session("2", "iqn.b", "1", "LOGGED_IN")
			require.NoError(t, os.Remove(filepath.Join(c.root, "iscsi_session", "session1", missing)))

			got, err := newTestReader(c).sessions()

			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, "iqn.b", got[0].target)
		})
	}
}

func TestAConnectionMissingAFileIsSkipped(t *testing.T) {
	for _, missing := range []string{"persistent_address", "persistent_port", "state"} {
		t.Run(missing, func(t *testing.T) {
			c := newClassTree(t)
			c.session("1", "iqn.a", "1", "LOGGED_IN")
			c.connection("1", "0", "192.0.2.10", "3260", "LOGGED_IN")
			require.NoError(t, os.Remove(filepath.Join(c.root, "iscsi_connection", "connection1:0", missing)))

			got, err := newTestReader(c).sessions()

			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Empty(t, got[0].connections)
		})
	}
}

func TestAnUnreadableSessionClassIsAnErrorNotNoSessions(t *testing.T) {
	c := newClassTree(t)
	c.session("1", "iqn.a", "1", "LOGGED_IN")
	r := newTestReader(c)
	r.readDir = func(string) ([]os.DirEntry, error) {
		return nil, &os.PathError{Op: "open", Path: c.root, Err: syscall.EACCES}
	}

	_, err := r.sessions()

	require.Error(t, err)
	assert.NotErrorIs(t, err, errNoSessions)
	assert.ErrorIs(t, err, syscall.EACCES)
}

func TestSessionsAreReadInNumericOrder(t *testing.T) {
	c := newClassTree(t)
	c.session("10", "iqn.ten", "1", "LOGGED_IN")
	c.session("2", "iqn.two", "1", "LOGGED_IN")

	got, err := newTestReader(c).sessions()

	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "iqn.two", got[0].target)
	assert.Equal(t, "iqn.ten", got[1].target)
}
