// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"errors"
	"fmt"
	"syscall"
)

const (
	initialReplyBytes = 64 * 1024
	maxReplyBytes     = 16 * 1024 * 1024
	maxChildren       = 100000
)

var (
	errTooManyChildren  = errors.New("a dataset lists more children than the limit")
	errIoctlUnsupported = errors.New("ZFS ioctls are not supported on this system")
)

// callReply is what the device gives back besides the packed list.
type callReply struct {
	name   string // the name the kernel wrote back; the child for a list request
	cookie uint64 // where the next list request resumes
	size   int    // bytes of the packed list, or the bytes needed when the buffer was too small
}

// zfsDevice sends one allow-listed request; only the Linux and FreeBSD files implement it.
type zfsDevice interface {
	call(vec uint, name string, cookie uint64, dst []byte) (callReply, error)
	close() error
}

// zfsClient turns requests into decoded lists and owns the reply buffer. It is used from one goroutine only.
type zfsClient struct {
	dev zfsDevice
	buf []byte
}

func newZfsClient(dev zfsDevice) *zfsClient {
	return &zfsClient{dev: dev, buf: make([]byte, initialReplyBytes)}
}

func (c *zfsClient) close() error { return c.dev.close() }

// send sends one request; the buffer grows once when the kernel asks for more. The packed reply is
// in the client buffer until the next send.
func (c *zfsClient) send(vec uint, name string, cookie uint64) (callReply, error) {
	reply, err := c.dev.call(vec, name, cookie, c.buf)
	if errors.Is(err, syscall.ENOMEM) && reply.size > len(c.buf) {
		if reply.size > maxReplyBytes {
			return reply, fmt.Errorf("reply of %d bytes is above the limit: %w", reply.size, err)
		}
		c.buf = make([]byte, reply.size+4096)
		reply, err = c.dev.call(vec, name, cookie, c.buf)
	}
	if err != nil {
		return reply, err
	}
	if reply.size < 0 || reply.size > len(c.buf) {
		return reply, fmt.Errorf("reply size %d does not fit its buffer", reply.size)
	}
	return reply, nil
}

// decode reads the packed list of the last send; an empty reply gives no list.
func (c *zfsClient) decode(reply callReply) (*nvList, error) {
	if reply.size == 0 {
		return nil, nil
	}
	return decodeNVList(c.buf[:reply.size])
}

// request sends one request and decodes the reply.
func (c *zfsClient) request(vec uint, name string, cookie uint64) (*nvList, callReply, error) {
	reply, err := c.send(vec, name, cookie)
	if err != nil {
		return nil, reply, err
	}
	nv, err := c.decode(reply)
	return nv, reply, err
}

// poolStats returns the pool configuration. The kernel can answer success with the configuration and
// leave the real error in the cookie; that error is returned with the partial configuration.
func (c *zfsClient) poolStats(pool string) (*nvList, error) {
	nv, reply, err := c.request(vecPoolStats, pool, 0)
	if err == nil && reply.cookie != 0 {
		err = syscall.Errno(reply.cookie)
	}
	return nv, err
}

func (c *zfsClient) poolProps(pool string) (*nvList, error) {
	nv, _, err := c.request(vecPoolGetProps, pool, 0)
	return nv, err
}

func (c *zfsClient) objsetStats(name string) (*nvList, error) {
	nv, _, err := c.request(vecObjsetStats, name, 0)
	return nv, err
}

// children calls fn for each filesystem and volume directly under parent; fn returns false to stop.
// props decodes the properties of that child and is valid only until fn returns, so a caller that
// does not want a child pays no decoding for it.
func (c *zfsClient) children(parent string, fn func(name string, props func() (*nvList, error)) bool) error {
	var cookie uint64
	for range maxChildren {
		reply, err := c.send(vecDatasetListNext, parent, cookie)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil {
			return err
		}
		if !fn(reply.name, func() (*nvList, error) { return c.decode(reply) }) {
			return nil
		}
		cookie = reply.cookie
	}
	return errTooManyChildren
}
