package model

import (
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/test/testdb"
)

// seedNSSKey writes a minimal live key created by a freshly seeded user, for
// the usage rows' foreign key, and returns its id.
func seedNSSKey(t *testing.T, db *gorm.DB, id string) string {
	t.Helper()

	key := NSSKey{
		ID: id, CurrentGeneration: 1, SecretHash: "00", Name: id,
		CreatedBy: seedNSSUser(t, db, "u-"+id), ExpiresAt: ptr(nssTestTime().Add(24 * time.Hour)),
		CreatedAt: nssTestTime(), UpdatedAt: nssTestTime(),
	}
	if err := db.Create(&key).Error; err != nil {
		t.Fatalf("seed key %s: %v", id, err)
	}
	return id
}

func TestNSSModels_ShouldNameTheirMigrationTableWhenAskedForTableName(t *testing.T) {
	tests := []struct {
		name  string
		model interface{ TableName() string }
		want  string
	}{
		{name: "should map NSSKey to nss_keys when asked", model: NSSKey{}, want: "nss_keys"},
		{name: "should map NSSKeyUsage to nss_key_usage when asked", model: NSSKeyUsage{}, want: "nss_key_usage"},
		{name: "should map NSSAlert to nss_alerts when asked", model: NSSAlert{}, want: "nss_alerts"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.model.TableName(); got != tt.want {
				t.Errorf("TableName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNSSKey_ShouldRoundTripEveryColumnWhenStoredAndReadBack(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		row  func(creator, revoker string) NSSKey
	}{
		{
			name: "should keep every field when all of them are set",
			row: func(creator, revoker string) NSSKey {
				return NSSKey{
					ID: "nss_ABCD2345", CurrentGeneration: 3, SecretHash: "9f86d081884c7d65",
					PreviousSecretHash: ptr("60303ae22b998861"), PreviousExpiresAt: at(720 * time.Hour),
					Name: "web fleet", Description: "every web host", CreatedBy: creator, GroupName: "web-admins",
					ExpiresAt: at(8760 * time.Hour), NotificationEmail: "web-admins@example.com",
					RevokedAt: at(time.Hour), RevokedBy: &revoker, RotatedAt: at(2 * time.Hour),
					ExpiryReminderSentAt: at(3 * time.Hour), LastExpiredAttemptNotifiedAt: at(4 * time.Hour),
					RotationCleanupSentAt: at(5 * time.Hour),
					CreatedAt:             nssTestTime(), UpdatedAt: nssTestTime().Add(time.Minute),
				}
			},
		},
		{
			name: "should keep nil nullable fields nil when none of them are set",
			row: func(creator, _ string) NSSKey {
				return NSSKey{
					ID: "nss_WXYZ6789", CurrentGeneration: 1, SecretHash: "9f86d081884c7d65", Name: "db fleet",
					CreatedBy: creator, CreatedAt: nssTestTime(), UpdatedAt: nssTestTime(),
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			assertRoundTrip(t, db, tt.row(seedNSSUser(t, db, "u-creator"), seedNSSUser(t, db, "u-revoker")))
		})
	}
}

func TestNSSKey_ShouldRefuseTheRowWhenTheCreatorIsNotAUser(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	key := NSSKey{
		ID: "nss_ORPHAN22", CurrentGeneration: 1, SecretHash: "00", Name: "orphan", CreatedBy: "no-such-user",
		ExpiresAt: ptr(nssTestTime()), CreatedAt: nssTestTime(), UpdatedAt: nssTestTime(),
	}
	if err := db.Create(&key).Error; err == nil {
		t.Error("create succeeded, want the users foreign key to refuse it")
	}
}

func TestNSSKeyUsage_ShouldRoundTripEveryColumnWhenStoredAndReadBack(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		row  func(keyID string) NSSKeyUsage
	}{
		{
			name: "should keep every field when the counts are ordinary",
			row: func(keyID string) NSSKeyUsage {
				return NSSKeyUsage{
					KeyID: keyID, SourceIP: "192.0.2.10", Generation: 2, Day: nssTestTime().Truncate(24 * time.Hour),
					Lookups: 1200, Misses: 7, FirstSeen: nssTestTime(), LastSeen: nssTestTime().Add(time.Hour),
				}
			},
		},
		{
			name: "should keep counts past 2^31 when a day is busy",
			row: func(keyID string) NSSKeyUsage {
				return NSSKeyUsage{
					KeyID: keyID, SourceIP: "2001:db8::1", Generation: 1, Day: nssTestTime().Truncate(24 * time.Hour),
					Lookups: 1 << 40, Misses: 1 << 33, FirstSeen: nssTestTime(), LastSeen: nssTestTime(),
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			assertRoundTrip(t, db, tt.row(seedNSSKey(t, db, "nss_USAGE234")))
		})
	}
}

func TestNSSKeyUsage_ShouldRefuseADuplicateWhenEveryKeyColumnMatches(t *testing.T) {
	t.Parallel()

	base := func(keyID string) NSSKeyUsage {
		return NSSKeyUsage{
			KeyID: keyID, SourceIP: "192.0.2.10", Generation: 1, Day: nssTestTime().Truncate(24 * time.Hour),
			Lookups: 1, FirstSeen: nssTestTime(), LastSeen: nssTestTime(),
		}
	}
	tests := []struct {
		name    string
		mutate  func(*NSSKeyUsage)
		wantErr bool
	}{
		{name: "should refuse the row when key, address, generation and day all match", mutate: func(*NSSKeyUsage) {}, wantErr: true},
		{name: "should accept the row when only the generation differs", mutate: func(u *NSSKeyUsage) { u.Generation = 2 }},
		{name: "should accept the row when only the day differs", mutate: func(u *NSSKeyUsage) { u.Day = u.Day.Add(24 * time.Hour) }},
		{name: "should accept the row when only the address differs", mutate: func(u *NSSKeyUsage) { u.SourceIP = "192.0.2.11" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			keyID := seedNSSKey(t, db, "nss_DUPLIC23")
			first := base(keyID)
			if err := db.Create(&first).Error; err != nil {
				t.Fatalf("create first: %v", err)
			}
			second := base(keyID)
			tt.mutate(&second)
			err := db.Create(&second).Error
			if tt.wantErr && err == nil {
				t.Error("create succeeded, want the primary key to refuse it")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("create = %v, want success", err)
			}
		})
	}
}

func TestNSSAlert_ShouldRoundTripWhenStoredAndReadBack(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		row  NSSAlert
	}{
		{name: "should keep the claim when the subject is a uid", row: NSSAlert{Kind: "uid_collision", Subject: "10001", LastRaisedAt: nssTestTime()}},
		{name: "should keep the claim when the subject is a key id", row: NSSAlert{Kind: "key_source", Subject: "nss_ABCD2345", LastRaisedAt: nssTestTime()}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assertRoundTrip(t, testdb.New(t), tt.row)
		})
	}
}
