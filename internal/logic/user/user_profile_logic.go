package user

import (
	"context"
	"errors"

	"local-life-api/common/jwtx"
	"local-life-api/internal/errorx"
	"local-life-api/internal/model"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type UserProfileLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewUserProfileLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UserProfileLogic {
	return &UserProfileLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// UserProfile 获取当前登录用户的资料
//
//  1. 从 JWT 取 openid(业务标识符)
//  2. 查 user 表
//  3. 把 user.Avatar (file_key) 转成完整 URL(永久有效, 不用签名)
//  4. 返回 UserProfileResponse
func (l *UserProfileLogic) UserProfile() (resp *types.UserProfileResponse, err error) {
	// 1. JWT 取 openid(业务标识符)
	openid, err := jwtx.GetOpenid(l.ctx)
	if err != nil {
		l.Errorf("JWT 解析失败: %v", err)
		return nil, errorx.ErrUnauthorized
	}

	// 2. 用 openid 查 user(命中 cache:user:openid:{openid})
	user, err := l.svcCtx.UserModel.FindOneByOpenid(l.ctx, openid)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil, errorx.ErrUnauthorized
		}
		l.Errorf("查询用户失败: openid=%s, err=%v", openid, err)
		return nil, errorx.ErrInternal
	}

	// 3. 把头像 file_key 转成完整 URL(永久有效，无需签名)
	avatarURL := user.Avatar
	if avatarURL != "" {
		avatarURL = l.svcCtx.OSS.Client.URL(avatarURL)
	}

	return &types.UserProfileResponse{
		Id:            int64(user.Id),
		Nickname:      user.Nickname,
		Avatar:        avatarURL,
		AvatarFileKey: user.Avatar,
		Phone:         user.Phone,
		Status:        int(user.Status),
		CreatedAt:     user.CreatedAt.Format("2006-01-02 15:04:05"),
	}, nil
}
