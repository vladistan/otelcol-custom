// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ipmireceiver

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
)

const (
	adapterFile  = "adapter.go"
	goIPMIImport = "github.com/bougou/go-ipmi"
)

// go-ipmi calls the adapter may make; any other call on a go-ipmi value is refused, whatever its name.
var allowedIPMICalls = map[string]bool{
	"Connect": true, "ConnectAuto": true, "Close": true,
	"GetDeviceID": true, "GetSDRsMap": true, "GetSensorReading": true, "GetDCMIPowerReading": true, "GetSDRRepoInfo": true,
	"GetFRUInventoryAreaInfo": true, "ReadFRUData": true, "GetSDRsRaw": true,
	"WithTimeout": true, "WithRetry": true, "WithInterface": true, "WithCipherSuiteID": true, "WithMaxPrivilegeLevel": true,
	"WithCommandContext": true, "WithResponderAddr": true, "WithResponderLUN": true, "NewClient": true,
	"ConvertReading": true, "TrueEvents": true, "IsResponseError": true, "CipherSuiteID": true,
}

// valueCalls are methods of plain values (errors, records, times). Every other method call in the
// adapter must be on allowedIPMICalls, so a client held in any variable is still checked.
var valueCalls = map[string]bool{
	"Error": true, "StatusCode": true, "CompletionCode": true, "HasAnalogReading": true, "Timeout": true,
	"Equal": true, "After": true, "IsZero": true, "OwnerID": true, "LUN": true, "Unwrap": true,
}

// packageNames maps the local name of each import of a file to whether it is a go-ipmi package.
func packageNames(f *ast.File) map[string]bool {
	out := map[string]bool{}
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		name := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		out[name] = strings.HasPrefix(path, goIPMIImport)
	}
	return out
}

// sourceViolations returns the rules the non-test sources (name to content) break.
func sourceViolations(files map[string]string) []string {
	var out []string
	metricNames := map[string]bool{}
	for _, n := range hwschema.BMCMetricNames() {
		metricNames[n] = true
	}
	for name, src := range files {
		f, err := parser.ParseFile(token.NewFileSet(), name, src, 0)
		if err != nil {
			out = append(out, name+": does not parse: "+err.Error())
			continue
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			switch {
			case strings.HasPrefix(path, goIPMIImport) && name != adapterFile:
				out = append(out, name+": imports go-ipmi outside the adapter")
			case path == "os/exec" || path == "C":
				out = append(out, name+": imports "+path)
			}
		}
		packages := packageNames(f)
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.BasicLit:
				if x.Kind == token.STRING {
					if s, err := strconv.Unquote(x.Value); err == nil && metricNames[s] {
						out = append(out, name+": writes the metric name "+s)
					}
				}
			case *ast.CallExpr:
				sel, ok := x.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if name == adapterFile {
					out = append(out, adapterCallViolation(sel, packages, name)...)
				}
				if lower := strings.ToLower(sel.Sel.Name); strings.Contains(lower, "probe") && strings.Contains(lower, "cipher") {
					out = append(out, name+": probes cipher suites")
				}
			}
			return true
		})
	}
	sort.Strings(out)
	return out
}

// adapterCallViolation refuses a call that is neither on a package other than go-ipmi nor on the lists.
func adapterCallViolation(sel *ast.SelectorExpr, packages map[string]bool, file string) []string {
	if id, ok := sel.X.(*ast.Ident); ok {
		if isIPMI, isPackage := packages[id.Name]; isPackage && !isIPMI {
			return nil
		}
	}
	if allowedIPMICalls[sel.Sel.Name] || valueCalls[sel.Sel.Name] {
		return nil
	}
	return []string{file + ": calls " + sel.Sel.Name + ", which is not on the list of reads"}
}

func TestTheReceiverSourcePassesItsOwnRules(t *testing.T) {
	files := map[string]string{}
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Clean(e.Name()))
		require.NoError(t, err)
		files[e.Name()] = string(b)
	}
	require.Contains(t, files, adapterFile)

	assert.Empty(t, sourceViolations(files))
}

func TestSourceRuleFailsOnAFixtureThatCallsColdReset(t *testing.T) {
	got := sourceViolations(map[string]string{adapterFile: `package x
func f(c interface{ ColdReset() }) { c.ColdReset() }`})

	assert.Contains(t, got, "adapter.go: calls ColdReset, which is not on the list of reads")
}

func TestSourceRuleFailsOnACallThatNoPrefixRuleWouldCatch(t *testing.T) {
	for _, call := range []string{"Exchange", "Reboot", "Shutdown", "RawCommand", "Do"} {
		got := sourceViolations(map[string]string{adapterFile: "package x\nfunc f(c interface{ " + call + "() }) { c." + call + "() }"})

		assert.Contains(t, got, "adapter.go: calls "+call+", which is not on the list of reads")
	}
}

