package model

type Variable struct {
	// 键名称
	Key string `bson:"key" json:"key"`

	// 值
	Value interface{} `bson:"value" json:"value"`

	// 描述
	Description string `bson:"description" json:"description"`

	// 系统的
	System *bool `bson:"system" json:"-"`

	// 脱敏的
	Secret *bool `bson:"secret" json:"-"`
}
