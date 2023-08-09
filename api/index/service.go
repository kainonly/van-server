package index

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"github.com/cloudwego/hertz/pkg/common/errors"
	"github.com/dgryski/dgoogauth"
	"github.com/google/uuid"
	"github.com/thoas/go-funk"
	"github.com/weplanx/go/locker"
	"github.com/weplanx/go/passlib"
	"github.com/weplanx/go/passport"
	"github.com/weplanx/go/sessions"
	"github.com/weplanx/server/api/tencent"
	"github.com/weplanx/server/common"
	"github.com/weplanx/server/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"net/url"
	"time"
)

type Service struct {
	*common.Inject
	Sessions *sessions.Service
	Tencent  *tencent.Service
}

type LoginResult struct {
	User        model.User
	AccessToken string
}

func (x *Service) Logining(ctx context.Context, filter bson.M) (u model.User, err error) {
	if err = x.Db.Collection("users").FindOne(ctx, filter).Decode(&u); err != nil {
		if err == mongo.ErrNoDocuments {
			err = common.ErrLoginNotExists
			return
		}
		return
	}

	if err = x.Locker.Verify(ctx, u.ID.Hex(), x.V.LoginFailures); err != nil {
		switch err {
		case locker.ErrLockerNotExists:
			err = nil
			break
		case locker.ErrLocked:
			err = common.ErrLoginMaxFailures
			return
		default:
			return
		}
	}

	return
}

func (x *Service) CreateAccessToken(ctx context.Context, userId string) (ts string, err error) {
	jti := uuid.New().String()
	if ts, err = x.Passport.Create(userId, jti); err != nil {
		return
	}
	if status := x.Sessions.Set(ctx, userId, jti); status != "OK" {
		err = common.ErrSession
		return
	}
	x.Locker.Delete(ctx, userId)

	key := x.V.Name("users", userId)
	if err = x.RDb.Del(ctx, key).Err(); err != nil {
		return
	}

	return
}

func (x *Service) Login(ctx context.Context, email string, password string) (r *LoginResult, err error) {
	r = new(LoginResult)
	if r.User, err = x.Logining(ctx, bson.M{"email": email, "status": true}); err != nil {
		return
	}

	userId := r.User.ID.Hex()
	if err = passlib.Verify(password, r.User.Password); err != nil {
		if err == passlib.ErrNotMatch {
			x.Locker.Update(ctx, userId, x.V.LoginTTL)
			err = common.ErrLoginInvalid
			return
		}
		return
	}

	if r.AccessToken, err = x.CreateAccessToken(ctx, userId); err != nil {
		return
	}

	return
}

func (x *Service) GetLoginSms(ctx context.Context, phone string) (code string, err error) {
	keyLock := x.V.Name("phone", phone)
	if err = x.Locker.Verify(ctx, keyLock, x.V.LoginFailures); err != nil {
		switch err {
		case locker.ErrLockerNotExists:
			err = nil
			break
		case locker.ErrLocked:
			err = common.ErrLoginMaxFailures
			return
		default:
			return
		}
	}

	var n int64
	if n, err = x.Db.Collection("users").CountDocuments(ctx, bson.M{"phone": phone}); err != nil {
		return
	}
	if n == 0 {
		x.Locker.Update(ctx, keyLock, time.Hour*24)
		err = errors.NewPublic("该手机号码尚未绑定")
		return
	}

	code = funk.RandomString(6, []rune("0123456789"))
	if err = x.Tencent.SmsSend(ctx, "WEB应用技术分享网", "1889615", []string{code}, []string{phone}); err != nil {
		return
	}
	key := x.V.Name("sms-login", phone)
	if exists := x.Captcha.Exists(ctx, key); exists {
		err = errors.NewPublic("您的验证码请求频繁，请稍后再试")
		return
	}
	x.Captcha.Create(ctx, key, code, time.Minute*2)
	return
}

func (x *Service) LoginSms(ctx context.Context, phone string, code string) (r *LoginResult, err error) {
	r = new(LoginResult)
	if r.User, err = x.Logining(ctx, bson.M{"phone": phone, "status": true}); err != nil {
		return
	}

	userId := r.User.ID.Hex()
	if err = x.Captcha.Verify(ctx, x.V.Name("sms-login", phone), code); err != nil {
		x.Locker.Update(ctx, userId, x.V.LoginTTL)
		err = common.ErrLoginInvalid
		return
	}

	if r.AccessToken, err = x.CreateAccessToken(ctx, userId); err != nil {
		return
	}
	return
}

