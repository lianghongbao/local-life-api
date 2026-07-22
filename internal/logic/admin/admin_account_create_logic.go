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

type AdminAccountCreateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAdminAccountCreateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AdminAccountCreateLogic {
	return &AdminAccountCreateLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// AdminAccountCreate 创建后台账号
//   - 1. 校验 username 唯一(走 AdminModel.FindOneByUsername 自带缓存)
//   - 2. bcrypt 加密 password(cost=10)
//   - 3. INSERT admin(role/status 默认 1)
//   - username 重名返回 ErrInvalidParam(1001),避免泄露"账号是否存在"
func (l *AdminAccountCreateLogic) AdminAccountCreate(req *types.AdminAccountCreateReq) (resp *types.EmptyResponse, err error) {
	// 1. 唯一性预检
	if _, err := l.svcCtx.AdminModel.FindOneByUsername(l.ctx, req.Username); err == nil {
		l.Infof("创建失败: username 已存在, username=%s", req.Username)
		return nil, errorx.ErrInvalidParam
	} else if !errors.Is(err, sqlc.ErrNotFound) && !errors.Is(err, model.ErrNotFound) {
		l.Errorf("查询 username 失败: %s, err=%v", req.Username, err)
		return nil, errorx.ErrInternal
	}

	// 2. bcrypt 加密
	hash, err := bcryptx.Hash(req.Password)
	if err != nil {
		l.Errorf("bcrypt 加密失败: err=%v", err)
		return nil, errorx.ErrInternal
	}

	// 3. INSERT
	newAdmin := &model.Admin{
		Username: req.Username,
		Password: hash,
		Role:     int64(req.Role),
		Status:   int64(req.Status),
	}
	res, err := l.svcCtx.AdminModel.Insert(l.ctx, newAdmin)
	if err != nil {
		l.Errorf("创建 admin 失败: username=%s, err=%v", req.Username, err)
		return nil, errorx.ErrInternal
	}
	id, _ := res.LastInsertId()
	newAdmin.Id = uint64(id)

	l.Infof("创建 admin 账号成功: id=%d, username=%s, role=%d, status=%d",
		newAdmin.Id, req.Username, req.Role, req.Status)
	return &types.EmptyResponse{}, nil
}
