// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwmapprocessor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The units of the approved mapping table: degrees Celsius, watts, bytes, a ratio and revolutions per
// minute, plus the units of the other hw.* semantic-convention metrics and the counted units of the
// interface error and packet counters.
func TestUnits_EveryUnitTheMappingTableNeedsLoads(t *testing.T) {
	for _, unit := range []string{"Cel", "W", "By", "1", "rpm", "V", "A", "s", "ms", "Hz", "{error}", "{packet}"} {
		r := goodRule("a")
		r.Unit = unit

		assert.NoError(t, (&Config{Rules: []Rule{r}}).Validate(), unit)
	}
}

// The list is closed: a unit the table does not use is added when a table row needs it.
func TestUnits_AUnitOutsideTheListIsRefusedWithTheRuleNamed(t *testing.T) {
	for _, unit := range []string{"J", "bit/s", "{client}", "%", "cel", "furlongs"} {
		r := goodRule("a")
		r.Unit = unit

		err := (&Config{Rules: []Rule{r}}).Validate()

		if assert.Error(t, err, unit) {
			assert.Contains(t, err.Error(), `rule "a"`, unit)
		}
	}
}
