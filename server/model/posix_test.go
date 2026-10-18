package model

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/test/testdb"
)

// The NSS models are checked against the real migrations rather than
// AutoMigrate: a column tag that names a column the migration does not
// create, or a Go type the column cannot hold, fails here on whichever
// backend the run selects (make test-postgres runs the Postgres leg).

// nssTestTime is a fixed instant with no sub-second part, so a value read
// back compares equal on both backends.
func nssTestTime() time.Time { return time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC) }

// at returns nssTestTime moved by d, as a pointer for nullable columns.
func at(d time.Duration) *time.Time { return ptr(nssTestTime().Add(d)) }

// ptr returns a pointer to v, for nullable columns.
func ptr[T any](v T) *T { return &v }

// seedNSSUser writes the users row the NSS tables' foreign keys need and
// returns its id.
func seedNSSUser(t *testing.T, db *gorm.DB, id string) string {
	t.Helper()

	user := User{ID: id, Subject: "sub-" + id, Username: id, CreatedAt: nssTestTime(), UpdatedAt: nssTestTime()}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user %s: %v", id, err)
	}
	return id
}

// normalizeTimes rewrites every time.Time and *time.Time field of the
// struct v points to into UTC without a monotonic reading, so rows read
// back from either backend compare with reflect.DeepEqual.
func normalizeTimes(v any) {
	rv := reflect.ValueOf(v).Elem()
	for i := range rv.NumField() {
		field := rv.Field(i)
		switch value := field.Interface().(type) {
		case time.Time:
			field.Set(reflect.ValueOf(value.UTC().Round(0)))
		case *time.Time:
			if value != nil {
				field.Set(reflect.ValueOf(ptr(value.UTC().Round(0))))
			}
		}
	}
}

// assertRoundTrip stores want in a table that holds nothing else, reads the
// table back, and fails unless it holds exactly that row with every field
// intact.
func assertRoundTrip[T any](t *testing.T, db *gorm.DB, want T) {
	t.Helper()

	if err := db.Create(&want).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	var rows []T
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("read back %d rows, want 1", len(rows))
	}
	got := rows[0]
	normalizeTimes(&want)
	normalizeTimes(&got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("row did not round-trip:\n got  %+v\n want %+v", got, want)
	}
}

