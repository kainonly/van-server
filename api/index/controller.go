package index

import (
	"context"
	"fmt"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/hlog"
	"github.com/huandu/xstrings"
	"github.com/thoas/go-funk"
	"github.com/weplanx/go/csrf"
	"github.com/weplanx/go/passlib"
	"github.com/weplanx/server/common"
	"github.com/weplanx/server/model"
	"go.mongodb.org/mongo-driver/bson"
	"net/http"
	"reflect"
	"time"
)

type Controller struct {
	V    *common.Values
	Csrf *csrf.Csrf

	IndexService *Service
}

func (x *Controller) Ping(_ context.Context, c *app.RequestContext) {
	if !x.V.IsRelease() {
		c.JSON(http.StatusOK, M{
			"extra": x.V.Extra,
		})
		return
	}

	c.JSON(http.StatusOK, M{
		"ip":   c.ClientIP(),
		"time": time.Now(),
	})
}

func (x *Controller) MockErr(_ context.Context, c *app.RequestContext) {
	c.JSON(http.StatusBadRequest, M{
		"code": 1000,
		"msg":  "mock err",
	})
}

//func (x *Controller) R

type LoginDto struct {
	Email    string `json:"email,required" vd:"email($)"`
	Password string `json:"password,required" vd:"len($)>=8"`
}

func (x *Controller) Login(ctx context.Context, c *app.RequestContext) {
	var dto LoginDto
	if err := c.BindAndValidate(&dto); err != nil {
		c.Error(err)
		return
	}

	r, err := x.IndexService.Login(ctx, dto.Email, dto.Password)
	if err != nil {
		c.Error(err)
		return
	}

	go func() {
		data := model.NewLogsetLogined(
			r.User.ID, string(c.GetHeader("X-Forwarded-For")), "email", string(c.UserAgent()))
		wctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
		defer cancel()
		if err = x.IndexService.WriteLogsetLogined(wctx, data); err != nil {
			hlog.Fatal(err)
		}
	}()

	common.SetAccessToken(c, r.AccessToken)
	c.JSON(200, M{
		"code":    0,
		"message": "ok",
	})
}

func (x *Controller) Verify(ctx context.Context, c *app.RequestContext) {
	ts := c.Cookie("access_token")
	if ts == nil {
		c.JSON(401, M{
			"code":    0,
			"message": common.ErrAuthenticationExpired.Error(),
		})
		return
	}

	if _, err := x.IndexService.Verify(ctx, string(ts)); err != nil {
		common.ClearAccessToken(c)
		c.JSON(401, M{
			"code":    0,
			"message": common.ErrAuthenticationExpired.Error(),
		})
		return
	}

	c.JSON(200, M{
		"code":    0,
		"message": "ok",
	})
}

func (x *Controller) GetRefreshCode(ctx context.Context, c *app.RequestContext) {
	claims := common.Claims(c)
	code, err := x.IndexService.GetRefreshCode(ctx, claims.UserId)
	if err != nil {
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, M{
		"code": code,
	})
}

type RefreshTokenDto struct {
	Code string `json:"code,required"`
}

func (x *Controller) RefreshToken(ctx context.Context, c *app.RequestContext) {
	var dto RefreshTokenDto
	if err := c.BindAndValidate(&dto); err != nil {
		c.Error(err)
		return
	}

	claims := common.Claims(c)
	ts, err := x.IndexService.RefreshToken(ctx, claims, dto.Code)
	if err != nil {
		c.Error(err)
		return
	}

	common.SetAccessToken(c, ts)
	c.JSON(http.StatusOK, M{
		"code":    0,
		"message": "ok",
	})
}

func (x *Controller) Logout(ctx context.Context, c *app.RequestContext) {
	claims := common.Claims(c)
	x.IndexService.Logout(ctx, claims.UserId)
	common.ClearAccessToken(c)
	c.JSON(http.StatusOK, M{
		"code":    0,
		"message": "ok",
	})
}

func (x *Controller) GetUser(ctx context.Context, c *app.RequestContext) {
	claims := common.Claims(c)
	data, err := x.IndexService.GetUser(ctx, claims.UserId)
	if err != nil {
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, data)
}

type SetUserDto struct {
	Set         string `json:"$set,required" vd:"in($, 'email', 'name', 'avatar', 'password', 'backup_email')"`
	Email       string `json:"email,omitempty" vd:"(Set)$!='Email' || email($);msg:'must be email'"`
	Name        string `json:"name,omitempty"`
	BackupEmail string `json:"backup_email,omitempty" vd:"(Set)$!='BackupEmail' || email($);msg:'must be email'"`
	Avatar      string `json:"avatar,omitempty"`
	Password    string `json:"password,omitempty" vd:"(Set)$!='Password' || len($)>8;msg:'must be greater than 8 characters'"`
}

func (x *Controller) SetUser(ctx context.Context, c *app.RequestContext) {
	var dto SetUserDto
	if err := c.BindAndValidate(&dto); err != nil {
		c.Error(err)
		return
	}
	data := make(map[string]interface{})
	path := xstrings.ToCamelCase(dto.Set)
	value := reflect.ValueOf(dto).FieldByName(path).Interface()
	if dto.Set == "password" {
		data[dto.Set], _ = passlib.Hash(value.(string))
	} else {
		data[dto.Set] = value
	}

	claims := common.Claims(c)
	_, err := x.IndexService.SetUser(ctx, claims.UserId, bson.M{
		"$set": data,
	})
	if err != nil {
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, M{
		"code":    0,
		"message": "ok",
	})
}

type UnsetUserDto struct {
	Key string `path:"key,required" vd:"in($, 'lark')"`
}

func (x *Controller) UnsetUser(ctx context.Context, c *app.RequestContext) {
	var dto UnsetUserDto
	if err := c.BindAndValidate(&dto); err != nil {
		c.Error(err)
		return
	}

	claims := common.Claims(c)
	_, err := x.IndexService.SetUser(ctx, claims.UserId, bson.M{
		"$unset": bson.M{dto.Key: 1},
	})
	if err != nil {
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, M{
		"code":    0,
		"message": "ok",
	})
}

type OptionsDto struct {
	Type string `query:"type"`
}

func (x *Controller) Options(ctx context.Context, c *app.RequestContext) {
	var dto OptionsDto
	if err := c.BindAndValidate(&dto); err != nil {
		c.Error(err)
		return
	}
	switch dto.Type {
	case "upload":
		switch x.V.Cloud {
		case "tencent":
			c.JSON(http.StatusOK, M{
				"type": "cos",
				"url": fmt.Sprintf(`https://%s.cos.%s.myqcloud.com`,
					x.V.TencentCosBucket, x.V.TencentCosRegion,
				),
				"limit": x.V.TencentCosLimit,
			})
			return
		}
	case "collaboration":
		c.JSON(http.StatusOK, M{
			"url":      "https://open.feishu.cn/open-apis/authen/v1/index",
			"redirect": x.V.RedirectUrl,
			"app_id":   x.V.LarkAppId,
		})
		return
	case "generate-secret":
		c.JSON(http.StatusOK, M{
			"id":  funk.RandomString(8),
			"key": funk.RandomString(16),
		})
		return
	}
	return
}
