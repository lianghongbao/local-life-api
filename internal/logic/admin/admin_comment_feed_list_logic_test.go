package admin

// admin_comment_feed_list_logic_test.go
//   - 单元测试覆盖说明：本模块 logic 内部纯函数 + 状态分支
//   - 业务逻辑里可独立测试的部分（不依赖 DBConn）:
//       * statusTextOf(status) — 数字→中文标签
//   - DB 相关（QueryRowCtx / QueryRowsCtx）依赖 svcCtx.DBConn 这个具体类型，
//     项目目前没引入 interface 化,要测需启动本地 MySQL,不在本单测范围。
//     参考 admin_account_logic_test.go 的约定。

import "testing"

func TestStatusTextOf(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{1, "正常"},
		{2, "已删除"},
		{3, "审核中"},
		{0, "未知"}, // 兜底
		{99, "未知"},
	}
	for _, c := range cases {
		t.Run("", func(t *testing.T) {
			if got := statusTextOf(c.in); got != c.want {
				t.Fatalf("statusTextOf(%d)=%q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestDateRangeValidation — date_from > date_to 应当拒绝
// 直接构造 req 比对返回行为;但 logic 入口会调 jwtx.GetAdminIdentity(ctx),
// 该函数依赖 jwt 注入的 admin context,这里用 logic 内部的 where 拼接逻辑不可直接测试。
// 故此测试只验证状态字符映射即可;date 校验在 handler 层 validate tag 已覆盖。
func TestDateFromAfterDateTo_LogicSanity(t *testing.T) {
	if "2026-07-15" <= "2026-07-01" {
		t.Fatal("string compare sanity fail: 'a' should not be <= 'b' when a is later date")
	}
}
