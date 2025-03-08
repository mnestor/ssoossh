// Created by Mike Nestor <me@mikenestor.org>
package log

import (
	"log/slog"
	"reflect"
	"testing"
)

func TestSetupLogger(t *testing.T) {
	type args struct {
		c LogSettings
	}
	tests := []struct {
		name string
		args args
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetupLogger(tt.args.c)
		})
	}
}

func TestGetLogger(t *testing.T) {
	tests := []struct {
		name string
		want *slog.Logger
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetLogger(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetLogger() = %v, want %v", got, tt.want)
			}
		})
	}
}
