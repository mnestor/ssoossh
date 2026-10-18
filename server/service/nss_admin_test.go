package service

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// nssAdminActor is the admin the admin actions are recorded against.
func nssAdminActor() *AuditSubject {
	return &AuditSubject{UserID: "u-admin", Subject: "sub-admin", Username: "admin"}
}

// tombstone marks the stored account or group named name deleted at
// deletedAt, as a full sync would after nss.tombstone_after.
func tombstone(t *testing.T, h *nssHarness, m any, name string, deletedAt time.Time) {
	t.Helper()
	if err := h.db.Model(m).Where("name = ?", name).
		Updates(map[string]any{"missing_since": deletedAt, "deleted_at": deletedAt}).Error; err != nil {
		t.Fatalf("tombstone %s: %v", name, err)
	}
}

// lastAudit decodes the newest audit payload carrying action.
func lastAudit(t *testing.T, h *nssHarness, action AuditAction) AuditEvent {
	t.Helper()
	var row model.AuditEvent
	if err := h.db.Where("action = ?", string(action)).Order("created_at DESC").First(&row).Error; err != nil {
		t.Fatalf("read %s audit row: %v", action, err)
	}
	var event AuditEvent
	if err := json.Unmarshal([]byte(row.Payload), &event); err != nil {
		t.Fatalf("decode %s audit row: %v", action, err)
	}
	return event
}

// assertHTTPStatus fails unless err renders as status.
func assertHTTPStatus(t *testing.T, err error, status int) {
	t.Helper()
	var coded interface{ HTTPStatusCode() int }
	if !errors.As(err, &coded) || coded.HTTPStatusCode() != status {
		t.Errorf("error = %v, want one rendered as %d", err, status)
	}
}

func TestNSSPurgeTombstones_ShouldPurgeOnlyTombstonesPastRetentionWhenTheSweepRuns(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "old", 1, 1, nil))
	h.dir.put(nssAccountEntry("", "recent", 2, 2, nil))
	h.dir.put(nssAccountEntry("", "live", 3, 3, nil))
	h.dir.put(nssGroupEntry("oldgroup", 700, "old", "live"))
	h.fullSync(t)
	now := nssTestStart()
	tombstone(t, h, &model.PosixUser{}, "old", now.Add(-2161*time.Hour))
	tombstone(t, h, &model.PosixUser{}, "recent", now.Add(-2159*time.Hour))
	tombstone(t, h, &model.PosixGroup{}, "oldgroup", now.Add(-2161*time.Hour))

	purged, err := h.svc.PurgeTombstones(t.Context())
	if err != nil || purged != 2 {
		t.Fatalf("PurgeTombstones() = %d, %v; want 2 purged", purged, err)
	}

	for _, tt := range []struct {
		name string
		m    any
		want int64
	}{{"old", &model.PosixUser{}, 0}, {"recent", &model.PosixUser{}, 1}, {"live", &model.PosixUser{}, 1}, {"oldgroup", &model.PosixGroup{}, 0}} {
		if got := h.count(t, tt.m, "name = ?", tt.name); got != tt.want {
			t.Errorf("%s rows = %d, want %d", tt.name, got, tt.want)
		}
	}
	if n := h.count(t, &model.PosixGroupMember{}, ""); n != 0 {
		t.Errorf("membership rows = %d, want the purged group's and account's gone", n)
	}
	if got := h.audits(t, AuditNSSTombstonePurged); got != 2 {
		t.Errorf("nss.tombstone_purged events = %d, want one per purged row", got)
	}
	event := lastAudit(t, h, AuditNSSTombstonePurged)
	if !event.System || event.Actor != nil || event.Detail["name"] == nil || event.Detail["dn"] == nil {
		t.Errorf("event = %+v, want a system event naming the row", event)
	}
}

