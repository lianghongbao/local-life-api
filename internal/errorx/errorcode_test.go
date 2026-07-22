package errorx

import (
	"errors"
	"testing"

	xerrors "github.com/zeromicro/x/errors"
)

// extract 提取 xerrors.CodeMsg 的 code + msg
func extract(err error) (int, string) {
	if err == nil {
		return 0, ""
	}
	var cm *xerrors.CodeMsg
	if errors.As(err, &cm) {
		return cm.Code, cm.Msg
	}
	// 兜底:有些 error 是字符串
	return 0, err.Error()
}

// TestErrorCodes_AllPresent 验证项目关键错误码都存在 + 文本非空
//   - 防止以后有人误删/重命名 errorx 包变量,导致 admin/小程序前端 code 判断失效
func TestErrorCodes_AllPresent(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		// 通用
		{"ErrInvalidParam", ErrInvalidParam},
		{"ErrUnauthorized", ErrUnauthorized},
		{"ErrForbidden", ErrForbidden},
		{"ErrNotFound", ErrNotFound},
		{"ErrInternal", ErrInternal},
		// 用户
		{"ErrWxLoginFailed", ErrWxLoginFailed},
		{"ErrUserBanned", ErrUserBanned},
		// 业务
		{"ErrPostNotFound", ErrPostNotFound},
		{"ErrPostAuditing", ErrPostAuditing},
		{"ErrShopNotFound", ErrShopNotFound},
		{"ErrNoticeNotFound", ErrNoticeNotFound},
		// 管理后台
		{"ErrAdminNotFound", ErrAdminNotFound},
		{"ErrPasswordWrong", ErrPasswordWrong},
		{"ErrAdminDisabled", ErrAdminDisabled},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err == nil {
				t.Fatalf("%s is nil", tc.name)
			}
			code, msg := extract(tc.err)
			if code == 0 {
				t.Fatalf("%s has code 0 (should be non-zero for client error mapping)", tc.name)
			}
			if msg == "" {
				t.Fatalf("%s has empty message", tc.name)
			}
		})
	}
}

// TestErrorCodes_NoCollision 验证错误码没有重复
func TestErrorCodes_NoCollision(t *testing.T) {
	all := []error{
		ErrInvalidParam, ErrUnauthorized, ErrForbidden, ErrNotFound, ErrInternal,
		ErrWxLoginFailed, ErrUserBanned,
		ErrPostNotFound, ErrPostAuditing, ErrShopNotFound, ErrNoticeNotFound,
		ErrAdminNotFound, ErrPasswordWrong, ErrAdminDisabled,
	}
	seen := make(map[int]string)
	for _, e := range all {
		code, msg := extract(e)
		if existing, ok := seen[code]; ok {
			t.Errorf("code %d collision: %q and %q", code, existing, msg)
		}
		seen[code] = msg
	}
}

// TestErrorCodes_CodeRanges 验证错误码符合 CLAUDE.md 约定的分段
//   - 通用 1000-1099
//   - 用户 2000-2099
//   - 业务 3000-3099
//   - 管理后台 4000-4099
//   - 这条规则是 admin/小程序前端按 code 走分支的依据,不能混
func TestErrorCodes_CodeRanges(t *testing.T) {
	cases := []struct {
		err  error
		min  int
		max  int
		name string
	}{
		{ErrInvalidParam, 1000, 1099, "ErrInvalidParam"},
		{ErrUnauthorized, 1000, 1099, "ErrUnauthorized"},
		{ErrForbidden, 1000, 1099, "ErrForbidden"},
		{ErrNotFound, 1000, 1099, "ErrNotFound"},
		{ErrInternal, 1000, 1099, "ErrInternal"},
		{ErrWxLoginFailed, 2000, 2099, "ErrWxLoginFailed"},
		{ErrUserBanned, 2000, 2099, "ErrUserBanned"},
		{ErrPostNotFound, 3000, 3099, "ErrPostNotFound"},
		{ErrPostAuditing, 3000, 3099, "ErrPostAuditing"},
		{ErrShopNotFound, 3000, 3099, "ErrShopNotFound"},
		{ErrNoticeNotFound, 3000, 3099, "ErrNoticeNotFound"},
		{ErrAdminNotFound, 4000, 4099, "ErrAdminNotFound"},
		{ErrPasswordWrong, 4000, 4099, "ErrPasswordWrong"},
		{ErrAdminDisabled, 4000, 4099, "ErrAdminDisabled"},
	}
	for _, tc := range cases {
		code, _ := extract(tc.err)
		if code < tc.min || code > tc.max {
			t.Errorf("%s code=%d out of range [%d,%d]", tc.name, code, tc.min, tc.max)
		}
	}
}
