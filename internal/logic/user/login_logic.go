package user

import (
	"context"
	"errors"

	"local-life-api/common/jwtx"
	"local-life-api/common/wx"
	"local-life-api/internal/errorx"
	"local-life-api/internal/model"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/sqlc"
)

type LoginLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewLoginLogic(ctx context.Context, svcCtx *svc.ServiceContext) *LoginLogic {
	return &LoginLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// Login 微信小程序登录
//   - 1. code 换 openid + session_key
//   - 2. 查 user 表；不存在则创建
//   - 3. 封禁校验
//   - 4. 签 JWT 返回
func (l *LoginLogic) Login(req *types.LoginRequest) (resp *types.LoginResponse, err error) {
	// 1. 调微信接口换 openid
	session, err := wx.Code2Session(
		l.ctx,
		l.svcCtx.Config.WxMini.AppID,
		l.svcCtx.Config.WxMini.AppSecret,
		req.Code,
	)
	if err != nil {
		// 微信侧错误 → 服务端记日志，前端返通用错误
		l.Errorf("微信 code2Session 失败: %v", err)
		return nil, errorx.ErrWxLoginFailed
	}
	openid := session.OpenID

	// 2. 查 user 表
	user, err := l.svcCtx.UserModel.FindOneByOpenid(l.ctx, openid)
	if err != nil {
		// 查询失败
		if !errors.Is(err, sqlc.ErrNotFound) {
			l.Errorf("查询用户失败: openid=%s, err: %v", openid, err)
			return nil, errorx.ErrInternal
		}

		// 3. 用户不存在 → 创建新用户
		newUser := &model.User{
			Openid:   openid,
			Nickname: "", // 首次登录昵称为空，前端引导用户补充
			Avatar:   "",
			Phone:    "",
			Status:   1, // 1正常
		}
		insertRes, insertErr := l.svcCtx.UserModel.Insert(l.ctx, newUser)
		if insertErr != nil {
			l.Errorf("创建用户失败: openid=%s, err: %v", openid, insertErr)
			return nil, errorx.ErrInternal
		}
		// 拿到自增 ID
		id, idErr := insertRes.LastInsertId()
		if idErr != nil {
			l.Errorf("获取用户ID失败: err=%v", idErr)
			return nil, errorx.ErrInternal
		}
		newUser.Id = uint64(id)
		user = newUser
		l.Infof("新用户注册成功: id=%d, openid=%s", user.Id, openid)
	}

	// 4. 封禁校验（status: 1正常 2封禁）
	if user.Status == 2 {
		l.Infof("登录被拒: 用户已封禁, id=%d", user.Id)
		return nil, errorx.ErrUserBanned
	}

	// 5. 签 JWT（用 C 端的 Auth 配置；主载荷是 openid，业务标识符）
	token, err := jwtx.GenerateAccessToken(
		l.svcCtx.Config.Auth.AccessSecret,
		user.Openid,    // 主载荷:openid(业务标识符)
		int64(user.Id), // 兼容字段:admin 后台用
		"user",         // role 标识
		l.svcCtx.Config.Auth.AccessExpire,
	)
	if err != nil {
		l.Errorf("签发 JWT 失败: id=%d, err: %v", user.Id, err)
		return nil, errorx.ErrInternal
	}

	l.Infof("登录成功: id=%d, openid=%s", user.Id, user.Openid)
	return &types.LoginResponse{
		Token:   token,
		UserId:  int64(user.Id),
		Openid:  user.Openid,
		Unionid: "", // 当前 user 表无 unionid 列,登录响应保留字段便于排查时统一使用
	}, nil
}
