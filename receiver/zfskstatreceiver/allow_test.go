// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Only the four read requests pass; the neighbours that write or destroy are refused.
func TestCheckVecAllowsOnlyTheFourReadRequests(t *testing.T) {
	allowed := []uint{0x05, 0x12, 0x14, 0x27}
	refused := map[uint]struct{}{}
	for vec := uint(0); vec < 0x200; vec++ {
		refused[vec] = struct{}{}
	}
	for _, vec := range allowed {
		delete(refused, vec)
	}

	for _, vec := range allowed {
		assert.NoError(t, checkVec(vec), "vec %#x", vec)
	}
	for vec := range refused {
		assert.ErrorIs(t, checkVec(vec), errIoctlRefused, "vec %#x", vec)
	}
}

func TestRequestNumbersAreThePinnedOpenZFSValues(t *testing.T) {
	assert.Equal(t, uint(0x05), vecPoolStats)
	assert.Equal(t, uint(0x12), vecObjsetStats)
	assert.Equal(t, uint(0x14), vecDatasetListNext)
	assert.Equal(t, uint(0x27), vecPoolGetProps)
}

func TestZcmdFieldsSitAtTheOpenZFSOffsets(t *testing.T) {
	z := newZcmd()

	require.NoError(t, z.setName("tank/data"))
	z.setDst(0x1122334455667788, 0x1000)
	z.setCookie(42)

	assert.Equal(t, "tank/data", z.name())
	assert.Equal(t, uint64(0x1000), z.dstSize())
	assert.Equal(t, uint64(42), z.cookie())
	assert.Equal(t, byte('t'), z.b[0])
	assert.Equal(t, byte(0x88), z.b[zcDstOff], "zc_nvlist_dst")
	assert.Equal(t, byte(0x10), z.b[zcDstSizeOff+1], "zc_nvlist_dst_size")
	assert.Equal(t, byte(42), z.b[zcCookieOff], "zc_cookie")
}

func TestZcmdRefusesANameThatDoesNotFit(t *testing.T) {
	z := newZcmd()

	assert.Error(t, z.setName(strings.Repeat("a", zcNameLen)))
	assert.NoError(t, z.setName(strings.Repeat("a", zcNameLen-1)))
}

func TestZcmdNameStopsAtTheFirstNul(t *testing.T) {
	z := newZcmd()
	copy(z.b[:], "pool/one\x00junk")

	assert.Equal(t, "pool/one", z.name())
}

func TestZcmdIsLargeEnoughForTheKernelStruct(t *testing.T) {
	assert.GreaterOrEqual(t, zcmdSize, 14*1024)
}

// ioctlSite is one function that passes unix.SYS_IOCTL to a call, with the
// source offsets of that use and of the first checkVec call in the function.
type ioctlSite struct {
	file, fn  string
	use, test token.Pos
}

// findIoctlSites parses every non-test source file, ignoring build tags, and
// reports each function that uses unix.SYS_IOCTL in code (not comments or strings).
func findIoctlSites(t *testing.T) []ioctlSite {
	t.Helper()
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	var sites []ioctlSite
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, err)
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			site := ioctlSite{file: name, fn: fd.Name.Name}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.SelectorExpr:
					if id, ok := x.X.(*ast.Ident); ok && id.Name == "unix" && x.Sel.Name == "SYS_IOCTL" && !site.use.IsValid() {
						site.use = x.Pos()
					}
				case *ast.CallExpr:
					if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "checkVec" && !site.test.IsValid() {
						site.test = x.Pos()
					}
				}
				return true
			})
			if site.use.IsValid() {
				sites = append(sites, site)
			}
		}
	}
	return sites
}

// Every function that makes the system call checks the request first.
func TestEverySyscallFileChecksTheRequestBeforeTheCall(t *testing.T) {
	sites := findIoctlSites(t)

	got := map[string]bool{}
	for _, s := range sites {
		got[s.file] = true
		assert.True(t, strings.HasPrefix(s.file, "ioctl_"), "%s makes a system call", s.file)
		assert.True(t, s.test.IsValid() && s.test < s.use, "%s.%s must call checkVec before the system call", s.file, s.fn)
	}
	assert.Equal(t, map[string]bool{"ioctl_linux.go": true, "ioctl_freebsd.go": true}, got,
		"only the Linux and FreeBSD files make the call")
}