func TestNSSPurgeTombstone_ShouldPurgeEarlyOnlyWhenTheRowIsATombstone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		tombstoned bool
		purge      func(*nssHarness) error
		wantStatus int
	}{
		{name: "should refuse an account that is live", purge: func(h *nssHarness) error { return h.svc.PurgeUserTombstone(t.Context(), 1, nssAdminActor()) }, wantStatus: 404},
		{name: "should refuse an account that does not exist", purge: func(h *nssHarness) error { return h.svc.PurgeUserTombstone(t.Context(), 99, nssAdminActor()) }, wantStatus: 404},
		{name: "should purge an account when it is a tombstone", tombstoned: true, purge: func(h *nssHarness) error { return h.svc.PurgeUserTombstone(t.Context(), 1, nssAdminActor()) }},
		{name: "should refuse a group that is live", purge: func(h *nssHarness) error { return h.svc.PurgeGroupTombstone(t.Context(), 700, nssAdminActor()) }, wantStatus: 404},
		{name: "should purge a group when it is a tombstone", tombstoned: true, purge: func(h *nssHarness) error { return h.svc.PurgeGroupTombstone(t.Context(), 700, nssAdminActor()) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newNSSHarness(t, nil)
			h.dir.put(nssAccountEntry("", "ada", 1, 1, nil))
			h.dir.put(nssGroupEntry("ops", 700, "ada"))
			h.fullSync(t)
			if tt.tombstoned {
				tombstone(t, h, &model.PosixUser{}, "ada", nssTestStart())
				tombstone(t, h, &model.PosixGroup{}, "ops", nssTestStart())
			}

			err := tt.purge(h)
			if tt.wantStatus != 0 {
				assertHTTPStatus(t, err, tt.wantStatus)
				return
			}
			if err != nil {
				t.Fatalf("purge error = %v", err)
			}
			event := lastAudit(t, h, AuditNSSTombstonePurged)
			if event.System || event.Actor == nil || event.Actor.UserID != "u-admin" {
				t.Errorf("event = %+v, want the admin recorded as the actor", event)
			}
		})
	}
}

func TestNSSPurgeTombstone_ShouldServeTheOtherEntryWhenItsHolderIsPurged(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "ada", 3000, 3000, nil))
	h.fullSync(t)
	tombstone(t, h, &model.PosixUser{}, "ada", nssTestStart())
	h.dir.remove("uid=ada,ou=people,dc=test")
	h.dir.put(nssAccountEntry("", "eve", 3000, 3000, nil))
	h.fullSync(t)
	if n := h.count(t, &model.NSSCollision{}, ""); n != 1 {
		t.Fatalf("collisions = %d, want the tombstone holding eve's uid", n)
	}

	if err := h.svc.PurgeUserTombstone(t.Context(), 3000, nssAdminActor()); err != nil {
		t.Fatalf("PurgeUserTombstone() error = %v", err)
	}
	if n := h.count(t, &model.NSSCollision{}, ""); n != 0 {
		t.Errorf("collisions = %d, want the purged holder's gone", n)
	}
	h.fullSync(t)
	if eve := h.accountByName(t, "eve"); eve.UID != 3000 || eve.WithheldAt != nil {
		t.Errorf("eve = %+v, want it stored and served once the uid is free", eve)
	}
}