func TestPosixModels_ShouldNameTheirMigrationTableWhenAskedForTableName(t *testing.T) {
	tests := []struct {
		name  string
		model interface{ TableName() string }
		want  string
	}{
		{name: "should map PosixUser to posix_users when asked", model: PosixUser{}, want: "posix_users"},
		{name: "should map PosixGroup to posix_groups when asked", model: PosixGroup{}, want: "posix_groups"},
		{name: "should map PosixGroupMember to posix_group_members when asked", model: PosixGroupMember{}, want: "posix_group_members"},
		{name: "should map NSSCollision to nss_collisions when asked", model: NSSCollision{}, want: "nss_collisions"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.model.TableName(); got != tt.want {
				t.Errorf("TableName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPosixUser_ShouldRoundTripEveryColumnWhenStoredAndReadBack(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		row  func(userID string) PosixUser
	}{
		{
			name: "should keep every field when all of them are set",
			row: func(userID string) PosixUser {
				return PosixUser{
					ID: "pu-1", UID: 10001, Name: "ada.lovelace", DirectoryID: "6f1c2a90-entry",
					DN: "uid=ada.lovelace,ou=people,dc=example", UserID: &userID, GID: new(uint32(10001)),
					PendingUID: ptr(uint32(20001)), PendingGID: ptr(uint32(20002)), PendingSince: at(time.Hour),
					Home: ptr("/home/ada.lovelace"), Shell: ptr("/bin/bash"),
					Gecos: "Ada Lovelace", DirDisabled: true, MissingSince: at(2 * time.Hour),
					DeletedAt: at(3 * time.Hour), WithheldAt: at(4 * time.Hour),
					WithheldReason: "uid 10001 is also carried by uid=bob,ou=people,dc=example",
					CreatedAt:      nssTestTime(), UpdatedAt: nssTestTime().Add(time.Minute), LastSyncedAt: nssTestTime().Add(2 * time.Minute),
				}
			},
		},
		{
			name: "should keep nil nullable fields nil when none of them are set",
			row: func(string) PosixUser {
				return PosixUser{
					ID: "pu-2", UID: 10002, Name: "svc-backup", DN: "uid=svc-backup,ou=services,dc=example",
					GID: new(uint32(10002)), CreatedAt: nssTestTime(), UpdatedAt: nssTestTime(), LastSyncedAt: nssTestTime(),
				}
			},
		},
		{
			name: "should hold the top of the uint32 range when uid and gid are at the boundary",
			row: func(string) PosixUser {
				return PosixUser{
					ID: "pu-3", UID: math.MaxUint32, Name: "nobody-max", DN: "uid=nobody-max,dc=example",
					GID: new(uint32(math.MaxUint32)), PendingUID: ptr(uint32(math.MaxUint32)), PendingGID: ptr(uint32(0)),
					CreatedAt: nssTestTime(), UpdatedAt: nssTestTime(), LastSyncedAt: nssTestTime(),
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			assertRoundTrip(t, db, tt.row(seedNSSUser(t, db, "u-ada")))
		})
	}
}

func TestPosixUser_ShouldRefuseASecondRowWhenAUniqueValueIsAlreadyHeld(t *testing.T) {
	t.Parallel()

	first := PosixUser{
		ID: "pu-holder", UID: 5000, Name: "alice", DN: "uid=alice,dc=example", GID: new(uint32(5000)),
		CreatedAt: nssTestTime(), UpdatedAt: nssTestTime(), LastSyncedAt: nssTestTime(),
	}
	tests := []struct {
		name       string
		linkHolder bool
		mutate     func(row *PosixUser, userID string)
		wantErr    bool
	}{
		{name: "should refuse the row when the uid is held", mutate: func(r *PosixUser, _ string) { r.UID = first.UID }, wantErr: true},
		{name: "should refuse the row when the name is held", mutate: func(r *PosixUser, _ string) { r.Name = first.Name }, wantErr: true},
		{name: "should refuse the row when the dn is held", mutate: func(r *PosixUser, _ string) { r.DN = first.DN }, wantErr: true},
		{name: "should accept the row when its dn is released and the holder's is not", mutate: func(r *PosixUser, _ string) { r.DN = "" }, wantErr: false},
		{name: "should refuse the row when the person is already linked", linkHolder: true, mutate: func(r *PosixUser, id string) { r.UserID = &id }, wantErr: true},
		{name: "should accept the row when neither row is linked to a person", mutate: func(*PosixUser, string) {}, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			userID := seedNSSUser(t, db, "u-alice")
			holder := first
			if tt.linkHolder {
				holder.UserID = &userID
			}
			if err := db.Create(&holder).Error; err != nil {
				t.Fatalf("create holder: %v", err)
			}

			second := PosixUser{
				ID: "pu-second", UID: 5001, Name: "bob", DN: "uid=bob,dc=example", GID: new(uint32(5001)),
				CreatedAt: nssTestTime(), UpdatedAt: nssTestTime(), LastSyncedAt: nssTestTime(),
			}
			tt.mutate(&second, userID)
			err := db.Create(&second).Error
			if tt.wantErr && !errors.Is(err, gorm.ErrDuplicatedKey) {
				t.Errorf("create = %v, want a duplicated key error", err)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("create = %v, want success", err)
			}
		})
	}
}

func TestPosixGroup_ShouldRoundTripEveryColumnWhenStoredAndReadBack(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		row  PosixGroup
	}{
		{
			name: "should keep every field when all of them are set",
			row: PosixGroup{
				ID: "pg-1", GID: 20000, PendingGID: ptr(uint32(20001)), PendingSince: at(time.Hour),
				Name: "engineering", DirectoryID: "a1b2-entry", DN: "cn=engineering,ou=groups,dc=example",
				MissingSince: at(3 * time.Hour), DeletedAt: at(4 * time.Hour),
				WithheldAt: at(2 * time.Hour), WithheldReason: "gid 20000 is also carried by cn=eng,dc=example",
				CreatedAt: nssTestTime(), UpdatedAt: nssTestTime().Add(time.Minute), LastSyncedAt: nssTestTime().Add(5 * time.Hour),
			},
		},
		{
			name: "should keep nil nullable fields nil when none of them are set",
			row: PosixGroup{
				ID: "pg-2", GID: math.MaxUint32, Name: "ada.lovelace", DN: "cn=ada.lovelace,dc=example",
				CreatedAt: nssTestTime(), UpdatedAt: nssTestTime(), LastSyncedAt: nssTestTime(),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assertRoundTrip(t, testdb.New(t), tt.row)
		})
	}
}

func TestPosixGroupMember_ShouldRoundTripWhenStoredAndReadBack(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		row  PosixGroupMember
	}{
		{name: "should keep the pair when both numbers are ordinary", row: PosixGroupMember{GID: 20000, UID: 10001}},
		{name: "should keep the pair when both numbers are zero", row: PosixGroupMember{GID: 0, UID: 0}},
		{name: "should keep the pair when both numbers are at the top of the range", row: PosixGroupMember{GID: math.MaxUint32, UID: math.MaxUint32}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			if err := db.Create(&tt.row).Error; err != nil {
				t.Fatalf("create: %v", err)
			}
			var got []PosixGroupMember
			if err := db.Find(&got).Error; err != nil {
				t.Fatalf("read back: %v", err)
			}
			if len(got) != 1 || got[0] != tt.row {
				t.Errorf("rows = %+v, want exactly %+v", got, tt.row)
			}
		})
	}
}

