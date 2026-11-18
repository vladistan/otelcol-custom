// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package idracreceiver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
)

// Groups of storage paths; a failed read counts under its group.
const (
	groupStorage    = "storage"
	groupController = "controller"
	groupDrive      = "drive"
	groupVolume     = "volume"

	// maxCollectionMembers is the most members read from one storage, volume or controller collection.
	maxCollectionMembers = 512

	// storageRootLatch is how long a 404 on the Storage collection itself keeps it marked absent: a BMC without
	// storage is not asked every read, and one that answered 404 during a reset is asked again within the hour.
	// Every path below the collection is asked on each read.
	storageRootLatch = time.Hour
)

var (
	errNotAllowed = errors.New("path is outside the storage paths")
	errMistyped   = errors.New("a field has the wrong type")
	errTooMany    = errors.New("more members than the limit")
	errUnusable   = errors.New("a drive entry has no drive fields and no link")
	errReadTime   = errors.New("the storage read ran out of time")
)

// storageState is what the storage read keeps between reads. Only the storage timer touches it.
type storageState struct {
	errors      map[string]int64 // failed reads since the start, by group
	warned      map[string]bool  // groups warned about in the episode that is running
	panicLogged bool
}

// storageGroups lists the error groups in the order their counters are sent.
var storageGroups = []string{groupStorage, groupController, groupDrive, groupVolume}

// init gives every group a counter at 0, so the first failed read is an increase.
func (s *storageState) init() {
	if s.errors != nil {
		return
	}
	s.errors, s.warned = map[string]int64{}, map[string]bool{}
	for _, g := range storageGroups {
		s.errors[g] = 0
	}
}

// report sends the running count of every group, 0 included.
func (s *storageState) report(b *hwschema.Builder) {
	for _, g := range storageGroups {
		b.ScrapeErrors(g, s.errors[g])
	}
}

// Decoding. A Redfish body is read field by field, so a field of the wrong type is skipped and counted
// while the rest of the member is kept. A null or missing field is not a failure.

type fields struct {
	m   map[string]json.RawMessage
	bad *int
}

func newFields(raw []byte, bad *int) *fields {
	f := &fields{m: map[string]json.RawMessage{}, bad: bad}
	if err := json.Unmarshal(raw, &f.m); err != nil {
		*bad++
		f.m = map[string]json.RawMessage{}
	}
	if f.m == nil {
		f.m = map[string]json.RawMessage{}
	}
	return f
}

func (f *fields) get(key string) (json.RawMessage, bool) {
	v, ok := f.m[key]
	if !ok || string(bytes.TrimSpace(v)) == "null" {
		return nil, false
	}
	return v, true
}

func (f *fields) str(key string) string {
	v, ok := f.get(key)
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(v, &s) != nil {
		*f.bad++
		return ""
	}
	return s
}

func (f *fields) num(key string) *float64 {
	v, ok := f.get(key)
	if !ok {
		return nil
	}
	var n float64
	if json.Unmarshal(v, &n) != nil {
		*f.bad++
		return nil
	}
	return &n
}

func (f *fields) flag(key string) *bool {
	v, ok := f.get(key)
	if !ok {
		return nil
	}
	var b bool
	if json.Unmarshal(v, &b) != nil {
		*f.bad++
		return nil
	}
	return &b
}

func (f *fields) obj(key string) *fields {
	v, ok := f.get(key)
	if !ok {
		return nil
	}
	return newFields(v, f.bad)
}

func (f *fields) list(key string) []json.RawMessage {
	v, ok := f.get(key)
	if !ok {
		return nil
	}
	var l []json.RawMessage
	if json.Unmarshal(v, &l) != nil {
		*f.bad++
		return nil
	}
	return l
}

// hasAny reports whether one of the keys holds a value that is not null.
func (f *fields) hasAny(keys ...string) bool {
	for _, k := range keys {
		if _, ok := f.get(k); ok {
			return true
		}
	}
	return false
}

// link reads the @odata.id of a field that holds a link.
func (f *fields) link(key string) string {
	if o := f.obj(key); o != nil {
		return o.str("@odata.id")
	}
	return ""
}

// health reads Status: the Health, nil when it is null or missing, and the State.
func (f *fields) health() (*string, string) {
	o := f.obj("Status")
	if o == nil {
		return nil, ""
	}
	var h *string
	if s := o.str("Health"); s != "" {
		h = &s
	}
	return h, o.str("State")
}

