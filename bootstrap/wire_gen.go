// Code generated by Wire. DO NOT EDIT.

//go:generate go run -mod=mod github.com/google/wire/cmd/wire
//go:build !wireinject
// +build !wireinject

package bootstrap

import (
	"github.com/weplanx/server/api"
	"github.com/weplanx/server/api/index"
	"github.com/weplanx/server/common"
)

// Injectors from wire.go:

func NewAPI(values *common.Values) (*api.API, error) {
	client, err := UseRedis(values)
	if err != nil {
		return nil, err
	}
	conn, err := UseNats(values)
	if err != nil {
		return nil, err
	}
	jetStreamContext, err := UseJetStream(conn)
	if err != nil {
		return nil, err
	}
	keyValue, err := UseKeyValue(values, jetStreamContext)
	if err != nil {
		return nil, err
	}
	cipher, err := UseCipher(values)
	if err != nil {
		return nil, err
	}
	captcha := UseCaptcha(client)
	locker := UseLocker(client)
	inject := &common.Inject{
		V:         values,
		RDb:       client,
		Nats:      conn,
		JetStream: jetStreamContext,
		KeyValue:  keyValue,
		Cipher:    cipher,
		Captcha:   captcha,
		Locker:    locker,
	}
	hertz, err := UseHertz(values)
	if err != nil {
		return nil, err
	}
	csrf := UseCsrf(values)
	passport := UseAPIPassport(values)
	service := &index.Service{
		Inject:   inject,
		Passport: passport,
	}
	controller := &index.Controller{
		V:      values,
		Csrf:   csrf,
		IndexX: service,
	}
	apiAPI := &api.API{
		Inject: inject,
		Hertz:  hertz,
		Csrf:   csrf,
		Index:  controller,
		IndexX: service,
	}
	return apiAPI, nil
}
