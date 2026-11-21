// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore slift

package logshapeprocessor

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/vladistan/otelcol-custom/processor/logshapeprocessor/internal/klog"
	"github.com/vladistan/otelcol-custom/shape/body"
	"github.com/vladistan/otelcol-custom/shape/k8slift"
	"github.com/vladistan/otelcol-custom/shape/severity"
)

// klogLifted are the structured keys that become Kubernetes attributes instead of staying in the Body.
var klogLifted = map[string]bool{"pod": true, "podUID": true, "podID": true, "containerName": true, "containerID": true}

type klogBranch struct {
	cfg    KlogConfig
	levels map[string]severity.Level
}

func newKlogBranch(cfg KlogConfig) *klogBranch {
	defaults := map[string]severity.Level{}
	for _, letter := range []string{"I", "W", "E", "F"} {
		l, _ := severity.FromSource(severity.Klog, letter)
		defaults[letter] = l
	}
	overrides := map[string]severity.Level{}
	for letter, word := range cfg.LevelOverrides {
		if l, ok := severity.FromSource(severity.Word, word); ok {
			overrides[letter] = l
		}
	}
	return &klogBranch{cfg: cfg, levels: severity.MergeMap(defaults, overrides)}
}

func (*klogBranch) name() string { return "klog" }

// claims parses the line and keeps the result for parse.
func (*klogBranch) claims(r *rec) bool {
	if !klog.QuickCheck(r.body) {
		return false
	}
	info := klog.ParseKlog(r.body)
	if info != nil {
		r.memo = info
	}
	return info != nil
}

func (b *klogBranch) parse(r *rec) error {
	info := memoOf[klog.KlogInfo](r)
	if info == nil {
		info = klog.ParseKlog(r.body)
	}
	if info == nil {
		return errors.New("not a klog line")
	}
	ts, err := b.timestamp(r.lr, info.MonthDay, info.Clock)
	if err != nil {
		return err
	}
	level := b.levels[info.Level]

	a := r.lr.Attributes()
	a.PutStr(originalKey, r.body)
	putInt(a, "process.pid", info.PID)
	a.PutStr("code.file.path", info.File)
	putInt(a, "code.line.number", info.Line)
	liftKubelet(a, info.Pairs)
	b.hostFromStaticPod(r.res)
	k8slift.Lift(r.res.Attributes(), a)

	r.lr.SetTimestamp(pcommon.NewTimestampFromTime(ts))
	r.lr.SetSeverityNumber(plog.SeverityNumber(level.Number))
	r.lr.SetSeverityText(level.Text)
	r.lr.Body().SetStr(body.Compose("", info.Message, keptPairs(info.Pairs)...))
	return nil
}

func (b *klogBranch) timestamp(lr plog.LogRecord, monthDay, clock string) (time.Time, error) {
	ref := lr.Timestamp().AsTime()
	if lr.Timestamp() == 0 {
		ref = lr.ObservedTimestamp().AsTime()
		if lr.ObservedTimestamp() == 0 {
			ref = time.Now().UTC()
		}
	}
	year := ref.Year()
	if b.cfg.Year != 0 {
		year = b.cfg.Year
	}
	t, err := time.Parse("2006-01-02 15:04:05.999999999", fmt.Sprintf("%04d-%s-%s %s", year, monthDay[:2], monthDay[2:], clock))
	if err != nil {
		return time.Time{}, fmt.Errorf("klog timestamp: %w", err)
	}
	if b.cfg.Year == 0 && t.After(ref.Add(24*time.Hour)) {
		t = t.AddDate(-1, 0, 0)
	}
	return t, nil
}

func (b *klogBranch) hostFromStaticPod(res pcommon.Resource) {
	a := res.Attributes()
	pod, ok := a.Get("k8s.pod.name")
	if !ok {
		return
	}
	node, ok := k8slift.NodeFromStaticPod(pod.Str(), b.cfg.StaticPodPrefixes)
	if !ok {
		return
	}
	if _, has := a.Get("k8s.node.name"); !has {
		a.PutStr("k8s.node.name", node)
	}
	if _, has := a.Get("host.name"); !has {
		a.PutStr("host.name", node)
	}
}

func keptPairs(kvs []klog.KV) []body.Pair {
	var out []body.Pair
	for _, kv := range kvs {
		if !klogLifted[kv.Key] {
			out = append(out, body.Pair{Key: kv.Key, Value: kv.Value})
		}
	}
	return out
}

func liftKubelet(a pcommon.Map, kvs []klog.KV) {
	for _, kv := range kvs {
		if kv.Value == "" {
			continue
		}
		switch kv.Key {
		case "pod":
			if ns, name, ok := strings.Cut(kv.Value, "/"); ok && ns != "" && name != "" {
				a.PutStr("k8s.namespace.name", ns)
				a.PutStr("k8s.pod.name", name)
			}
		case "podUID":
			a.PutStr("k8s.pod.uid", kv.Value)
		case "podID":
			if _, has := a.Get("k8s.pod.uid"); !has {
				a.PutStr("k8s.pod.uid", kv.Value)
			}
		case "containerName":
			a.PutStr("k8s.container.name", kv.Value)
		case "containerID":
			a.PutStr("container.id", kv.Value)
		}
	}
}
