// Created By Mike Nestor <me@mikenestor.org>
package nonce

import (
	rand "math/rand"
)

var (
	letters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
)

func NewNonce(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rand.Int63()%int64(len(letters))]
	}
	return string(b)
}