// idOf is the Id of a member, or the last part of its @odata.id.
func (f *fields) idOf() string {
	if id := f.str("Id"); id != "" {
		return id
	}
	path := f.str("@odata.id")
	return path[strings.LastIndex(path, "/")+1:]
}

type controllerData struct {
	id, name, model, firmware string
	linkSpeed                 *float64
	health                    *string
	state                     string
}

// readController reads a controller. Its id is the Id of the resource, then single (the id of the storage when
// it lists one controller without an Id; the Controllers link gives that controller under the same Id), then the
// MemberId, then the fallback (the list index).
func readController(f *fields, single, fallback string) controllerData {
	c := controllerData{
		id: firstOf(f.str("Id"), single, f.str("MemberId"), fallback), name: f.str("Name"), model: f.str("Model"),
		firmware: f.str("FirmwareVersion"), linkSpeed: f.num("SpeedGbps"),
	}
	c.health, c.state = f.health()
	return c
}

func parseController(raw []byte) (controllerData, int) {
	var bad int
	return readController(newFields(raw, &bad), "", ""), bad
}

type driveData struct {
	id, name, model, firmware, serial, mediaType, protocol string
	capacity, lifeLeft                                     *float64
	health                                                 *string
	state                                                  string
	predicted                                              *bool
	indicator                                              *bool
}

func parseDrive(raw []byte) (driveData, int) {
	var bad int
	f := newFields(raw, &bad)
	d := driveData{
		id: f.idOf(), name: f.str("Name"), model: f.str("Model"), firmware: f.str("Revision"), serial: f.str("SerialNumber"),
		mediaType: f.str("MediaType"), protocol: f.str("Protocol"), capacity: f.num("CapacityBytes"),
		lifeLeft: f.num("PredictedMediaLifeLeftPercent"),
	}
	d.health, d.state = f.health()
	d.predicted = f.flag("FailurePredicted")
	d.indicator = f.flag("LocationIndicatorActive")
	if led := f.str("IndicatorLED"); d.indicator == nil && led != "" {
		on := led == "Lit" || led == "Blinking"
		if on || led == "Off" {
			d.indicator = &on
		}
	}
	return d, bad
}

type volumeData struct {
	id, name, raid, volumeType string
	capacity, spans            *float64
	health                     *string
	state                      string
}

func parseVolume(raw []byte) (volumeData, int) {
	var bad int
	f := newFields(raw, &bad)
	v := volumeData{id: f.idOf(), name: f.str("Name"), raid: f.str("RAIDType"), volumeType: f.str("VolumeType"), capacity: f.num("CapacityBytes")}
	v.health, v.state = f.health()
	if oem := f.obj("Oem"); oem != nil {
		if dell := oem.obj("Dell"); dell != nil {
			if dv := dell.obj("DellVolume"); dv != nil {
				v.spans = dv.num("MediaSpanCount")
			}
		}
	}
	return v, bad
}

// driveEntry is one item of the Drives list: a link to read, or a drive the list holds itself.
type driveEntry struct {
	link   string
	inline json.RawMessage
}

type storageData struct {
	id, name        string
	health          *string
	state           string
	controllers     []controllerData
	controllersLink string
	volumesLink     string
	drives          []driveEntry
	hasDrives       bool // the Drives list is there, even when it is empty
	dropped         int  // entries of the list that are neither a drive nor a link
}

// readBudget is the time a storage read may take once it has found the given number of drives: scrape_timeout
// plus a pace for each drive, up to the ceiling; a scrape_timeout above the ceiling is kept.
func (t *target) readBudget(drives int) time.Duration {
	budget := t.cfg.ScrapeTimeout + time.Duration(drives)*t.drivePace
	return max(t.cfg.ScrapeTimeout, min(budget, t.readCeiling))
}

// driveFields are the keys that tell a drive the list holds from a link to one.
var driveFields = []string{"Status", "CapacityBytes", "Model", "MediaType", "Protocol", "Id", "Name"}

func parseStorage(raw []byte) (storageData, int) {
	var bad int
	f := newFields(raw, &bad)
	s := storageData{id: f.idOf(), name: f.str("Name"), controllersLink: f.link("Controllers"), volumesLink: f.link("Volumes")}
	s.health, s.state = f.health()
	listed := f.list("StorageControllers")
	for i, c := range listed {
		single := ""
		if len(listed) == 1 {
			single = s.id
		}
		s.controllers = append(s.controllers, readController(newFields(c, &bad), single, strconv.Itoa(i)))
	}
	_, s.hasDrives = f.get("Drives")
	for _, d := range f.list("Drives") {
		e := newFields(d, &bad)
		switch id := e.str("@odata.id"); {
		case e.hasAny(driveFields...):
			s.drives = append(s.drives, driveEntry{inline: d})
		case id != "":
			s.drives = append(s.drives, driveEntry{link: id})
		default:
			s.dropped++
		}
	}
	return s, bad
}

