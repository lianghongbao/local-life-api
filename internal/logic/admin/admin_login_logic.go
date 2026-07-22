package admin

import (
	"context"
	"errors"

	"local-life-api/common/bcryptx"
	"local-life-api/common/jwtx"
	"local-life-api/internal/errorx"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/sqlc"
)

type AdminLoginLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAdminLoginLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AdminLoginLogic {
	return &AdminLoginLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// AdminLogin 管理员账号密码登录
//   - 1. 按 username 查 admin 表
//   - 2. bcrypt 校验密码
//   - 3. 状态校验（status: 1正常 2禁用）
//   - 4. 签 JWT 返回（走 AdminAuth 配置，role=admin）
func (l *AdminLoginLogic) AdminLogin(req *types.AdminLoginRequest) (resp *types.AdminLoginResponse, err error) {
	// 1. 按 username 查询管理员
	admin, err := l.svcCtx.AdminModel.FindOneByUsername(l.ctx, req.Username)
	if err != nil {
		if errors.Is(err, sqlc.ErrNotFound) {
			// 用户名不存在也返回“密码错误”，避免泄露账号是否存在
			l.Infof("登录被拒: 管理员不存在, username=%s", req.Username)
			return nil, errorx.ErrPasswordWrong
		}
		l.Errorf("查询管理员失败: username=%s, err: %v", req.Username, err)
		return nil, errorx.ErrInternal
	}

	// 2. bcrypt 校验密码
	if verifyErr := bcryptx.Verify(admin.Password, req.Password); verifyErr != nil {
		l.Infof("登录被拒: 密码错误, id=%d, username=%s", admin.Id, admin.Username)
		return nil, errorx.ErrPasswordWrong
	}

	// 3. 状态校验（status: 1正常 2禁用）
	if admin.Status == 2 {
		l.Infof("登录被拒: 账号已禁用, id=%d, username=%s", admin.Id, admin.Username)
		return nil, errorx.ErrAdminDisabled
	}

	// 4. 签 JWT（走 AdminAuth 配置；admin 端主载荷=userId+username，不写 openid 字段）
	token, err := jwtx.GenerateAdminAccessToken(
		l.svcCtx.Config.AdminAuth.AccessSecret,
		int64(admin.Id), // userId = admin.id
		admin.Username,  // username（业务标识符）
		"admin",         // role 标识
		l.svcCtx.Config.AdminAuth.AccessExpire,
	)
	if err != nil {
		l.Errorf("签发 JWT 失败: id=%d, err: %v", admin.Id, err)
		return nil, errorx.ErrInternal
	}

	l.Infof("管理员登录成功: id=%d, username=%s", admin.Id, admin.Username)
	return &types.AdminLoginResponse{
		Token: token,
	}, nil
}
