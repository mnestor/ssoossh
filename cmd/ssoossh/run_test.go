// Created By Mike Nestor <me@mikenestor.org>
package main

import (
	"bytes"
	"context"
	"testing"
)

func Test_run(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantO   string
		wantE   string
		wantErr bool
	}{
		{
			name:    "nothing passed in",
			args:    []string{},
			wantO:   "",
			wantE:   "",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &bytes.Buffer{}
			e := &bytes.Buffer{}
			ctx := context.Background()
			_ = run(ctx, o, e, tt.args)

			if tt.wantErr && e.String() != tt.wantE {
				t.Errorf("Error: [%v] [%v]", tt.wantE, e)
			}
		})
	}
}
