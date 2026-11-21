// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan

package logshapeprocessor

import (
	"errors"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/vladistan/otelcol-custom/processor/logshapeprocessor/internal/redis"
	"github.com/vladistan/otelcol-custom/shape/severity"
)

type redisBranch struct{ cfg RedisConfig }

func newRedisBranch(cfg RedisConfig) *redisBranch { return &redisBranch{cfg: cfg} }

func (*redisBranch) name() string { return "redis" }

// text is the source attribute when set and present, else the body.
func (b *redisBranch) text(r *rec) string {
	if b.cfg.SourceAttribute != "" {
		if v, ok := r.lr.Attributes().Get(b.cfg.SourceAttribute); ok && v.Str() != "" {
			return v.Str()
		}
	}
	return r.body
}

func (b *redisBranch) claims(r *rec) bool { return redis.IsRedisLog(b.text(r)) }

func (b *redisBranch) parse(r *rec) error {
	text := b.text(r)
	info := redis.ParseRedisLog(text)
	if info == nil {
		return errors.New("not a Redis log line")
	}
	a := r.lr.Attributes()
	if b.cfg.PreserveOriginal {
		a.PutStr(originalKey, text)
	}
	if full, ok := a.Get("full_message"); ok {
		a.PutStr(originalKey, full.Str())
		a.Remove("full_message")
	}
	if b.cfg.SetServiceName {
		if _, ok := r.res.Attributes().Get(serviceKey); !ok {
			r.res.Attributes().PutStr(serviceKey, b.cfg.ServiceName)
		}
	}
	putInt(a, "process.pid", info.PID)
	a.PutStr("vstan.redis.role", info.RoleName)
	if b.cfg.AdjustSeverity {
		if level, ok := severity.FromSource(severity.Redis, info.LevelChar); ok {
			r.lr.SetSeverityNumber(plog.SeverityNumber(level.Number))
			r.lr.SetSeverityText(level.Text)
		}
	}
	if t, ok := redis.LineTime(info.Timestamp); ok {
		r.lr.SetTimestamp(pcommon.NewTimestampFromTime(t))
	}
	if b.cfg.RewriteMessages {
		r.lr.Body().SetStr(info.Message)
	}
	return nil
}
