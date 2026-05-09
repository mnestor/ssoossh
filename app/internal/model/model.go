package model

import (
	"gorm.io/gorm"
)

type Model struct {
	db *gorm.DB
}

func NewModel(db *gorm.DB) *Model {
	return &Model{db}
}
