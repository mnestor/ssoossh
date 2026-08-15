//go:build pam

package main

import (
	"errors"
	"testing"
)

// stubLogger is a no-op Logger for tests that don't care about log output.
type stubLogger struct{}

func (stubLogger) Debugf(format string, v ...any)   {}
func (stubLogger) Infof(format string, v ...any)    {}
func (stubLogger) Noticef(format string, v ...any)  {}
func (stubLogger) Warningf(format string, v ...any) {}
func (stubLogger) Errorf(format string, v ...any)   {}
func (stubLogger) SetDebug(d string)                {}
func (stubLogger) Close() error                     { return nil }

func TestAuthenticate(t *testing.T) {
	tests := []struct {
		name     string
		cfg      config
		wantCode int
		wantErr  error
	}{
		{
			name:     "should return PamUserUnknown when server is not configured",
			cfg:      config{trustedCAFile: "/etc/ssoossh/ca.pub"},
			wantCode: PamUserUnknown,
			wantErr:  errors.New("not configured correctly in pam.d"),
		},
		{
			name:     "should return PamNoModuleData when trusted-ca-file is not configured",
			cfg:      config{server: "https://example.com"},
			wantCode: PamNoModuleData,
			wantErr:  errors.New("not configured correctly in pam.d"),
		},
		{
			name:     "should fail closed with PamAuthInfoUnavail when configured correctly, since certificate issuance is not implemented yet",
			cfg:      config{server: "https://example.com", trustedCAFile: "/etc/ssoossh/ca.pub"},
			wantCode: PamAuthInfoUnavail,
			wantErr:  errNotImplemented,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var log Logger = stubLogger{}
			gotCode, gotErr := Authenticate(&log, "alice", tt.cfg)
			if gotCode != tt.wantCode {
				t.Errorf("Authenticate() code = %d, want %d", gotCode, tt.wantCode)
			}
			if gotErr == nil || gotErr.Error() != tt.wantErr.Error() {
				t.Errorf("Authenticate() err = %v, want %v", gotErr, tt.wantErr)
			}
		})
	}
}
