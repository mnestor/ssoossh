// Created By Mike Nestor <me@mikenestor.org>
package nonce

import "testing"

func TestNewNonce(t *testing.T) {
	wantedLen64 := 64
	gotNonce := NewNonce(wantedLen64)
	if len(gotNonce) != wantedLen64 {
		t.Errorf("got length %q, wanted %q", gotNonce, wantedLen64)
	}
}
