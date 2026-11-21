// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

// work holds the scratch objects of one ConsumeLogs call. A branch parses on a scratch record, so a
// failed parse leaves the real one as it was. The scratch resource and scope are copied from the input
// again only when the previous record left them changed, and a changed resource or scope is kept once
// for all records that end up with the same one.
type work struct {
	rec   rec
	lr    plog.LogRecord
	res   pcommon.Resource
	scope pcommon.InstrumentationScope

	// resDirty and scopeDirty say that the scratch object may differ from the input one.
	resDirty, scopeDirty bool

	resCanon   map[uint64][]pcommon.Resource
	scopeCanon map[uint64][]pcommon.InstrumentationScope
}

func newWork() *work {
	return &work{lr: plog.NewLogRecord(), res: pcommon.NewResource(), scope: pcommon.NewInstrumentationScope()}
}

// prepare makes the scratch record, resource and scope equal to the input ones.
func (w *work) prepare(lr plog.LogRecord, res pcommon.Resource, scope pcommon.InstrumentationScope, body string, memo any) *rec {
	lr.CopyTo(w.lr)
	if w.resDirty {
		res.CopyTo(w.res)
		w.resDirty = false
	}
	if w.scopeDirty {
		scope.CopyTo(w.scope)
		w.scopeDirty = false
	}
	w.rec = rec{lr: w.lr, res: w.res, scope: w.scope, body: body, memo: memo}
	return &w.rec
}

// changedResource returns the resource of every record that ends with these attributes: one object,
// found by the hash and confirmed by comparing the attributes.
func (w *work) changedResource() pcommon.Resource {
	if w.resCanon == nil {
		w.resCanon = map[uint64][]pcommon.Resource{}
	}
	hash := hashResource(w.res.Attributes())
	for _, c := range w.resCanon[hash] {
		if mapsEqual(c.Attributes(), w.res.Attributes()) {
			return c
		}
	}
	c := pcommon.NewResource()
	w.res.CopyTo(c)
	w.resCanon[hash] = append(w.resCanon[hash], c)
	return c
}

// changedScope is changedResource for the scope.
func (w *work) changedScope() pcommon.InstrumentationScope {
	if w.scopeCanon == nil {
		w.scopeCanon = map[uint64][]pcommon.InstrumentationScope{}
	}
	hash := hashScopeOf(w.scope)
	for _, c := range w.scopeCanon[hash] {
		if scopesEqual(c, w.scope) {
			return c
		}
	}
	c := pcommon.NewInstrumentationScope()
	w.scope.CopyTo(c)
	w.scopeCanon[hash] = append(w.scopeCanon[hash], c)
	return c
}
