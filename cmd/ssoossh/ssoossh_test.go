// Created By Mike Nestor <me@mikenestor.org>
package main

import (
	"bytes"
	"context"
	"testing"
)

func Test_main(t *testing.T) {
	// ctxTest := context.Background()

	// o = &bytes.Buffer{}
	// e = &bytes.Buffer{}

	// ctx, cancel = context.WithCancel(ctxTest)
	// t.Cleanup(cancel)

	// main()
}

func Test_run(t *testing.T) {
	type args struct {
		ctx  context.Context
		args []string
	}
	tests := []struct {
		name    string
		args    args
		wantO   string
		wantE   string
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &bytes.Buffer{}
			e := &bytes.Buffer{}
			if err := run(tt.args.ctx, o, e, tt.args.args); (err != nil) != tt.wantErr {
				t.Errorf("run() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotO := o.String(); gotO != tt.wantO {
				t.Errorf("run() = %v, want %v", gotO, tt.wantO)
			}
			if gotE := e.String(); gotE != tt.wantE {
				t.Errorf("run() = %v, want %v", gotE, tt.wantE)
			}
		})
	}
}
