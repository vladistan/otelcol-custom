// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package sqlserver

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// AllowlistProcessor is the processor every log pipeline with a sqlserver receiver must run: it keeps only the
// listed attributes, so no query or plan text can leave under any key.
const AllowlistProcessor = "transform/sqlserver_query_allowlist"

func isSQLServer(id string) bool { return id == "sqlserver" || strings.HasPrefix(id, "sqlserver/") }

func isLogsPipeline(id string) bool { return id == "logs" || strings.HasPrefix(id, "logs/") }

type pipeline struct{ receivers, processors, exporters []string }

// stringList reads an optional list of strings. A value of another type is a problem, never skipped.
func stringList(v any, where string, problems *[]string) []string {
	if v == nil {
		return nil
	}
	items, ok := v.([]any)
	if !ok {
		*problems = append(*problems, fmt.Sprintf("%s is malformed: %T, not a list", where, v))
		return nil
	}
	out := make([]string, 0, len(items))
	for i, item := range items {
		s, ok := item.(string)
		if !ok {
			*problems = append(*problems, fmt.Sprintf("%s[%d] is malformed: %T, not a string", where, i, item))
			continue
		}
		out = append(out, s)
	}
	return out
}

func parsePipeline(id string, v any, problems *[]string) pipeline {
	body, ok := v.(map[string]any)
	if !ok && v != nil {
		*problems = append(*problems, fmt.Sprintf("service.pipelines.%s is malformed: %T, not a map", id, v))
	}
	at := "service.pipelines." + id
	return pipeline{
		receivers:  stringList(body["receivers"], at+".receivers", problems),
		processors: stringList(body["processors"], at+".processors", problems),
		exporters:  stringList(body["exporters"], at+".exporters", problems),
	}
}

// QueryPipelineProblems checks the log pipelines of a config. A log pipeline with a sqlserver receiver
// carries query events: it must hold no other receiver, run the allowlist processor and use exporters that no
// other log pipeline uses. One such pipeline must exist. A value of the wrong type is a problem.
func QueryPipelineProblems(config map[string]any) []string {
	var problems []string
	service, err := mapAt(config, "service")
	if err != "" {
		problems = append(problems, err)
	}
	raw, err := mapAt(service, "pipelines")
	if err != "" {
		problems = append(problems, "service."+err)
	}
	ids := make([]string, 0, len(raw))
	for id := range raw {
		if isLogsPipeline(id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)

	queries := map[string]pipeline{}
	var queryIDs []string
	otherExporters := map[string]string{}
	for _, id := range ids {
		p := parsePipeline(id, raw[id], &problems)
		if slices.ContainsFunc(p.receivers, isSQLServer) {
			queries[id] = p
			queryIDs = append(queryIDs, id)
			continue
		}
		for _, e := range p.exporters {
			otherExporters[e] = id
		}
	}
	if len(queryIDs) == 0 {
		problems = append(problems, "no log pipeline carries the sqlserver query events")
	}
	for _, id := range queryIDs {
		p := queries[id]
		for _, r := range p.receivers {
			if !isSQLServer(r) {
				problems = append(problems, fmt.Sprintf("service.pipelines.%s also reads %s", id, r))
			}
		}
		if !slices.Contains(p.processors, AllowlistProcessor) {
			problems = append(problems, fmt.Sprintf("service.pipelines.%s does not run %s", id, AllowlistProcessor))
		}
		if len(p.exporters) == 0 {
			problems = append(problems, fmt.Sprintf("service.pipelines.%s has no exporter", id))
		}
		for _, e := range p.exporters {
			if other, shared := otherExporters[e]; shared {
				problems = append(problems, fmt.Sprintf("service.pipelines.%s shares exporter %s with %s", id, e, other))
			}
		}
	}
	return problems
}

// mapAt reads an optional map under key. A value of another type gives a problem text.
func mapAt(m map[string]any, key string) (map[string]any, string) {
	v, present := m[key]
	if !present || v == nil {
		return nil, ""
	}
	out, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Sprintf("%s is malformed: %T, not a map", key, v)
	}
	return out, ""
}
