// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore charon strongswan ipsec

package logshapeprocessor

import (
	"errors"
	"strings"

	"github.com/vladistan/otelcol-custom/processor/logshapeprocessor/internal/charon"
)

// charonService is the journal unit of the strongSwan starter, with or without the ".service" suffix.
const charonService = "strongswan-starter"

// charonBranch shapes the lines of the strongSwan charon daemon, which the journal records under the
// strongswan-starter service. It replaces the charonlog processor; a pipeline runs one of the two.
type charonBranch struct{}

func newCharonBranch() *charonBranch { return &charonBranch{} }

func (*charonBranch) name() string { return "charon" }

func (*charonBranch) claims(r *rec) bool {
	v, ok := r.res.Attributes().Get(serviceKey)
	if !ok || strings.TrimSuffix(v.Str(), ".service") != charonService {
		return false
	}
	info, ok := charon.Parse(r.body)
	if ok {
		r.memo = info
	}
	return ok
}

func (*charonBranch) parse(r *rec) error {
	info := memoOf[charon.Info](r)
	if info == nil {
		var ok bool
		if info, ok = charon.Parse(r.body); !ok {
			return errors.New("not a charon line")
		}
	}
	a := r.lr.Attributes()
	a.PutStr(originalKey, r.body)
	if n, ok := charon.ThreadID(info.Thread); ok {
		a.PutInt("thread.id", n)
	}
	a.PutStr("ipsec.component", info.Component)
	putNonEmpty(a, "ipsec.connection_id", info.ConnID)
	putNonEmpty(a, "ipsec.connection_seq", info.ConnSeq)
	for k, v := range info.Extra {
		a.PutStr(k, v)
	}
	if word := charon.Level(info.Message); word != "" {
		applyLevel(r.lr, wordLevel(word))
	}
	r.lr.Body().SetStr("[" + info.Component + "] " + info.Message)
	return nil
}
