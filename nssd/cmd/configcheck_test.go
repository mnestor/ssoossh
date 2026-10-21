package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/internal/configcheck"
)

// unroutableServer is in TEST-NET-1 (RFC 5737): a check that tried to
// reach it would hang until a timeout rather than finish at once.
const unroutableServer = "https://192.0.2.1"

// TestConfigCheck_ShouldPassWhenTheDaemonWouldStart covers exit 0 and the
// check contacting nothing.
func TestConfigCheck_ShouldPassWhenTheDaemonWouldStart(t *testing.T) {
	t.Parallel()
	h := newHost(t, unroutableServer, "AAAA", "")
	if err := os.WriteFile(h.nsswitch, []byte("passwd: files systemd\ngroup: files systemd\nshadow: files systemd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	out, err := h.exec(t, "config", "check")
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("config check took %v: it must not wait on the server", elapsed)
	}
	if err != nil {
		t.Fatalf("config check failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "0 errors, 0 warnings: the configuration is valid") {
		t.Errorf("output %q does not say the configuration is valid", out)
	}
}

// TestConfigCheck_ShouldReportWhatTheDaemonWouldRefuse covers the mode,
// owner-adjacent and content findings, each sorted by kind, and the
// start-time warnings.
func TestConfigCheck_ShouldReportWhatTheDaemonWouldRefuse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		extra     string
		setup     func(t *testing.T, h *host)
		wantKinds []configcheck.Kind
		wantFail  bool
	}{
		{
			name: "should report file permissions when nss.yaml is world-readable",
			setup: func(t *testing.T, h *host) {
				if err := os.Chmod(h.config, 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantKinds: []configcheck.Kind{configcheck.KindFilePermissions},
			wantFail:  true,
		},
		{
			name: "should report file permissions when the key is group-readable",
			setup: func(t *testing.T, h *host) {
				if err := os.Chmod(h.key, 0o640); err != nil {
					t.Fatal(err)
				}
			},
			wantKinds: []configcheck.Kind{configcheck.KindFilePermissions},
			wantFail:  true,
		},
		{
			name:      "should report each validation failure when nss.yaml has two",
			extra:     "min_id: 0\nentry_cache_nowait_percentage: 200\n",
			wantKinds: []configcheck.Kind{configcheck.KindValidation, configcheck.KindValidation},
			wantFail:  true,
		},
		{
			name: "should report a load failure when the override file does not parse",
			setup: func(t *testing.T, h *host) {
				if err := os.WriteFile(h.overrides, []byte("alice: [\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantKinds: []configcheck.Kind{configcheck.KindLoad},
			wantFail:  true,
		},
		{
			name: "should warn and pass when only the system store is trusted",
			setup: func(t *testing.T, h *host) {
				// newHost pins a key; with no pin the store is all.
				yaml := "server: " + unroutableServer + "\ntls_ca_file: system\napi_key_file: " + h.key + "\n"
				if err := os.WriteFile(h.config, []byte(yaml), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantKinds: []configcheck.Kind{configcheck.KindWarning},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHost(t, unroutableServer, "AAAA", tt.extra)
			if err := os.WriteFile(h.nsswitch, []byte("passwd: files systemd\ngroup: files systemd\nshadow: files systemd\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if tt.setup != nil {
				tt.setup(t, h)
			}
			out, err := h.exec(t, "config", "check", "--json")
			if failed := errors.Is(err, configcheck.ErrInvalid); failed != tt.wantFail {
				t.Fatalf("err = %v, want failure %v\n%s", err, tt.wantFail, out)
			}
			var report configcheck.Report
			if err := json.Unmarshal([]byte(out), &report); err != nil {
				t.Fatalf("output is not a JSON report: %v\n%s", err, out)
			}
			var got []configcheck.Kind
			for _, f := range report.Findings {
				got = append(got, f.Kind)
			}
			if len(got) != len(tt.wantKinds) {
				t.Fatalf("finding kinds = %v, want %v\n%s", got, tt.wantKinds, out)
			}
			for i := range got {
				if got[i] != tt.wantKinds[i] {
					t.Errorf("finding kinds = %v, want %v", got, tt.wantKinds)
				}
			}
			if strings.Contains(out, "s3cret") {
				t.Error("the report discloses the fleet key secret")
			}
		})
	}
}

// TestConfigCheck_ShouldWarnWhenNsswitchDoesNotConsultTheDaemon covers the
// nsswitch.conf check: a warning, as the daemon only logs it.
func TestConfigCheck_ShouldWarnWhenNsswitchDoesNotConsultTheDaemon(t *testing.T) {
	t.Parallel()
	h := newHost(t, unroutableServer, "AAAA", "")
	out, err := h.exec(t, "config", "check")
	if err != nil {
		t.Fatalf("config check failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "warning  nsswitch.conf will not consult this daemon") {
		t.Errorf("output %q lacks the nsswitch.conf warning", out)
	}
}
