package admin

import (
	"context"
	"errors"

	"local-life-api/internal/errorx"
	"local-life-api/internal/model"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/sqlc"
)

type AdminUserDetailLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAdminUserDetailLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AdminUserDetailLogic {
	return &AdminUserDetailLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// AdminUserDetail 后台查看用户详情
//   - 单条查询 user 表
//   - 返回 openid/手机号/状态/创建/更新时间
//   - 不缓存（后台需要实时）
func (l *AdminUserDetailLogic) AdminUserDetail(req *types.IdRequest) (resp *types.AdminUserDetailResponse, err error) {
	user, err := l.svcCtx.UserModel.FindOne(l.ctx, uint64(req.Id))
	if err != nil {
		if errors.Is(err, sqlc.ErrNotFound) || errors.Is(err, model.ErrNotFound) {
			l.Infof("用户不存在: id=%d", req.Id)
			return nil, errorx.ErrNotFound
		}
		l.Errorf("查询用户失败: id=%d, err=%v", req.Id, err)
		return nil, errorx.ErrInternal
	}

	// 签名 Avatar URL: file_key → URL(永久有效)
	avatarURL := user.Avatar
	if user.Avatar != "" {
		avatarURL = l.svcCtx.OSS.Client.URL(user.Avatar)
	}

	return &types.AdminUserDetailResponse{
		Id:        int64(user.Id),
		Openid:    user.Openid,
		Nickname:  user.Nickname,
		Avatar:    avatarURL,
		Phone:     user.Phone,
		Status:    int(user.Status),
		CreatedAt: user.CreatedAt.Format("2006-01-02 15:04:05"),
		UpdatedAt: user.UpdatedAt.Format("2006-01-02 15:04:05"),
	}, nil
}
