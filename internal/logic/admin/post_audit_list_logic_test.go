package admin

// ============================================================
// post_audit_list SQL 防 NULL Scan 回归测试
// ============================================================
// 背景:
//   2026-07-15 本地 dev 库返 1005: "sql: Scan error on column index 2,
//   name \"nickname\": converting NULL to string is unsupported"
//   根因: post_info LEFT JOIN user,user.nickname 可能为 NULL(删号 /
//   早期没填昵称的测试数据)。SQL 没 COALESCE,postRow.Nickname 又用裸
//   string 接收,database/sql Scan 见到 NULL 直接报错。
//
// 测试策略:
//   抽 buildPostAuditListSQL() 纯函数,断言它对所有"来自 user 表的
//   可空 string 列"都用了 COALESCE(col, '')。这样:
//     - 改前:测试红
//     - 改后:测试绿
//     - 以后任何重写 SQL 的人,都会因为这个测试挂掉而被提醒加 COALESCE
//
// 不测什么:
//   - 不起 MySQL、不 mock DBConn:同 admin_account_logic_test.go 风格,
//     "Logic 层强依赖 goctl 生成的 *customAdminModel,无法用普通 mock 替换"。
//   - 不测 LEFT JOIN 整体语义:那是集成测试范畴。
//
// 集成测试建议(未实施):
//   启动本地 MySQL,灌一条 nickname=NULL 的 post + 一个正常 user,
//   调 PostAuditList(req),期望 List 不为空且不返回 1005。

import (
	"strings"
	"testing"
)

// TestBuildPostAuditListSQL_NullableUserColumnsCoalesced
// 列出 user 表所有参与 SELECT 的 string 列(均为可空),逐个断言 SQL 里
// 出现的是 COALESCE(u.col, '') 而不是裸的 u.col。
func TestBuildPostAuditListSQL_NullableUserColumnsCoalesced(t *testing.T) {
	sql := buildPostAuditListSQL()

	// 所有 LEFT JOIN user 来源的 string 列,都应在 SQL 里以 COALESCE 形式出现
	nullableUserCols := []string{"nickname", "avatar"}

	for _, col := range nullableUserCols {
		// 必须出现 "COALESCE(u.<col>" 这种片段
		needle := "COALESCE(u." + col
		if !strings.Contains(sql, needle) {
			t.Errorf("SQL 没对 LEFT JOIN user 的可空 string 列 %q 加 COALESCE,"+
				"会导致 user.nickname/avatar 为 NULL 时 Scan 失败(本地 1005 bug)。"+
				"\n  期望包含片段: %q"+
				"\n  实际 SQL:\n%s", col, needle, sql)
		}

		// 反向断言:不要同时存在裸的 "u.<col>" 别名(否则 COALESCE 包了等于没包)
		// 简单做法:确保 "COALESCE(u.<col>" 出现后,不再有独立的 ", u.<col>,"
		// 这里只提示开发者重读 SQL,不做强校验避免脆弱。
	}
}

// TestBuildPostAuditListSQL_PostInfoNullableStringColumnsCoalesced
// post_info 表自身的可空 string 列也要 COALESCE(用户没填/清空)。
// 当前 schema 上线后允许 NULL 的 string 列:
//   - reject_reason(status=3 时才填,但 schema 允许 NULL)
//   - contact / area / area_name / street_name / city_name
//   - area_adcode / city_adcode(定位后才有,但 schema 允许 NULL)
func TestBuildPostAuditListSQL_PostInfoNullableStringColumnsCoalesced(t *testing.T) {
	sql := buildPostAuditListSQL()

	// 这些是 post_info 表自身、可空、当前在 SELECT 里的 string 列
	nullablePostCols := []string{
		"reject_reason",
		"contact",
		"area",
		"area_adcode", "area_name",
		"street_name",
		"city_adcode", "city_name",
	}

	for _, col := range nullablePostCols {
		needle := "COALESCE(p." + col
		if !strings.Contains(sql, needle) {
			t.Errorf("SQL 没对 post_info 表的可空 string 列 %q 加 COALESCE,"+
				"字段为 NULL 时 Scan 同样会炸。"+
				"\n  期望包含片段: %q"+
				"\n  实际 SQL:\n%s", col, needle, sql)
		}
	}
}

// TestBuildPostAuditListSQL_HasJoinAndOrder
// 锚定 SQL 骨架:防止有人大改 SQL 时把 JOIN / ORDER BY / LIMIT 一起丢了。
func TestBuildPostAuditListSQL_HasJoinAndOrder(t *testing.T) {
	sql := buildPostAuditListSQL()

	mustHave := []string{
		"FROM post_info p",
		"LEFT JOIN user u",
		"ON u.id = p.user_id",
		"ORDER BY",
		"(p.status = 1) DESC",
		"LIMIT ? OFFSET ?",
	}
	for _, frag := range mustHave {
		if !strings.Contains(sql, frag) {
			t.Errorf("SQL 缺少关键骨架片段 %q(可能是 JOIN/ORDER/LIMIT 被改丢了):\n%s", frag, sql)
		}
	}
}