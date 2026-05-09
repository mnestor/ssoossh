package gormsession

// Source: https://github.com/gin-contrib/sessions/blob/master/gorm/gorm.go
// Copyright (c) 2016 Gin-Gonic

import (
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/wader/gormstore/v2"
	"gorm.io/gorm"
)

type Store interface {
	sessions.Store
}

func NewStore(d *gorm.DB, expiredSessionCleanup bool, keyPairs ...[]byte) Store {
	s := gormstore.New(d, keyPairs...)
	if expiredSessionCleanup {
		quit := make(chan struct{})
		go s.PeriodicCleanup(1*time.Hour, quit)
	}
	// Since securecookie has a default size of 4096 we need to kill that
	// so we can use it to encrypt/decrypt values going into the database
	s.MaxLength(0)
	return &store{s}
}

type store struct {
	*gormstore.Store
}

func (s *store) Options(options sessions.Options) {
	s.SessionOpts = options.ToGorillaOptions()
}