// storageRun is one read of the storage resources of a target.
type storageRun struct {
	t        *target
	ctx      context.Context // ends when the read budget is spent, or at shutdown
	parent   context.Context // the context of the caller; it ends at shutdown
	began    time.Time
	timer    *time.Timer // cancels ctx at the end of the budget
	points   []pointAdder
	failed   map[string]bool // groups with a failed read in this run
	read     map[string]bool // groups with a read that ran in this run
	drives   int
	listed   int  // drives the storage resources list so far, which sets the budget
	isCapped bool // the drive limit was reached; no more drives are read
	// hasTimedOut is set once the budget ran out and the timeout was counted.
	hasTimedOut bool
	group       string // the group being decoded, for a panic
}

// isAllowed reports whether a path may be requested: the storage collection of the profile and what is below it.
func (t *target) isAllowed(path string) bool {
	base := t.profile.Storage()
	p := strings.SplitN(path, "?", 2)[0]
	if p != base && !strings.HasPrefix(p, base+"/") {
		return false
	}
	return !strings.Contains(p, "..") && !strings.ContainsAny(p, "\\\r\n")
}

// scrapeStorage reads controllers, volumes and drives once and sends their points with the failed-read
// count of each group. A read that runs out of time keeps what it read, so one slow drive hides no other.
func (t *target) scrapeStorage(parent context.Context) (md pmetric.Metrics) {
	start := t.env.now()
	b := hwschema.NewBuilder(scopeName, t.storageResource(), start)
	if t.isBlocked(start) {
		return b.Metrics()
	}
	t.storage.init()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	run := &storageRun{t: t, ctx: ctx, parent: parent, began: time.Now(), failed: map[string]bool{}, read: map[string]bool{}}
	run.timer = time.AfterFunc(t.cfg.ScrapeTimeout, cancel)
	defer run.timer.Stop()
	defer func() {
		if p := recover(); p != nil {
			md = t.storagePanic(b, run.group, p)
		}
	}()

	run.readAll()

	for _, p := range run.points {
		p(b)
	}
	t.storage.report(b)
	// A group is clear again only when it was read in this run and did not fail.
	for group := range t.storage.warned {
		if run.read[group] && !run.failed[group] {
			delete(t.storage.warned, group)
		}
	}
	return b.Metrics()
}

// grow raises the budget of the read for drives that were found.
func (r *storageRun) grow(drives int) {
	r.listed += drives
	left := r.t.readBudget(min(r.listed, r.t.cfg.StorageMaxDrives)) - time.Since(r.began)
	if left > 0 && r.timer.Stop() {
		r.timer.Reset(left)
	}
}

// isExpired reports whether the read has to stop. The first time the budget is found spent, it counts one
// timeout under group; a stop at shutdown is not an error.
func (r *storageRun) isExpired(group string) bool {
	if r.ctx.Err() == nil {
		return false
	}
	if r.parent.Err() == nil && !r.hasTimedOut {
		r.fail(group, "", errReadTime)
	}
	return true
}

func (r *storageRun) markRead(group string) {
	if r.read == nil {
		r.read = map[string]bool{}
	}
	r.read[group] = true
}

// storagePanic counts a decoder panic as a failed read and logs it once per target with the stack.
func (t *target) storagePanic(b *hwschema.Builder, group string, p any) pmetric.Metrics {
	if !t.storage.panicLogged {
		t.storage.panicLogged = true
		t.env.logger.Error("iDRAC storage decoder failed; the read is skipped and the next one runs",
			zap.String("target", t.cfg.Name), zap.String("part", group),
			zap.String("panic", t.client.redact(fmt.Sprint(p))), zap.ByteString("stack", debug.Stack()))
	}
	t.storage.errors[firstOf(group, groupStorage)]++
	t.storage.report(b)
	return b.Metrics()
}

// fail counts one failed read of a group and warns once per episode. A 404 on a path that was not marked
// before counts once; the client has logged it. A read that ran out of time counts once.
func (r *storageRun) fail(group, path string, err error) {
	t := r.t
	var absent *absentError
	if errors.As(err, &absent) {
		if absent.fresh {
			t.storage.errors[group]++
		}
		return
	}
	if r.parent.Err() != nil {
		return
	}
	if r.ctx.Err() != nil {
		if r.hasTimedOut {
			return
		}
		r.hasTimedOut = true
	}
	t.storage.errors[group]++
	r.failed[group] = true
	if t.env.skipped != nil {
		t.env.skipped(t.cfg.Name, group)
	}
	r.warn(group, group, path, err)
}

