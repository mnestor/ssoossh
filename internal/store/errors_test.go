// Created by Mike Nestor <me@mikenestor.org>
package store

import "testing"

func TestDuplicateKeyError_Error(t *testing.T) {
	type fields struct {
		ID string
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{
			name: "valie",
			fields: fields{
				ID: "dupe key",
			},
			want: "duplicate id: dupe key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &DuplicateKeyError{
				ID: tt.fields.ID,
			}
			if got := e.Error(); got != tt.want {
				t.Errorf("DuplicateKeyError.Error() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRecordNotFoundError_Error(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{
			name: "valid error",
			want: "record not found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &RecordNotFoundError{}
			if got := e.Error(); got != tt.want {
				t.Errorf("RecordNotFoundError.Error() = %v, want %v", got, tt.want)
			}
		})
	}
}