func (x *Service) LoginTotp(ctx context.Context, email string, code string) (r *LoginResult, err error) {
	r = new(LoginResult)
	if r.User, err = x.Logining(ctx, bson.M{"email": email, "status": true}); err != nil {
		return
	}

	userId := r.User.ID.Hex()
	otpc := &dgoogauth.OTPConfig{
		Secret:      r.User.Totp,
		WindowSize:  1,
		HotpCounter: 0,
		UTC:         true,
	}
	var check bool
	if check, err = otpc.Authenticate(code); err != nil {
		return
	}
	if !check {
		x.Locker.Update(ctx, userId, x.V.LoginTTL)
		err = common.ErrLoginInvalid
		return
	}

	if r.AccessToken, err = x.CreateAccessToken(ctx, userId); err != nil {
		return
	}
	return
}

func (x *Service) WriteLogsetLogined(ctx context.Context, data *model.LogsetLogined) (err error) {
	var r *tencent.CityResult
	if r, err = x.Tencent.GetCity(ctx, data.Metadata.ClientIP); err != nil {
		return
	}
	if !r.Success {
		return errors.NewPublic(r.Msg)
	}
	data.SetVersion("shuliancloud.v4")
	data.SetDetail(r.GetDetail())
	if _, err = x.Db.Collection("logset_logined").InsertOne(ctx, data); err != nil {
		return
	}
	filter := bson.M{"_id": data.Metadata.UserID}
	if _, err = x.Db.Collection("users").UpdateOne(ctx, filter, bson.M{
		"$inc": bson.M{"sessions": 1},
		"$set": bson.M{"history": data},
	}); err != nil {
		return
	}
	return
}

func (x *Service) Verify(ctx context.Context, ts string) (claims passport.Claims, err error) {
	if claims, err = x.Passport.Verify(ts); err != nil {
		return
	}
	result := x.Sessions.Verify(ctx, claims.UserId, claims.ID)
	if !result {
		err = common.ErrSessionInconsistent
		return
	}

	// TODO: Check User Status

	x.Sessions.Renew(ctx, claims.UserId)

	return
}

func (x *Service) GetRefreshCode(ctx context.Context, userId string) (code string, err error) {
	code = uuid.New().String()
	x.Captcha.Create(ctx, userId, code, 15*time.Second)
	return
}

func (x *Service) RefreshToken(ctx context.Context, claims passport.Claims, code string) (ts string, err error) {
	if err = x.Captcha.Verify(ctx, claims.UserId, code); err != nil {
		return
	}
	if ts, err = x.Passport.Create(claims.UserId, claims.ID); err != nil {
		return
	}
	return
}

func (x *Service) Logout(ctx context.Context, userId string) {
	x.Sessions.Remove(ctx, userId)
}

func (x *Service) GetUser(ctx context.Context, userId string) (data M, err error) {
	id, _ := primitive.ObjectIDFromHex(userId)
	var user model.User
	if err = x.Db.Collection("users").
		FindOne(ctx, bson.M{"_id": id}).
		Decode(&user); err != nil {
		return
	}

	detail := M{}
	for _, x := range user.History.Detail.(bson.D) {
		detail[x.Key] = x.Value
	}
	user.History.Detail = detail

	phone := ""
	if user.Phone != "" {
		phone = "*"
	}

	totp := ""
	if user.Totp != "" {
		totp = "*"
	}

	data = M{
		"_id":         user.ID,
		"email":       user.Email,
		"name":        user.Name,
		"avatar":      user.Avatar,
		"phone":       phone,
		"sessions":    user.Sessions,
		"history":     user.History,
		"totp":        totp,
		"status":      user.Status,
		"create_time": user.CreateTime,
		"update_time": user.UpdateTime,
	}

	if user.Lark != nil {
		lark := user.Lark
		data["lark"] = M{
			"name":          lark.Name,
			"en_name":       lark.EnName,
			"avatar_url":    lark.AvatarUrl,
			"avatar_thumb":  lark.AvatarThumb,
			"avatar_middle": lark.AvatarMiddle,
			"avatar_big":    lark.AvatarBig,
			"open_id":       lark.OpenId,
		}
	}

	return
}

