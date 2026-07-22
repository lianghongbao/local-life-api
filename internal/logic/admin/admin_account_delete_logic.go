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

type AdminAccountDeleteLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAdminAccountDeleteLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AdminAccountDeleteLogic {
	return &AdminAccountDeleteLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// AdminAccountDelete 删除后台账号
//   - 自我保护:不允许删除自己
//   - 兜底保护:全表只剩 1 个启用中的超管时,不允许删除(避免全员失联)
func (l *AdminAccountDeleteLogic) AdminAccountDelete(req *types.IdRequest) (resp *types.EmptyResponse, err error) {
	// 1. 查询目标
	target, err := l.svcCtx.AdminModel.FindOne(l.ctx, uint64(req.Id))
	if err != nil {
		if errors.Is(err, sqlc.ErrNotFound) || errors.Is(err, model.ErrNotFound) {
			l.Infof("删除失败: admin 不存在, id=%d", req.Id)
			return nil, errorx.ErrAdminNotFound
		}
		l.Errorf("查询 admin 失败: id=%d, err=%v", req.Id, err)
		return nil, errorx.ErrInternal
	}

	// 2. 自我保护:不能删除自己
	currentUser, jwtErr := jwtx.FromContext(l.ctx)
	if jwtErr == nil && currentUser.Username == target.Username {
		l.Infof("删除被拒: 不能删除自己的账号, id=%d, username=%s", target.Id, target.Username)
		return nil, errorx.ErrForbidden
	}

	// 3. 兜底保护:目标若是最后 1 个启用的超管,不允许删
	if target.Role == 1 && target.Status == 1 {
		var enabledSuperAdminCount int64
		countSQL := "SELECT COUNT(*) FROM `admin` WHERE `role` = 1 AND `status` = 1"
		if err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &enabledSuperAdminCount, countSQL); err != nil {
			l.Errorf("统计超管数量失败: err=%v", err)
			return nil, errorx.ErrInternal
		}
		if enabledSuperAdminCount <= 1 {
			l.Infof("删除被拒: 仅剩最后 1 个启用的超管, id=%d", target.Id)
			return nil, errorx.ErrForbidden
		}
	}

	// 4. 删除(model.Delete 自带缓存清理)
	if err := l.svcCtx.AdminModel.Delete(l.ctx, uint64(req.Id)); err != nil {
		if errors.Is(err, sqlc.ErrNotFound) || errors.Is(err, model.ErrNotFound) {
			// 并发删除:别人已删
			l.Infof("删除跳过: admin 已被其他人删除, id=%d", req.Id)
			return &types.EmptyResponse{}, nil
		}
		l.Errorf("删除 admin 失败: id=%d, err=%v", req.Id, err)
		return nil, errorx.ErrInternal
	}

	l.Infof("删除 admin 成功: id=%d, username=%s", target.Id, target.Username)
	return &types.EmptyResponse{}, nil
}