func TestNSSAcceptUserIDChange_ShouldApplyThePendingNumbersWhenNothingHoldsThem(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "ada", 1001, 100, nil))
	h.dir.put(nssGroupEntry("ops", 700, "ada"))
	h.fullSync(t)
	h.dir.put(nssAccountEntry("", "ada", 2001, 200, nil))
	h.fullSync(t)

	accepted, err := h.svc.AcceptUserIDChange(t.Context(), 1001, nssAdminActor())
	if err != nil {
		t.Fatalf("AcceptUserIDChange() error = %v", err)
	}
	if accepted.UID != 2001 || accepted.GID != 200 || accepted.PendingUID != nil || accepted.PendingGID != nil || accepted.PendingSince != nil {
		t.Errorf("accepted = %+v, want the pending numbers applied and cleared", accepted)
	}
	if got := h.members(t, 700); !reflect.DeepEqual(got, []uint32{2001}) {
		t.Errorf("members = %v, want the membership re-keyed to the new uid", got)
	}
	event := lastAudit(t, h, AuditNSSIDChangeAccepted)
	if event.Actor == nil || event.Actor.UserID != "u-admin" {
		t.Errorf("event = %+v, want the admin as actor", event)
	}

	// The directory and the row agree now, so the next sync holds nothing.
	h.fullSync(t)
	if ada := h.accountByName(t, "ada"); ada.PendingUID != nil {
		t.Errorf("ada pending_uid = %v after the next sync, want nil", ada.PendingUID)
	}
}

func TestNSSAcceptUserIDChange_ShouldRefuseTheChangeWhenItCannotApply(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setup      func(*testing.T, *nssHarness)
		uid        uint32
		wantStatus int
	}{
		{name: "should answer not found when no account has the uid", uid: 9999, wantStatus: 404},
		{name: "should answer not found when nothing is pending", uid: 1001, wantStatus: 404},
		{
			name: "should answer conflict when a tombstone holds the new uid",
			setup: func(t *testing.T, h *nssHarness) {
				h.dir.put(nssAccountEntry("", "gone", 2001, 2001, nil))
				h.fullSync(t)
				tombstone(t, h, &model.PosixUser{}, "gone", nssTestStart())
				h.dir.remove("uid=gone,ou=people,dc=test")
				h.dir.put(nssAccountEntry("", "ada", 2001, 1001, nil))
				h.fullSync(t)
			},
			uid: 1001, wantStatus: 409,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newNSSHarness(t, nil)
			h.dir.put(nssAccountEntry("", "ada", 1001, 1001, nil))
			h.fullSync(t)
			if tt.setup != nil {
				tt.setup(t, h)
			}

			_, err := h.svc.AcceptUserIDChange(t.Context(), tt.uid, nssAdminActor())
			assertHTTPStatus(t, err, tt.wantStatus)
			if got := h.accountByName(t, "ada").UID; got != 1001 {
				t.Errorf("ada uid = %d, want it unchanged after a refused accept", got)
			}
		})
	}
}

func TestNSSAcceptGroupIDChange_ShouldReKeyMembersAndPrimaryGidsWhenAccepted(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "ada", 1001, 5000, nil))
	h.dir.put(nssAccountEntry("", "bob", 1002, 5000, nil))
	h.dir.put(nssGroupEntry("staff", 5000, "ada", "bob"))
	h.fullSync(t)
	// The directory moves the group and its members' gid attribute.
	h.dir.put(nssAccountEntry("", "ada", 1001, 6000, nil))
	h.dir.put(nssAccountEntry("", "bob", 1002, 6000, nil))
	h.dir.put(nssGroupEntry("staff", 6000, "ada", "bob"))
	h.fullSync(t)

	accepted, err := h.svc.AcceptGroupIDChange(t.Context(), 5000, nssAdminActor())
	if err != nil {
		t.Fatalf("AcceptGroupIDChange() error = %v", err)
	}
	if accepted.GID != 6000 || accepted.PendingGID != nil {
		t.Errorf("accepted = %+v, want gid 6000 with nothing pending", accepted)
	}
	if got := h.members(t, 6000); !reflect.DeepEqual(got, []uint32{1001, 1002}) {
		t.Errorf("members of 6000 = %v, want both re-keyed", got)
	}
	for _, name := range []string{"ada", "bob"} {
		if row := h.accountByName(t, name); row.GID != 6000 || row.PendingGID != nil || row.PendingSince != nil {
			t.Errorf("%s = gid %d pending %v since %v, want 6000 with nothing pending", name, row.GID, row.PendingGID, row.PendingSince)
		}
	}
}