func (r *storageRun) warn(key, group, path string, err error) {
	t := r.t
	if t.storage.warned[key] {
		return
	}
	t.storage.warned[key] = true
	fields := []zap.Field{zap.String("target", t.cfg.Name), zap.String("part", group)}
	if path != "" {
		fields = append(fields, zap.String("path", path))
	}
	msg := "iDRAC storage read failed; that part is skipped"
	if errors.Is(err, errTooMany) {
		msg = "iDRAC storage list is longer than the limit; the rest is skipped"
	}
	t.env.logger.Warn(msg, append(fields, zap.String("error", t.client.redact(err.Error())))...)
}

// mistyped counts a member that had fields of the wrong type: one failed read for the member.
func (r *storageRun) mistyped(group string, bad int) {
	if bad > 0 {
		r.fail(group, "", errMistyped)
	}
}

// decode runs the test hook, if any, before a member is decoded.
func (r *storageRun) decode(group string) {
	r.group = group
	if r.t.onDecode != nil {
		r.t.onDecode(group)
	}
}

func (r *storageRun) soft(group string) softFunc {
	return func(_, path string, err error) bool {
		if r.ctx.Err() != nil {
			return false
		}
		r.fail(group, path, err)
		return true
	}
}

func (r *storageRun) collection(group, path string) []json.RawMessage {
	if r.isExpired(group) {
		return nil
	}
	if !r.t.isAllowed(path) {
		r.fail(group, "", errNotAllowed)
		return nil
	}
	latch := neverLatch
	if path == r.t.profile.Storage() {
		latch = storageRootLatch
	}
	members, listed, err := r.t.members(r.ctx, path, collOpts{soft: r.soft(group), allow: r.t.isAllowed, limit: maxCollectionMembers, latch: latch})
	if err != nil {
		r.fail(group, path, err)
		return nil
	}
	r.markRead(group)
	if listed > maxCollectionMembers {
		r.fail(group, "", errTooMany)
	}
	return members
}

func (r *storageRun) readAll() {
	for _, raw := range r.collection(groupStorage, r.t.profile.Storage()) {
		if r.isExpired(groupStorage) {
			return
		}
		r.readStorage(raw)
	}
}

func (r *storageRun) readStorage(raw json.RawMessage) {
	r.decode(groupStorage)
	s, bad := parseStorage(raw)
	r.mistyped(groupStorage, bad)
	if s.id == "" {
		return
	}
	r.grow(len(s.drives))
	for range s.dropped {
		r.fail(groupDrive, "", errUnusable)
	}
	if !absentState(s.state) {
		r.addRollup(s)
	}
	controllers := s.controllers
	if len(controllers) == 0 && s.controllersLink != "" {
		for i, c := range r.collection(groupController, s.controllersLink) {
			r.decode(groupController)
			cd, bad := parseController(c)
			r.mistyped(groupController, bad)
			cd.id = firstOf(cd.id, strconv.Itoa(i))
			controllers = append(controllers, cd)
		}
	}
	if len(controllers) > 0 {
		r.markRead(groupController)
	}
	for _, c := range controllers {
		r.addController(s.id, c)
	}
	r.readDrives(s)
	if s.volumesLink != "" {
		for _, v := range r.collection(groupVolume, s.volumesLink) {
			r.decode(groupVolume)
			vd, bad := parseVolume(v)
			r.mistyped(groupVolume, bad)
			r.addVolume(s.id, vd)
		}
	}
}

func (r *storageRun) readDrives(s storageData) {
	for _, e := range s.drives {
		if r.isExpired(groupDrive) || r.isCapped {
			return
		}
		if r.drives >= r.t.cfg.StorageMaxDrives {
			r.isCapped = true
			r.fail(groupDrive, "", errTooMany)
			return
		}
		r.drives++
		raw := e.inline
		if raw == nil {
			if !r.t.isAllowed(e.link) {
				r.fail(groupDrive, "", errNotAllowed)
				continue
			}
			var body json.RawMessage
			if err := r.t.client.getFor(r.ctx, e.link, &body, neverLatch); err != nil {
				r.fail(groupDrive, e.link, err)
				continue
			}
			raw = body
		}
		r.markRead(groupDrive)
		r.decode(groupDrive)
		d, bad := parseDrive(raw)
		r.mistyped(groupDrive, bad)
		if d.id == "" && e.link != "" {
			d.id = e.link[strings.LastIndex(e.link, "/")+1:]
		}
		if d.id == "" {
			r.fail(groupDrive, "", errUnusable)
			continue
		}
		r.addDrive(s.id, d)
	}
}

