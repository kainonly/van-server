package model

import (
	"taste-api/application/common/types"
)

type RoleBasic struct {
	ID         uint64
	Key        string     `gorm:"size:200;unique;not null;comment:role key"`
	Name       types.JSON `gorm:"type:jsonb;not null;comment:role name"`
	Note       string     `gorm:"type:text;comment:note"`
	Status     bool       `gorm:"not null;default:1"`
	CreateTime uint64     `gorm:"not null;autoCreateTime"`
	UpdateTime uint64     `gorm:"not null;autoUpdateTime"`
}
