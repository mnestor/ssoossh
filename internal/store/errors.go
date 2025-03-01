// Created by Mike Nestor <me@mikenestor.org>
package store

import (
	"fmt"
)

type DuplicateKeyError struct {
	ID string
}

func (e *DuplicateKeyError) Error() string {
	return fmt.Sprintf("duplicate movie id: %v", e.ID)
}

type RecordNotFoundError struct{}

func (e *RecordNotFoundError) Error() string {
	return "record not found"
}
