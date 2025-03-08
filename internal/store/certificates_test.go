// Created by Mike Nestor <me@mikenestor.org>
package store

import (
	"reflect"
	"testing"
	"time"
)

func TestNewMemoryCertificatesStore(t *testing.T) {
	tests := []struct {
		name string
		want *MemoryCertificatesStore
	}{
		{
			name: "empty store",
			want: &MemoryCertificatesStore{
				certs: map[string]Certificate{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewMemoryCertificatesStore(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewMemoryCertificatesStore() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMemoryCertificatesStore_CreateGetDelete(t *testing.T) {
	s := &MemoryCertificatesStore{
		certs: map[string]Certificate{},
	}

	if len(s.certs) != 0 {
		t.Errorf("New cert store created with values!")
	}

	testCert := Certificate{
		ID:          "testid",
		Certificate: "some cert",
		CreatedAt:   time.Now(),
	}

	testCertWaitAfter := Certificate{
		ID:          "testid after",
		Certificate: "some cert after",
		CreatedAt:   time.Now(),
	}

	testCertWaitBefore := Certificate{
		ID:          "testid before",
		Certificate: "some cert before",
		CreatedAt:   time.Now(),
	}

	err := s.Create(&testCert)
	if err != nil {
		t.Error("got error on first create")
	}

	if len(s.certs) != 1 {
		t.Errorf("Cert store has more than 1 record after only 1 Create()")
	}

	err = s.Create(&testCert)
	dupErr := &DuplicateKeyError{ID: testCert.ID}
	if err.Error() != dupErr.Error() {
		t.Errorf("Dupe error not same Want:%v = Got:%v", err, dupErr)
	}

	newCert, err := s.Get(testCert.ID)
	if err != nil {
		t.Errorf("Got error when we wanted no error: %v", err)
	}
	if newCert.Certificate != testCert.Certificate {
		t.Errorf("Certificate returned is not the same as what we stored: [%s] != [%s]", testCert.Certificate, newCert.Certificate)
	}

	_, err = s.Get(testCertWaitBefore.ID)
	if err == nil {
		t.Error("Got no error when we wanted one")
	}

	go func() {
		waitCert := s.GetWait(testCertWaitBefore.ID)
		<-waitCert.Phone
		waitCertGet, err := s.Get(testCertWaitBefore.ID)
		if err != nil {
			t.Errorf("GetWait: Got error when we wanted no error: %v", err)
		}
		if waitCertGet.Certificate != testCertWaitBefore.Certificate {
			t.Errorf("GetWait: Certificate returned is not the same as what we stored: [%s] != [%s]", testCert.Certificate, newCert.Certificate)
		}
	}()

	go func() {
		waitCert := s.GetWait(testCertWaitBefore.ID)
		<-waitCert.Phone
		waitCertGet, err := s.Get(testCertWaitBefore.ID)
		if err != nil {
			t.Errorf("GetWait Duplicate: Got error when we wanted no error: %v", err)
		}
		if waitCertGet.Certificate != testCertWaitBefore.Certificate {
			t.Errorf("GetWait Duplicate: Certificate returned is not the same as what we stored: [%s] != [%s]", testCert.Certificate, newCert.Certificate)
		}
	}()

	time.Sleep(2 * time.Second)
	err = s.Create(&testCertWaitBefore)
	if err != nil {
		t.Errorf("Create2: Got error when we wanted no error: %v", err)
	}

	lb := s.Count()
	err = s.Delete(testCert.ID)
	if err != nil {
		t.Errorf("Got error deleting known id %v", err)
	}

	la := s.Count()
	if la != lb-1 {
		t.Errorf("Delete did not remove an item: %v != %v", lb, la)
	}

	err = s.Create(&testCertWaitAfter)
	if err != nil {
		t.Errorf("Create2: Got error when we wanted no error: %v", err)
	}

	go func() {
		waitCert := s.GetWait(testCertWaitAfter.ID)
		<-waitCert.Phone
		waitCertGet, err := s.Get(testCertWaitAfter.ID)
		if err != nil {
			t.Errorf("GetWait: Got error when we wanted no error: %v", err)
		}
		if waitCertGet.Certificate != testCertWaitAfter.Certificate {
			t.Errorf("GetWait: Certificate returned is not the same as what we stored: [%s] != [%s]", testCert.Certificate, newCert.Certificate)
		}
	}()
}