func TestNSSAcceptGroupIDChange_ShouldRefuseTheChangeWhenItCannotApply(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setup      func(*testing.T, *nssHarness)
		gid        uint32
		wantStatus int
	}{
		{name: "should answer not found when nothing is pending", gid: 5000, wantStatus: 404},
		{
			name: "should answer conflict when another group holds the new gid",
			setup: func(t *testing.T, h *nssHarness) {
				h.dir.put(nssGroupEntry("other", 6000))
				h.dir.put(nssGroupEntry("staff", 6000))
				h.fullSync(t)
			},
			gid: 5000, wantStatus: 409,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newNSSHarness(t, nil)
			h.dir.put(nssGroupEntry("staff", 5000))
			h.fullSync(t)
			if tt.setup != nil {
				tt.setup(t, h)
			}
			_, err := h.svc.AcceptGroupIDChange(t.Context(), tt.gid, nssAdminActor())
			assertHTTPStatus(t, err, tt.wantStatus)
		})
	}
}

func TestNSSPendingIDChanges_ShouldListAccountsAndGroupsWhenNumbersArePending(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "ada", 1001, 1001, nil))
	h.dir.put(nssAccountEntry("", "bob", 1002, 1002, nil))
	h.dir.put(nssGroupEntry("ops", 700))
	h.fullSync(t)
	h.dir.put(nssAccountEntry("", "ada", 2001, 1001, nil))
	h.dir.put(nssGroupEntry("ops", 701))
	h.fullSync(t)

	accounts, groups, err := h.svc.PendingIDChanges(t.Context())
	if err != nil {
		t.Fatalf("PendingIDChanges() error = %v", err)
	}
	if len(accounts) != 1 || accounts[0].Name != "ada" || len(groups) != 1 || groups[0].Name != "ops" {
		t.Errorf("pending = %d accounts, %d groups; want ada and ops", len(accounts), len(groups))
	}
}

func TestNSSCollisions_ShouldDescribeBothSidesWhenACollisionIsListed(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "ada", 3000, 3000, nil))
	h.fullSync(t)
	tombstone(t, h, &model.PosixUser{}, "ada", nssTestStart())
	h.dir.remove("uid=ada,ou=people,dc=test")
	h.dir.put(nssAccountEntry("", "eve", 3000, 3000, nil))
	h.fullSync(t)

	reports, err := h.svc.Collisions(t.Context())
	if err != nil || len(reports) != 1 {
		t.Fatalf("Collisions() = %d reports, %v; want 1", len(reports), err)
	}
	r := reports[0]
	if r.Kind != model.NSSCollisionUID || r.Value != "3000" {
		t.Errorf("report = %+v, want the uid", r)
	}
	if !r.Holder.Stored || r.Holder.DeletedAt == nil || r.Holder.Name != "ada" {
		t.Errorf("holder = %+v, want the stored tombstone ada", r.Holder)
	}
	if r.Other.Stored || r.Other.Name != "eve" || r.Other.Number != 3000 || r.Other.DN != "uid=eve,ou=people,dc=test" {
		t.Errorf("other = %+v, want the refused entry eve", r.Other)
	}
}

func TestNSSAdmin_ShouldAnswerNotFoundWhenNSSIsDisabled(t *testing.T) {
	t.Parallel()

	var svc *NSSDirectoryService
	ctx := t.Context()
	_, err1 := svc.AcceptUserIDChange(ctx, 1, nil)
	_, err2 := svc.AcceptGroupIDChange(ctx, 1, nil)
	_, err3 := svc.Collisions(ctx)
	for i, err := range []error{err1, err2, err3, svc.PurgeUserTombstone(ctx, 1, nil), svc.PurgeGroupTombstone(ctx, 1, nil)} {
		var nf *errorresponses.NotFoundError
		if !errors.As(err, &nf) {
			t.Errorf("call %d error = %v, want a NotFoundError", i, err)
		}
	}
}
