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

type AdminUserBanLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAdminUserBanLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AdminUserBanLogic {
	return &AdminUserBanLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// AdminUserBan 后台封禁/解封用户
//   - 1=解封 2=封禁（req.Status 已由 go-zero 校验 oneof=1 2）
//   - 封禁后该用户无法登录（C 端 Login 校验 status==2 → ErrUserBanned）
//   - 已有 post/shop 的可见性暂不变（依赖 post.status / shop.status 控制展示）
func (l *AdminUserBanLogic) AdminUserBan(req *types.AdminUserBanRequest) (resp *types.EmptyResponse, err error) {
	// 1. 查询用户
	user, err := l.svcCtx.UserModel.FindOne(l.ctx, uint64(req.Id))
	if err != nil {
		if errors.Is(err, sqlc.ErrNotFound) || errors.Is(err, model.ErrNotFound) {
			l.Infof("封禁失败: 用户不存在, id=%d", req.Id)
			return nil, errorx.ErrNotFound
		}
		l.Errorf("查询用户失败: id=%d, err=%v", req.Id, err)
		return nil, errorx.ErrInternal
	}

	// 2. 幂等保护：状态已经是目标值 → 直接返回
	if int64(req.Status) == user.Status {
		l.Infof("用户状态无需变更: id=%d, status=%d", user.Id, user.Status)
		return &types.EmptyResponse{}, nil
	}

	// 3. 更新状态
	user.Status = int64(req.Status)
	if err := l.svcCtx.UserModel.Update(l.ctx, user); err != nil {
		l.Errorf("更新用户状态失败: id=%d, status=%d, err=%v", user.Id, user.Status, err)
		return nil, errorx.ErrInternal
	}

	action := "解封"
	if req.Status == 2 {
		action = "封禁"
	}
	l.Infof("%s用户成功: id=%d, openid=%s", action, user.Id, user.Openid)
	return &types.EmptyResponse{}, nil
}
