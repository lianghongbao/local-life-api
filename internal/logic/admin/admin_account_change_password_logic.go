package admin

import (
	"context"
	"errors"

	"local-life-api/common/bcryptx"
	"local-life-api/internal/errorx"
	"local-life-api/internal/model"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/sqlc"
)

type AdminAccountChangePasswordLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAdminAccountChangePasswordLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AdminAccountChangePasswordLogic {
	return &AdminAccountChangePasswordLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// AdminAccountChangePassword 重置/修改账号密码
//   - 不需要传旧密码(超管场景下就是"重置")
//   - 强制 min=8(types 已校验)
//   - bcrypt 加密后整行 Update
func (l *AdminAccountChangePasswordLogic) AdminAccountChangePassword(req *types.AdminAccountChangePasswordReq) (resp *types.EmptyResponse, err error) {
	// 1. 查询
	target, err := l.svcCtx.AdminModel.FindOne(l.ctx, uint64(req.Id))
	if err != nil {
		if errors.Is(err, sqlc.ErrNotFound) || errors.Is(err, model.ErrNotFound) {
			l.Infof("改密失败: admin 不存在, id=%d", req.Id)
			return nil, errorx.ErrAdminNotFound
		}
		l.Errorf("查询 admin 失败: id=%d, err=%v", req.Id, err)
		return nil, errorx.ErrInternal
	}

	// 2. bcrypt 加密新密码
	hash, err := bcryptx.Hash(req.Password)
	if err != nil {
		l.Errorf("bcrypt 加密失败: id=%d, err=%v", target.Id, err)
		return nil, errorx.ErrInternal
	}

	// 3. 写库(全字段覆盖,Update 方法要求 row 完整)
	target.Password = hash
	if err := l.svcCtx.AdminModel.Update(l.ctx, target); err != nil {
		l.Errorf("更新 admin 密码失败: id=%d, err=%v", target.Id, err)
		return nil, errorx.ErrInternal
	}

	l.Infof("重置 admin 密码成功: id=%d, username=%s", target.Id, target.Username)
	return &types.EmptyResponse{}, nil
}
