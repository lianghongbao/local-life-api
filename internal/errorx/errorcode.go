// Package errorx 定义项目级业务错误码。
// 放在 internal/errorx 而非 common/errorx，
// 因为这些错误码含本项目的业务语义（Post/Shop/Notice 等），不属于通用技术层。
package errorx

import "github.com/zeromicro/x/errors"

var (
	// 通用错误 1000-1099
	ErrInvalidParam = errors.New(1001, "参数错误")
	ErrUnauthorized = errors.New(1002, "未登录")
	ErrForbidden    = errors.New(1003, "无权限")
	ErrNotFound     = errors.New(1004, "资源不存在")
	ErrInternal     = errors.New(1005, "服务器内部错误")

	// 用户错误 2000-2099
	ErrWxLoginFailed = errors.New(2001, "微信登录失败")
	ErrUserBanned    = errors.New(2002, "账号已被封禁")

	// 业务错误 3000-3099
	ErrPostNotFound   = errors.New(3001, "信息不存在")
	ErrPostAuditing   = errors.New(3002, "信息审核中")
	ErrShopNotFound   = errors.New(3003, "商家不存在")
	ErrNoticeNotFound = errors.New(3004, "公告不存在")

	// 管理后台 4000-4099
	ErrAdminNotFound = errors.New(4001, "管理员不存在")
	ErrPasswordWrong = errors.New(4002, "用户名或密码错误")
	ErrAdminDisabled = errors.New(4003, "账号已禁用")
)