func absentState(state string) bool { return state == "Absent" }

func stateFor(health *string) (string, bool) {
	if health == nil {
		return "", false
	}
	return hwschema.StateFromHealth(*health)
}

func (r *storageRun) addRollup(s storageData) {
	id, name := "storage/"+s.id, firstOf(s.name, s.id)
	if s.hasDrives {
		count := float64(len(s.drives))
		r.points = append(r.points, func(b *hwschema.Builder) { b.StorageDriveCount(id, name, count) })
	}
	state, ok := stateFor(s.health)
	if !ok {
		return
	}
	r.points = append(r.points, func(b *hwschema.Builder) { b.Status(hwschema.TypeStorage, id, name, state) })
}

func (r *storageRun) addController(storageID string, c controllerData) {
	if absentState(c.state) {
		return
	}
	id, name := "disk_controller/"+storageID+"/"+c.id, firstOf(c.name, c.id)
	if state, ok := stateFor(c.health); ok {
		extra := map[string]string{hwschema.AttrParent: storageID, hwschema.AttrModel: c.model, hwschema.AttrFirmwareVersion: c.firmware}
		r.points = append(r.points, func(b *hwschema.Builder) { b.StatusWith(hwschema.TypeDiskController, id, name, state, extra) })
	}
	if c.linkSpeed != nil {
		perSec := *c.linkSpeed * 1000
		r.points = append(r.points, func(b *hwschema.Builder) { b.DiskControllerSpeed(id, name, storageID, perSec) })
	}
}

func (r *storageRun) addVolume(storageID string, v volumeData) {
	if v.id == "" || absentState(v.state) {
		return
	}
	id, name := "logical_disk/"+storageID+"/"+v.id, firstOf(v.name, v.id)
	attrs := map[string]string{hwschema.AttrRAIDLevel: v.raid, hwschema.AttrVolumeType: v.volumeType}
	if state, ok := stateFor(v.health); ok {
		extra := map[string]string{hwschema.AttrParent: storageID}
		for k, val := range attrs {
			extra[k] = val
		}
		r.points = append(r.points, func(b *hwschema.Builder) { b.StatusWith(hwschema.TypeLogicalDisk, id, name, state, extra) })
	}
	if v.capacity != nil {
		size := *v.capacity
		r.points = append(r.points, func(b *hwschema.Builder) { b.LogicalDiskLimit(id, name, storageID, size, attrs) })
	}
	if v.spans != nil {
		spans := *v.spans
		r.points = append(r.points, func(b *hwschema.Builder) { b.LogicalDiskMediaSpanCount(id, name, storageID, spans) })
	}
}

func (r *storageRun) addDrive(storageID string, d driveData) {
	if d.id == "" || absentState(d.state) {
		return
	}
	id, name := "physical_disk/"+storageID+"/"+d.id, firstOf(d.name, d.id)
	res := map[string]string{
		hwschema.AttrSerialNumber: d.serial, hwschema.AttrModel: d.model, hwschema.AttrFirmwareVersion: d.firmware,
		hwschema.AttrPhysicalDiskType: d.mediaType, hwschema.AttrPhysicalDiskProtocol: d.protocol,
	}
	state, hasState := stateFor(d.health)
	if d.capacity == nil && d.lifeLeft == nil && d.indicator == nil && d.predicted == nil && !hasState {
		return
	}
	r.points = append(r.points, func(b *hwschema.Builder) {
		c := b.Component(res)
		if d.capacity != nil {
			c.PhysicalDiskSize(id, name, storageID, *d.capacity)
		}
		if d.lifeLeft != nil {
			c.PhysicalDiskEndurance(id, name, storageID, 1-*d.lifeLeft/100)
		}
		if d.indicator != nil {
			c.PhysicalDiskIndicator(id, name, storageID, *d.indicator)
		}
		if d.predicted != nil {
			c.PhysicalDiskPredictedFailure(id, name, storageID, *d.predicted)
		}
		if hasState {
			c.StatusWith(hwschema.TypePhysicalDisk, id, name, state, map[string]string{hwschema.AttrParent: storageID})
		}
	})
}
