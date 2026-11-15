// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build windows

package fswatchreceiver

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/windows"
)

const (
	// Names only: writes are not wanted and add directory-write noise.
	notifyMask = windows.FILE_NOTIFY_CHANGE_FILE_NAME | windows.FILE_NOTIFY_CHANGE_DIR_NAME
	// remoteBufferLimit is the largest buffer SMB guarantees.
	remoteBufferLimit = 64 * 1024
)

// dirWatcher holds one overlapped ReadDirectoryChangesW handle on a root, subtree on.
// The buffer and the Overlapped live in the struct, so the kernel never writes to a moved stack.
type dirWatcher struct {
	handle     windows.Handle
	event      windows.Handle
	overlapped windows.Overlapped
	buf        []byte

	msgs      chan watchMsg
	stop      chan struct{}
	done      chan struct{}
	isClosing atomic.Bool
	closeOnce sync.Once
}

func openWatcher(root string, bufferSize int) (watcher, error) {
	path, err := windows.UTF16PtrFromString(longPath(root))
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(path, windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", root, err)
	}
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("create event: %w", err)
	}
	if bufferSize > remoteBufferLimit && isRemote(root) {
		bufferSize = remoteBufferLimit
	}
	w := &dirWatcher{
		handle: handle, event: event, buf: make([]byte, bufferSize),
		msgs: make(chan watchMsg), stop: make(chan struct{}), done: make(chan struct{}),
	}
	go w.loop()
	return w, nil
}

func isRemote(root string) bool {
	if isUNC(root) {
		return true
	}
	if len(root) < 3 {
		return false
	}
	drive, err := windows.UTF16PtrFromString(root[:3])
	return err == nil && windows.GetDriveType(drive) == windows.DRIVE_REMOTE
}

func (w *dirWatcher) Messages() <-chan watchMsg { return w.msgs }

func (w *dirWatcher) loop() {
	defer close(w.done)
	defer close(w.msgs)
	for !w.isClosing.Load() {
		w.overlapped = windows.Overlapped{HEvent: w.event}
		err := windows.ReadDirectoryChanges(w.handle, &w.buf[0], uint32(len(w.buf)), true, notifyMask, nil, &w.overlapped, 0)
		var bytesReturned uint32
		if err == nil {
			err = windows.GetOverlappedResult(w.handle, &w.overlapped, &bytesReturned, true)
		}
		switch {
		case w.isClosing.Load():
			return
		case errors.Is(err, windows.ERROR_NOTIFY_ENUM_DIR), err == nil && bytesReturned == 0:
			w.send(watchMsg{overflow: true})
		case err != nil:
			w.send(watchMsg{err: err})
			return
		default:
			w.send(watchMsg{events: decodeNotifications(w.buf[:bytesReturned])})
		}
	}
}

// send blocks while the engine is busy; the kernel buffer then fills and the next read reports the overflow.
func (w *dirWatcher) send(m watchMsg) {
	select {
	case w.msgs <- m:
	case <-w.stop:
	}
}

// Close cancels the pending read, waits for the loop to end, and frees the handles. CancelIoEx is
// repeated because a cancel that lands before the read is issued has nothing to cancel.
func (w *dirWatcher) Close() error {
	w.closeOnce.Do(func() {
		w.isClosing.Store(true)
		close(w.stop)
		for {
			_ = windows.CancelIoEx(w.handle, nil)
			select {
			case <-w.done:
				_ = windows.CloseHandle(w.handle)
				_ = windows.CloseHandle(w.event)
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	})
	return nil
}
