// Created by Mike Nestor <me@mikenestor.org>
package store

import (
	"reflect"
	"testing"
	"time"
)

func TestNewMemoryCertRequestStore(t *testing.T) {
	tests := []struct {
		name string
		want *MemoryCertRequestStore
	}{
		{
			name: "empty store",
			want: &MemoryCertRequestStore{
				requests: map[string]CertRequest{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewMemoryCertRequestStore(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewMemoryCertRequestStore() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMemoryCertRequestStore_CreateGetDelete(t *testing.T) {
	s := &MemoryCertRequestStore{
		requests: map[string]CertRequest{},
	}

	if len(s.requests) != 0 {
		t.Errorf("New cert store created with values!")
	}

	testCertRequest := CertRequest{
		ID:        "testid",
		Pubkey:    "some key",
		Type:      "User",
		Account:   "account",
		CreatedAt: time.Now(),
	}

	err := s.Create(&testCertRequest)
	if err != nil {
		t.Error("got error on first create")
	}

	if len(s.requests) != 1 {
		t.Errorf("Cert store has more than 1 record after only 1 Create()")
	}

	_, err = s.Get("invalid id")
	if err == nil {
		t.Error("Got no error when we wanted one")
	}
	if !reflect.DeepEqual(err, &RecordNotFoundError{}) {
		t.Errorf("Error returned is not what we wanted: %v", err)
	}

	err = s.Create(&testCertRequest)
	if err == nil {
		t.Error("Expected an error giving duplicat key got nil")
	}
	if err.Error() != "duplicate id: testid" {
		t.Errorf("Dupe error not same Want:duplicate id: testid = Got:%v", err)
	}

	testCertRequestNoID := CertRequest{
		ID:        "",
		Pubkey:    "some key",
		Type:      "User",
		Account:   "account",
		CreatedAt: time.Now(),
	}

	err = s.Create(&testCertRequestNoID)
	if err != nil {
		t.Errorf("Error not expected: %v", err)
	}
	if testCertRequestNoID.ID == "" {
		t.Error("Expected ID to be set")
	}

	newCertReq, err := s.Get(testCertRequest.ID)
	if err != nil {
		t.Errorf("Got error when we wanted no error: %v", err)
	}
	if newCertReq.Pubkey != testCertRequest.Pubkey {
		t.Errorf("Pubkey returned is not the same as what we stored: [%s] != [%s]", testCertRequest.Pubkey, newCertReq.Pubkey)
	}

	lb := s.Count()
	err = s.Delete(testCertRequest.ID)
	if err != nil {
		t.Errorf("Got error deleting known id %v", err)
	}

	la := s.Count()
	if la != lb-1 {
		t.Errorf("Delete did not remove an item: %v != %v", lb, la)
	}
}
