// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"regexp"
	"strconv"
	"strings"
)

// opcodeNames is the Windows event opcode table (winmeta). A provider that
// defines its own opcodes overrides it per event in providerLabels.
var opcodeNames = map[string]string{
	"0": "Info", "1": "Start", "2": "Stop", "3": "DC Start", "4": "DC Stop",
	"5": "Extension", "6": "Reply", "7": "Resume", "8": "Suspend", "9": "Send", "240": "Receive",
}

type eventLabels struct{ task, opcode string }

// providerLabels holds task and opcode text a provider defines for one event,
// keyed by lower-case short provider name and event id.
var providerLabels = map[string]eventLabels{
	"powershell/4104": {task: "Execute a Remote Command", opcode: "On create calls"},
}

// headerLabels returns the task and opcode text. A provider table entry for
// the event wins over whatever the receiver rendered, because the rendered
// text of some events belongs to another event. Without an entry, rendered
// text is kept and a bare number is replaced from the opcode table.
func headerLabels(provider string, id int64, task, opcode string) (string, string) {
	if known, ok := providerLabels[shortProvider(provider)+"/"+strconv.FormatInt(id, 10)]; ok {
		if known.task != "" {
			task = known.task
		}
		if known.opcode != "" {
			opcode = known.opcode
		}
		return task, opcode
	}
	if isRawDigits(opcode) {
		if name, ok := opcodeNames[opcode]; ok {
			opcode = name
		}
	}
	return task, opcode
}

var placeholder = regexp.MustCompile(`%[1-7]\b`)

// hasUnsubstitutedPlaceholders reports a Firewall message that still carries
// %1..%7 and has no event data to fill them from.
func hasUnsubstitutedPlaceholders(provider, message string, data []dataField) bool {
	return len(data) == 0 && strings.Contains(strings.ToLower(provider), "firewall") && placeholder.MatchString(message)
}