func TestSourceRuleFailsOnACallThroughAnyVariableOrACallResult(t *testing.T) {
	for name, body := range map[string]string{
		"other variable": "func f(conn interface{ Reboot() }) { conn.Reboot() }",
		"call result":    "func f(get func() interface{ Reboot() }) { get().Reboot() }",
		"field":          "type s struct{ session interface{ Reboot() } }\nfunc (x s) f() { x.session.Reboot() }",
	} {
		t.Run(name, func(t *testing.T) {
			got := sourceViolations(map[string]string{adapterFile: "package x\n" + body})

			assert.Contains(t, got, "adapter.go: calls Reboot, which is not on the list of reads")
		})
	}
}

func TestSourceRuleFailsOnAGoIPMIPackageFunctionThatIsNotOnTheList(t *testing.T) {
	got := sourceViolations(map[string]string{adapterFile: `package x
import "github.com/bougou/go-ipmi/pkg/client"
func f() { client.NewClientWithAdmin() }`})

	assert.Contains(t, got, "adapter.go: calls NewClientWithAdmin, which is not on the list of reads")
}

func TestSourceRuleFailsOnAFixtureThatCallsChassisControl(t *testing.T) {
	got := sourceViolations(map[string]string{adapterFile: `package x
func f(c interface{ ChassisControl() }) { c.ChassisControl() }`})

	assert.Contains(t, got, "adapter.go: calls ChassisControl, which is not on the list of reads")
}

func TestSourceRuleFailsOnAFileOtherThanTheAdapterThatImportsGoIPMI(t *testing.T) {
	got := sourceViolations(map[string]string{"target.go": `package x
import _ "github.com/bougou/go-ipmi/pkg/client"`})

	assert.Contains(t, got, "target.go: imports go-ipmi outside the adapter")
}

func TestSourceRuleAcceptsTheAllowedReadsInTheAdapter(t *testing.T) {
	got := sourceViolations(map[string]string{adapterFile: `package x
import _ "github.com/bougou/go-ipmi/pkg/client"
func f(c interface{ GetDeviceID(); GetSDRsMap(); GetSensorReading(); GetDCMIPowerReading() }) {
	c.GetDeviceID(); c.GetSDRsMap(); c.GetSensorReading(); c.GetDCMIPowerReading()
}`})

	assert.Empty(t, got)
}

func TestSourceRuleAcceptsTheReadOnlyFRUCalls(t *testing.T) {
	got := sourceViolations(map[string]string{adapterFile: `package x
import _ "github.com/bougou/go-ipmi/pkg/client"
func f(c interface{ GetFRUInventoryAreaInfo(); ReadFRUData(); GetSDRsRaw() }) {
	c.GetFRUInventoryAreaInfo(); c.ReadFRUData(); c.GetSDRsRaw()
}`})

	assert.Empty(t, got)
}

func TestSourceRuleFailsOnFRUWriteAndOtherStorageCommands(t *testing.T) {
	for _, call := range []string{"WriteFRUData", "ClearSEL", "DeleteSDR", "ColdReset", "SetSystemBootOptions"} {
		got := sourceViolations(map[string]string{adapterFile: "package x\nfunc f(c interface{ " + call + "() }) { c." + call + "() }"})

		assert.Contains(t, got, "adapter.go: calls "+call+", which is not on the list of reads")
	}
}

func TestSourceRuleFailsOnTheFRUMetricNamesWrittenOutsideHwschema(t *testing.T) {
	got := sourceViolations(map[string]string{"x.go": `package x
var a = "inventory.bmc.fru.info"
var b = "inventory.collection.status"`})

	assert.Contains(t, got, "x.go: writes the metric name inventory.bmc.fru.info")
	assert.Contains(t, got, "x.go: writes the metric name inventory.collection.status")
}

func TestSourceRuleFailsOnExecCgoAndAMetricNameOutsideHwschema(t *testing.T) {
	got := sourceViolations(map[string]string{"x.go": `package x
import "os/exec"
var _ = exec.Command
var name = "hw.temperature"`})

	assert.Contains(t, got, "x.go: imports os/exec")
	assert.Contains(t, got, "x.go: writes the metric name hw.temperature")
}

func TestSourceRuleFailsOnCipherProbing(t *testing.T) {
	got := sourceViolations(map[string]string{"x.go": `package x
func probeCipherSuites() {}
func g() { probeCipherSuites() }
func h(s struct{ ProbeCipherSuites func() }) { s.ProbeCipherSuites() }`})

	assert.Contains(t, got, "x.go: probes cipher suites")
}

func TestTheTargetImportsHwschemaToBuildItsPoints(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "target.go", nil, parser.ImportsOnly)
	require.NoError(t, err)

	var imports []string
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		require.NoError(t, err)
		imports = append(imports, path)
	}

	assert.Contains(t, imports, "github.com/vladistan/otelcol-custom/receiver/hwschema")
}