func (x *Service) SetUser(ctx context.Context, userId string, update bson.M) (result interface{}, err error) {
	id, _ := primitive.ObjectIDFromHex(userId)

	if result, err = x.Db.Collection("users").
		UpdateByID(ctx, id, update); err != nil {
		return
	}

	key := x.V.Name("users", userId)
	if _, err = x.RDb.Del(ctx, key).Result(); err != nil {
		return
	}

	return
}

func (x *Service) SetUserPassword(ctx context.Context, userId string, old string, password string) (r interface{}, err error) {
	id, _ := primitive.ObjectIDFromHex(userId)
	var user model.User
	if err = x.Db.Collection("users").
		FindOne(ctx, bson.M{"_id": id}).Decode(&user); err != nil {
		return
	}

	if err = passlib.Verify(old, user.Password); err != nil {
		if err == passlib.ErrNotMatch {
			err = errors.NewPublic(passlib.ErrNotMatch.Error())
			return
		}
	}

	var hash string
	if hash, err = passlib.Hash(password); err != nil {
		return
	}
	return x.SetUser(ctx, userId, bson.M{
		"$set": bson.M{
			"password": hash,
		},
	})
}

func (x *Service) GetUserPhoneCode(ctx context.Context, phone string) (code string, err error) {
	code = funk.RandomString(6, []rune("0123456789"))
	if err = x.Tencent.SmsSend(ctx, "WEB应用技术分享网", "1889620", []string{code}, []string{phone}); err != nil {
		return
	}
	key := x.V.Name("sms-bind", phone)
	if exists := x.Captcha.Exists(ctx, key); exists {
		err = errors.NewPublic("您的验证码请求频繁，请稍后再试")
		return
	}
	x.Captcha.Create(ctx, key, code, time.Minute*2)
	return
}

func (x *Service) SetUserPhone(ctx context.Context, userId string, phone string, code string) (r interface{}, err error) {
	if err = x.Captcha.Verify(ctx, x.V.Name("sms-bind", phone), code); err != nil {
		return
	}

	return x.SetUser(ctx, userId, bson.M{
		"$set": bson.M{
			"phone": phone,
		},
	})
}

func (x *Service) GetUserTotp(ctx context.Context, userId string) (totp string, err error) {
	id, _ := primitive.ObjectIDFromHex(userId)
	var user model.User
	if err = x.Db.Collection("users").FindOne(ctx, bson.M{
		"_id": id,
	}).Decode(&user); err != nil {
		return
	}
	random := make([]byte, 10)
	if _, err = rand.Read(random); err != nil {
		return
	}
	secret := base32.StdEncoding.EncodeToString(random)
	var u *url.URL
	if u, err = url.Parse("otpauth://totp"); err != nil {
		return
	}
	u.Path += "/" + url.PathEscape(x.V.Namespace) + ":" + url.PathEscape(user.Email)
	params := url.Values{}
	params.Add("secret", secret)
	params.Add("issuer", x.V.Namespace)
	u.RawQuery = params.Encode()
	totp = u.String()

	if err = x.RDb.Set(ctx, totp, secret, time.Minute*5).Err(); err != nil {
		return
	}

	return
}

func (x *Service) SetUserTotp(ctx context.Context, userId string, totp string, tss [2]string) (r interface{}, err error) {
	if tss[0] == tss[1] {
		return "", common.ErrTOTPInvalid
	}
	var secret string
	if secret, err = x.RDb.Get(ctx, totp).Result(); err != nil {
		return
	}
	otpc := &dgoogauth.OTPConfig{
		Secret:      secret,
		WindowSize:  2,
		HotpCounter: 0,
		UTC:         true,
	}
	for _, v := range tss {
		var check bool
		if check, err = otpc.Authenticate(v); err != nil {
			return
		}
		if !check {
			return "", common.ErrTOTPInvalid
		}
	}

	if err = x.RDb.Del(ctx, totp).Err(); err != nil {
		return
	}

	return x.SetUser(ctx, userId, bson.M{
		"$set": bson.M{
			"totp": secret,
		},
	})
}
