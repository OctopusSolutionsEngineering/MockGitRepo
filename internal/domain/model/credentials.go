package model

type Credentials struct {
	Id       string `jsonapi:"primary,credentials" json:"id"`
	Password string `jsonapi:"attr,password" json:"password"`
}
