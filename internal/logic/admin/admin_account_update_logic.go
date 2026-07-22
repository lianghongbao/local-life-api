package admin

import (
	"context"
	"errors"

	"local-life-api/common/jwtx"
	"local-life-api/internal/errorx"
	"local-life-api/internal/model"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/sqlc"
)

type AdminAccountUpdateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAdminAccountUpdateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AdminAccountUpdateLogic {
	return &AdminAccountUpdateLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// AdminAccountUpdate 更新账号的 role / status
//   - username / password 不允许在此接口修改(防破坏审计)
//   - 保护:不能把自己禁用(status=2),避免误操作锁死账号
func (l *AdminAccountUpdateLogic) AdminAccountUpdate(req *types.AdminAccountUpdateReq) (resp *types.EmptyResponse, err error) {
	// 1. 查询
	target, err := l.svcCtx.AdminModel.FindOne(l.ctx, uint64(req.Id))
	if err != nil {
		if errors.Is(err, sqlc.ErrNotFound) || errors.Is(err, model.ErrNotFound) {
			l.Infof("更新失败: admin 不存在, id=%d", req.Id)
			return nil, errorx.ErrAdminNotFound
		}
		l.Errorf("查询 admin 失败: id=%d, err=%v", req.Id, err)
		return nil, errorx.ErrInternal
	}

	// 2. 自我保护:不允许把自己禁用
	if req.Status == 2 {
		currentUser, jwtErr := jwtx.FromContext(l.ctx)
		if jwtErr == nil && currentUser.Username == target.Username {
			l.Infof("更新被拒: 不允许禁用自己的账号, id=%d, username=%s", target.Id, target.Username)
			return nil, errorx.ErrForbidden
		}
	}

	// 3. 幂等
	if target.Role == int64(req.Role) && target.Status == int64(req.Status) {
		l.Infof("admin 状态无需变更: id=%d, role=%d, status=%d", target.Id, target.Role, target.Status)
		return &types.EmptyResponse{}, nil
	}

	// 4. 写库
	target.Role = int64(req.Role)
	target.Status = int64(req.Status)
	if err := l.svcCtx.AdminModel.Update(l.ctx, target); err != nil {
		l.Errorf("更新 admin 失败: id=%d, err=%v", target.Id, err)
		return nil, errorx.ErrInternal
	}

	l.Infof("更新 admin 成功: id=%d, role=%d, status=%d", target.Id, target.Role, target.Status)
	return &types.EmptyResponse{}, nil
}
