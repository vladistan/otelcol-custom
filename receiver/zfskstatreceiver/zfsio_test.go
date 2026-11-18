// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDev is a manual stand-in for the control device; handler decides each reply.
type fakeDev struct {
	handler func(vec uint, name string, cookie uint64, dst []byte) (callReply, error)
	calls   []fakeCall
	closed  int
}

type fakeCall struct {
	vec    uint
	name   string
	cookie uint64
	dstLen int
}

func (f *fakeDev) call(vec uint, name string, cookie uint64, dst []byte) (callReply, error) {
	f.calls = append(f.calls, fakeCall{vec, name, cookie, len(dst)})
	return f.handler(vec, name, cookie, dst)
}

func (f *fakeDev) close() error { f.closed++; return nil }

// fill copies a packed list into the reply buffer the way the kernel does.
func fill(dst, packed []byte) callReply {
	n := copy(dst, packed)
	return callReply{size: n}
}

func TestRequestDecodesTheReplyAndPassesTheArguments(t *testing.T) {
	dev := &fakeDev{handler: func(_ uint, _ string, _ uint64, dst []byte) (callReply, error) {
		return fill(dst, newNV().u64("size", 77).bytes()), nil
	}}
	c := newZfsClient(dev)

	nv, _, err := c.request(vecPoolGetProps, "tank", 3)

	require.NoError(t, err)
	v, _ := nv.uint64Value("size")
	assert.Equal(t, uint64(77), v)
	assert.Equal(t, []fakeCall{{vecPoolGetProps, "tank", 3, initialReplyBytes}}, dev.calls)
}

func TestRequestGrowsTheBufferOnceWhenTheKernelSaysItIsTooSmall(t *testing.T) {
	dev := &fakeDev{}
	dev.handler = func(_ uint, _ string, _ uint64, dst []byte) (callReply, error) {
		if len(dst) < 100000 {
			return callReply{size: 100000}, syscall.ENOMEM
		}
		return fill(dst, newNV().u64("a", 1).bytes()), nil
	}
	c := newZfsClient(dev)

	nv, _, err := c.request(vecObjsetStats, "tank", 0)

	require.NoError(t, err)
	_, ok := nv.uint64Value("a")
	assert.True(t, ok)
	assert.Len(t, dev.calls, 2)
	assert.GreaterOrEqual(t, dev.calls[1].dstLen, 100000)
}

func TestRequestGivesUpWhenTheKernelKeepsSayingTooSmall(t *testing.T) {
	dev := &fakeDev{handler: func(_ uint, _ string, _ uint64, dst []byte) (callReply, error) {
		return callReply{size: len(dst)}, syscall.ENOMEM
	}}

	_, _, err := newZfsClient(dev).request(vecObjsetStats, "tank", 0)

	assert.ErrorIs(t, err, syscall.ENOMEM)
	assert.Len(t, dev.calls, 1)
}

func TestRequestRefusesAReplyAboveTheCap(t *testing.T) {
	dev := &fakeDev{handler: func(uint, string, uint64, []byte) (callReply, error) {
		return callReply{size: maxReplyBytes + 1}, syscall.ENOMEM
	}}

	_, _, err := newZfsClient(dev).request(vecObjsetStats, "tank", 0)

	assert.Error(t, err)
	assert.Len(t, dev.calls, 1)
}

func TestRequestPassesOtherErrorsThrough(t *testing.T) {
	dev := &fakeDev{handler: func(uint, string, uint64, []byte) (callReply, error) {
		return callReply{}, syscall.ENOENT
	}}

	_, _, err := newZfsClient(dev).request(vecPoolStats, "gone", 0)

	assert.ErrorIs(t, err, syscall.ENOENT)
}

func TestRequestWithAnEmptyReplyGivesNoListAndNoError(t *testing.T) {
	dev := &fakeDev{handler: func(uint, string, uint64, []byte) (callReply, error) {
		return callReply{}, nil
	}}

	nv, _, err := newZfsClient(dev).request(vecPoolStats, "tank", 0)

	require.NoError(t, err)
	assert.Nil(t, nv)
}

func TestRequestRejectsAReplyThatIsNotAnNvlist(t *testing.T) {
	dev := &fakeDev{handler: func(_ uint, _ string, _ uint64, dst []byte) (callReply, error) {
		return fill(dst, []byte("not an nvlist at all")), nil
	}}

	_, _, err := newZfsClient(dev).request(vecPoolStats, "tank", 0)

	assert.Error(t, err)
}

func TestRequestRejectsASizeLargerThanTheBuffer(t *testing.T) {
	dev := &fakeDev{handler: func(_ uint, _ string, _ uint64, dst []byte) (callReply, error) {
		return callReply{size: len(dst) + 1}, nil
	}}

	_, _, err := newZfsClient(dev).request(vecPoolStats, "tank", 0)

	assert.Error(t, err)
}

func listNextDev(children []string) *fakeDev {
	return &fakeDev{handler: func(_ uint, _ string, cookie uint64, dst []byte) (callReply, error) {
		if int(cookie) >= len(children) {
			return callReply{}, syscall.ESRCH
		}
		r := fill(dst, newNV().list("used", func(e *nvEnc) { e.u64("value", cookie) }).bytes())
		r.name = children[cookie]
		r.cookie = cookie + 1
		return r, nil
	}}
}