func TestPosixGroup_ShouldRefuseASecondRowWhenAUniqueValueIsAlreadyHeld(t *testing.T) {
	t.Parallel()

	first := PosixGroup{
		ID: "pg-holder", GID: 7000, Name: "devs", DN: "cn=devs,dc=example",
		CreatedAt: nssTestTime(), UpdatedAt: nssTestTime(), LastSyncedAt: nssTestTime(),
	}
	tests := []struct {
		name    string
		mutate  func(row *PosixGroup)
		wantErr bool
	}{
		{name: "should refuse the row when the gid is held", mutate: func(r *PosixGroup) { r.GID = first.GID }, wantErr: true},
		{name: "should refuse the row when the name is held", mutate: func(r *PosixGroup) { r.Name = first.Name }, wantErr: true},
		{name: "should refuse the row when the dn is held", mutate: func(r *PosixGroup) { r.DN = first.DN }, wantErr: true},
		{name: "should accept the row when its dn is released", mutate: func(r *PosixGroup) { r.DN = "" }, wantErr: false},
		{name: "should accept the row when nothing it holds is taken", mutate: func(*PosixGroup) {}, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			holder := first
			if err := db.Create(&holder).Error; err != nil {
				t.Fatalf("create holder: %v", err)
			}
			second := PosixGroup{
				ID: "pg-second", GID: 7001, Name: "ops", DN: "cn=ops,dc=example",
				CreatedAt: nssTestTime(), UpdatedAt: nssTestTime(), LastSyncedAt: nssTestTime(),
			}
			tt.mutate(&second)
			err := db.Create(&second).Error
			if tt.wantErr && !errors.Is(err, gorm.ErrDuplicatedKey) {
				t.Errorf("create = %v, want a duplicated key error", err)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("create = %v, want success", err)
			}
		})
	}
}

func TestPosixGroup_ShouldAcceptSeveralReleasedDNsWhenEachIsEmpty(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	for i, id := range []string{"pg-a", "pg-b"} {
		row := PosixGroup{
			ID: id, GID: uint32(8000 + i), Name: id, DN: "",
			CreatedAt: nssTestTime(), UpdatedAt: nssTestTime(), LastSyncedAt: nssTestTime(),
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
}

func TestNSSCollision_ShouldRoundTripEveryColumnWhenStoredAndReadBack(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		row  NSSCollision
	}{
		{
			name: "should keep every field when the other entry is stored too",
			row: NSSCollision{
				Kind: NSSCollisionKindUserName, HolderID: "pu-1", OtherDN: "uid=alice,ou=people,dc=example",
				Value: "alice", OtherID: ptr("pu-2"), OtherName: "alice", OtherNumber: math.MaxUint32,
				FirstSeenAt: nssTestTime(), LastSeenAt: nssTestTime().Add(time.Hour),
			},
		},
		{
			name: "should keep the other row nil when the other entry was refused",
			row: NSSCollision{
				Kind: NSSCollisionKindGID, HolderID: "pg-1", OtherDN: "cn=dup,dc=example", Value: "20000",
				OtherName: "dup", OtherNumber: 20000, FirstSeenAt: nssTestTime(), LastSeenAt: nssTestTime(),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assertRoundTrip(t, testdb.New(t), tt.row)
		})
	}
}

func TestNSSCollision_ShouldRefuseTheRowWhenTheKindIsUnknown(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	row := NSSCollision{
		Kind: "shoe_size", HolderID: "pu-1", OtherDN: "uid=x,dc=example", Value: "9",
		FirstSeenAt: nssTestTime(), LastSeenAt: nssTestTime(),
	}
	if err := db.Create(&row).Error; err == nil {
		t.Error("create accepted a kind outside the CHECK constraint")
	}
}

func TestNSSCollisionKind_ShouldReportTheGroupTableWhenTheKindIsAboutGroups(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kind NSSCollisionKind
		want bool
	}{
		{name: "should report accounts when the kind is uid", kind: NSSCollisionKindUID, want: false},
		{name: "should report accounts when the kind is user_name", kind: NSSCollisionKindUserName, want: false},
		{name: "should report groups when the kind is gid", kind: NSSCollisionKindGID, want: true},
		{name: "should report groups when the kind is group_name", kind: NSSCollisionKindGroupName, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.kind.ForGroups(); got != tt.want {
				t.Errorf("ForGroups() = %v, want %v", got, tt.want)
			}
		})
	}
}