func poolStatsDev(cookie uint64, packed []byte) *fakeDev {
	return &fakeDev{handler: func(_ uint, _ string, _ uint64, dst []byte) (callReply, error) {
		r := fill(dst, packed)
		r.cookie = cookie
		return r, nil
	}}
}

func TestAPoolStatsReplyThatCarriesAnErrnoInTheCookieIsAnError(t *testing.T) {
	dev := poolStatsDev(uint64(syscall.EAGAIN), newNV().u64("suspended", 1).bytes())

	nv, err := newZfsClient(dev).poolStats("tank")

	assert.ErrorIs(t, err, syscall.EAGAIN)
	suspended, _ := nv.uint64Value("suspended")
	assert.Equal(t, uint64(1), suspended, "the partial reply is still returned")
}

func TestAPoolStatsReplyWithNoErrnoIsFine(t *testing.T) {
	dev := poolStatsDev(0, newNV().u64("suspended", 0).bytes())

	_, err := newZfsClient(dev).poolStats("tank")

	assert.NoError(t, err)
}

func TestTheCookieOfAnotherRequestIsNotAnErrno(t *testing.T) {
	dev := poolStatsDev(7, newNV().bytes())

	_, err := newZfsClient(dev).poolProps("tank")

	assert.NoError(t, err)
}

func TestChildrenDecodesOnlyWhenAskedAndAGarbageReplyOfAnUnselectedChildIsHarmless(t *testing.T) {
	dev := &fakeDev{handler: func(_ uint, _ string, cookie uint64, dst []byte) (callReply, error) {
		if cookie >= 2 {
			return callReply{}, syscall.ESRCH
		}
		r := fill(dst, []byte("garbage that is not an nvlist"))
		r.name, r.cookie = "tank/x", cookie+1
		return r, nil
	}}
	asked := 0

	err := newZfsClient(dev).children("tank", func(_ string, props func() (*nvList, error)) bool {
		asked++
		if asked == 2 {
			_, decodeErr := props()
			assert.Error(t, decodeErr, "asking decodes, and garbage is an error")
		}
		return true
	})

	require.NoError(t, err)
	assert.Equal(t, 2, asked)
}

func TestChildrenWalksTheCookieToTheEnd(t *testing.T) {
	dev := listNextDev([]string{"tank/a", "tank/b", "tank/c"})
	var got []string

	err := newZfsClient(dev).children("tank", func(name string, props func() (*nvList, error)) bool {
		got = append(got, name)
		nv, err := props()
		require.NoError(t, err)
		used, _ := nv.subList("used")
		value, _ := used.uint64Value("value")
		assert.Equal(t, uint64(len(got)-1), value, "the props are the ones of this child")
		return true
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"tank/a", "tank/b", "tank/c"}, got)
	assert.Equal(t, uint64(2), dev.calls[2].cookie)
	for _, c := range dev.calls {
		assert.Equal(t, "tank", c.name, "the parent name is sent each time")
	}
}

func TestChildrenStopsWhenTheCallbackSaysSo(t *testing.T) {
	dev := listNextDev([]string{"tank/a", "tank/b"})

	err := newZfsClient(dev).children("tank", func(string, func() (*nvList, error)) bool { return false })

	require.NoError(t, err)
	assert.Len(t, dev.calls, 1)
}

func TestChildrenWithNoChildrenIsNotAnError(t *testing.T) {
	err := newZfsClient(listNextDev(nil)).children("tank", func(string, func() (*nvList, error)) bool { return true })

	assert.NoError(t, err)
}

func TestChildrenReturnsADeviceError(t *testing.T) {
	dev := &fakeDev{handler: func(uint, string, uint64, []byte) (callReply, error) {
		return callReply{}, syscall.EIO
	}}

	err := newZfsClient(dev).children("tank", func(string, func() (*nvList, error)) bool { return true })

	assert.ErrorIs(t, err, syscall.EIO)
}

func TestChildrenStopsAKernelThatNeverEnds(t *testing.T) {
	dev := &fakeDev{handler: func(_ uint, _ string, cookie uint64, dst []byte) (callReply, error) {
		r := fill(dst, newNV().bytes())
		r.name, r.cookie = "tank/x", cookie+1
		return r, nil
	}}

	err := newZfsClient(dev).children("tank", func(string, func() (*nvList, error)) bool { return true })

	assert.ErrorIs(t, err, errTooManyChildren)
}

func TestTheNamedRequestsUseTheAllowListedNumbers(t *testing.T) {
	dev := &fakeDev{handler: func(_ uint, _ string, _ uint64, dst []byte) (callReply, error) {
		return fill(dst, newNV().bytes()), nil
	}}
	c := newZfsClient(dev)

	_, _ = c.poolStats("tank")
	_, _ = c.poolProps("tank")
	_, _ = c.objsetStats("tank/data")

	var vecs []uint
	for _, call := range dev.calls {
		vecs = append(vecs, call.vec)
		assert.NoError(t, checkVec(call.vec))
	}
	assert.Equal(t, []uint{vecPoolStats, vecPoolGetProps, vecObjsetStats}, vecs)
}

func TestCloseClosesTheDevice(t *testing.T) {
	dev := &fakeDev{}

	require.NoError(t, newZfsClient(dev).close())

	assert.Equal(t, 1, dev.closed)
}
