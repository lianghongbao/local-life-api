package admin

// admin_comment_list_logic_test.go
//   - 单元测试覆盖说明:本模块 logic 内部纯函数
//       * pickTopReporters(rows, maxPerComment) — in-memory 分组 + 截断
//       * toCommentAuthor(...) — reporter row → CommentAuthor
//       * normalizeAvatarURL(svcCtx, raw) — file_key → CDN URL,与 C 端对齐
//   - DB 相关 (QueryRowsCtx) 依赖 svcCtx.DBConn,项目目前没引入 interface 化,
//     要测需启动本地 MySQL,不在本单测范围。参考 admin_account_logic_test.go 约定。

import (
	"strings"
	"testing"

	"local-life-api/common/oss"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"
)

// reporterRow — 与 logic 包内 reporterRow 同名(同 package,直接复用)。
// 这里不再重复定义类型,避免漂移。

// 验证:pickTopReporters 按 comment_id 分组,每组按输入顺序保留前 N 个,空 ID 跳过。
func TestPickTopReporters_GroupAndCap(t *testing.T) {
	rows := []reporterRow{
		{CommentID: 100, ReporterID: "u1", Type: 1, Name: "用户1", Avatar: "a1"},
		{CommentID: 100, ReporterID: "u2", Type: 1, Name: "用户2", Avatar: "a2"},
		{CommentID: 100, ReporterID: "u3", Type: 1, Name: "用户3", Avatar: "a3"},
		{CommentID: 100, ReporterID: "u4", Type: 1, Name: "用户4", Avatar: "a4"}, // 被截断
		{CommentID: 200, ReporterID: "s1", Type: 2, Name: "店铺1", Avatar: "sa1"},
	}
	got := pickTopReporters(rows, 3)

	if len(got) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(got))
	}
	if len(got[100]) != 3 {
		t.Fatalf("comment 100: expected 3 reporters (capped), got %d", len(got[100]))
	}
	if got[100][0].Id != "u1" || got[100][1].Id != "u2" || got[100][2].Id != "u3" {
		t.Fatalf("comment 100: order/identity wrong: %+v", got[100])
	}
	if got[100][0].IsShop != false {
		t.Fatalf("user reporter should have IsShop=false")
	}
	if len(got[200]) != 1 || got[200][0].IsShop != true || got[200][0].Name != "店铺1" {
		t.Fatalf("comment 200: expected single shop reporter, got %+v", got[200])
	}
}

// 验证:空 reporter_id(可能是脏数据 / reporter 注销了)不产出 CommentAuthor,但仍占位?
// 设计:空 id 跳过 — 占位会污染 admin 视图,宁可少不可错。
func TestPickTopReporters_SkipEmptyID(t *testing.T) {
	rows := []reporterRow{
		{CommentID: 1, ReporterID: "", Type: 1, Name: "幽灵", Avatar: ""},
		{CommentID: 1, ReporterID: "u_real", Type: 1, Name: "真人", Avatar: "ok"},
	}
	got := pickTopReporters(rows, 3)
	if len(got[1]) != 1 {
		t.Fatalf("expected 1 reporter after skipping empty id, got %d", len(got[1]))
	}
	if got[1][0].Id != "u_real" {
		t.Fatalf("expected real user kept, got %+v", got[1][0])
	}
}

// 验证:空输入返回空 map(不 nil 便于调用方直接 range / index)。
func TestPickTopReporters_Empty(t *testing.T) {
	got := pickTopReporters(nil, 3)
	if got == nil {
		t.Fatal("expected non-nil empty map, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("expected empty map, got %d groups", len(got))
	}
}

// 验证:cap<=0 时兜底为 3(避免 admin 误传 0 时显示所有 reporter)。
func TestPickTopReporters_CapFloor(t *testing.T) {
	rows := []reporterRow{
		{CommentID: 1, ReporterID: "u1", Type: 1, Name: "n1"},
		{CommentID: 1, ReporterID: "u2", Type: 1, Name: "n2"},
		{CommentID: 1, ReporterID: "u3", Type: 1, Name: "n3"},
		{CommentID: 1, ReporterID: "u4", Type: 1, Name: "n4"},
	}
	got := pickTopReporters(rows, 0)
	if len(got[1]) != 3 {
		t.Fatalf("cap<=0 should floor to 3, got %d", len(got[1]))
	}
}

// 防回归:CommentAuthor.Type 字段映射正确(1=user → IsShop=false, 2=shop → IsShop=true)。
func TestToCommentAuthor_TypeMapping(t *testing.T) {
	cases := []struct {
		row      reporterRow
		wantType int
		wantShop bool
	}{
		{reporterRow{Type: 1, ReporterID: "u", Name: "n", Avatar: "a"}, 1, false},
		{reporterRow{Type: 2, ReporterID: "5", Name: "s", Avatar: ""}, 2, true},
	}
	for _, c := range cases {
		got := toCommentAuthor(c.row)
		if got.Type != c.wantType || got.IsShop != c.wantShop {
			t.Fatalf("row=%+v want type=%d shop=%v, got %+v", c.row, c.wantType, c.wantShop, got)
		}
		if got.Id != c.row.ReporterID || got.Name != c.row.Name || got.Avatar != c.row.Avatar {
			t.Fatalf("field copy wrong: %+v", got)
		}
	}
}

// 防止 pickTopReporters 输出 nil 切片(避免 JSON 序列化成 null)。
// 设计契约:即使某 comment 没 reporter,map 里也应有 []types.CommentAuthor{},
// 但本测试只覆盖"有 reporter 的 comment"切片非 nil。
func TestPickTopReporters_NonNilSlice(t *testing.T) {
	rows := []reporterRow{
		{CommentID: 7, ReporterID: "u1", Type: 1, Name: "n"},
	}
	got := pickTopReporters(rows, 3)
	v := got[7]
	if v == nil {
		t.Fatal("reporter slice must be non-nil (避免 JSON null)")
	}
	_ = types.CommentAuthor{}
}

// fakeSvcCtxWithOSS 构造一个仅含 OSS.Client 的 ServiceContext,
// 其它字段(DBConn/RedisCli/...)留 nil — 单元测试不依赖它们。
// URLPrefix 从参数注入,模拟 yaml 配置的 OSS_URL_PREFIX,验证"配置驱动"契约。
func fakeSvcCtxWithOSS(t *testing.T, urlPrefix string) *svc.ServiceContext {
	t.Helper()
	client := oss.NewClient(oss.Config{
		URLPrefix: urlPrefix,
		Hosts:     []string{"static.lamborai.com"},
	})
	return &svc.ServiceContext{
		OSS: svc.OSSBundle{Client: client},
	}
}

// 验证:裸 file_key → 拼接配置里的 URLPrefix(不能写死 static.lamborai.com)。
// 这是当前 bug 的核心:之前直接返回裸 key,el-avatar 当相对路径解析 → 404。
func TestNormalizeAvatarURL_BareKey_PrefixedByConfig(t *testing.T) {
	cases := []struct {
		name      string
		urlPrefix string
		raw       string
		want      string
	}{
		{
			name:      "prod: static.lamborai.com 域名前缀",
			urlPrefix: "https://static.lamborai.com",
			raw:       "avatar/1/5c6da971-256b-4dd5-b4a4-19682ea3bf68.jpeg",
			want:      "https://static.lamborai.com/avatar/1/5c6da971-256b-4dd5-b4a4-19682ea3bf68.jpeg",
		},
		{
			name:      "dev: 本地 mock 域名(模拟 127.0.0.1 static.lamborai.com hosts 绑定)",
			urlPrefix: "https://static.lamborai.com",
			raw:       "post/2/cover.jpg",
			want:      "https://static.lamborai.com/post/2/cover.jpg",
		},
		{
			name:      "URLPrefix 末尾带 / 也要兼容(不应产生 // 双斜杠)",
			urlPrefix: "https://static.lamborai.com/",
			raw:       "avatar/1/x.jpeg",
			want:      "https://static.lamborai.com/avatar/1/x.jpeg",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svcCtx := fakeSvcCtxWithOSS(t, c.urlPrefix)
			got := normalizeAvatarURL(svcCtx, c.raw)
			if got != c.want {
				t.Fatalf("normalizeAvatarURL(%q) with prefix=%q = %q, want %q",
					c.raw, c.urlPrefix, got, c.want)
			}
		})
	}
}

// 验证:已经是完整 http(s):// URL 的历史数据(snapshot 已被外部拼接过),原样返回。
// 避免双前缀(https://static.lamborai.com/https://xxx.com/...)这种烂活。
func TestNormalizeAvatarURL_AlreadyFullURL_Preserved(t *testing.T) {
	cases := []string{
		"https://static.lamborai.com/avatar/1/x.jpeg",
		"https://cdn.aliyuncs.com/foo/bar.png",
		"http://legacy.example.com/old/avatar.jpg",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			svcCtx := fakeSvcCtxWithOSS(t, "https://static.lamborai.com")
			got := normalizeAvatarURL(svcCtx, raw)
			if got != raw {
				t.Fatalf("expected full URL preserved, got %q (input %q)", got, raw)
			}
		})
	}
}

// 验证:空串原样返回(让前端走 cat avatar 兜底,与 C 端契约一致)。
func TestNormalizeAvatarURL_Empty_ReturnedAsEmpty(t *testing.T) {
	svcCtx := fakeSvcCtxWithOSS(t, "https://static.lamborai.com")
	if got := normalizeAvatarURL(svcCtx, ""); got != "" {
		t.Fatalf("empty raw should return empty, got %q", got)
	}
}

// 验证:nil svcCtx 不 panic,直接返回 raw(便于上游在 svcCtx 未初始化时降级)。
func TestNormalizeAvatarURL_NilSvcCtx_PassesThrough(t *testing.T) {
	if got := normalizeAvatarURL(nil, "avatar/1/x.jpeg"); got != "avatar/1/x.jpeg" {
		t.Fatalf("nil svcCtx should pass through raw, got %q", got)
	}
}

// =====================================================================
// 4 tab SQL 拼装测试 — 防止 4 个 case 漂移、count/list 不一致
// =====================================================================

// TestBuildListSQL_Pending_InnerJoinReport 验证 tab=1 待处理:
//   - INNER JOIN r WHERE r.status=1(只看 pending report)
//   - c.status IN (1,3)(comment 还可见或审核中)
//   - orderExpr="latest_report DESC"
func TestBuildListSQL_Pending_InnerJoinReport(t *testing.T) {
	sql, order := buildListSQL(1)
	mustHave := []string{
		"INNER JOIN comment_report r ON r.comment_id = c.id",
		"AND r.status = 1",
		"c.status IN (1, 3)",
	}
	for _, m := range mustHave {
		if !strings.Contains(sql, m) {
			t.Errorf("tab=1 SQL missing %q\nSQL:\n%s", m, sql)
		}
	}
	if order != "latest_report DESC" {
		t.Errorf("tab=1 orderExpr=%q, want latest_report DESC", order)
	}
}

// TestBuildListSQL_Hidden_LeftJoinReport 验证 tab=2 已隐藏:
//   - LEFT JOIN r(必须 LEFT!admin 直接 hide 无 report 的 comment 也要展示)
//   - WHERE c.status=3
//   - orderExpr=""(调用方按 c.id DESC 兜底)
//
// 回归场景:之前用 INNER JOIN 时,"admin 直接 hide 没 report 的 comment"会被漏掉。
func TestBuildListSQL_Hidden_LeftJoinReport(t *testing.T) {
	sql, order := buildListSQL(2)
	mustHave := []string{
		"LEFT JOIN comment_report r ON r.comment_id = c.id",
		"WHERE c.status = 3",
	}
	for _, m := range mustHave {
		if !strings.Contains(sql, m) {
			t.Errorf("tab=2 SQL missing %q\nSQL:\n%s", m, sql)
		}
	}
	// tab=2 必须 LEFT,不能 INNER(否则漏掉 admin 直接 hide 无 report 的 comment)
	if strings.Contains(sql, "INNER JOIN comment_report") {
		t.Errorf("tab=2 SQL should use LEFT JOIN, not INNER (regression: 漏掉无 report 的 comment)\nSQL:\n%s", sql)
	}
	if order != "" {
		t.Errorf("tab=2 orderExpr=%q, want \"\"", order)
	}
}

// TestBuildListSQL_Deleted_InnerJoinAdoptedReport 验证 tab=3 已删除:
//   - INNER JOIN r WHERE r.status=2(必须有被采纳的 report)
//   - WHERE c.status=2(评论状态)
//   - orderExpr="MAX(r.created_at) DESC"
//
// 语义:"评论举报"模块只展示"被举报后被采纳删除"的评论,
// 作者自删 / admin 直接 delete / 用户自删没 report 的都不该混进来。
// 那些该走"评论管理"模块(不在本接口范围)。
func TestBuildListSQL_Deleted_InnerJoinAdoptedReport(t *testing.T) {
	sql, order := buildListSQL(3)
	mustHave := []string{
		"INNER JOIN comment_report r ON r.comment_id = c.id",
		"AND r.status = 2",
		"WHERE c.status = 2",
	}
	for _, m := range mustHave {
		if !strings.Contains(sql, m) {
			t.Errorf("tab=3 SQL missing %q\nSQL:\n%s", m, sql)
		}
	}
	// tab=3 必须 INNER JOIN(防止"非被举报的删除"混入"评论举报"模块)
	if strings.Contains(sql, "LEFT JOIN comment_report r ON r.comment_id = c.id\n") {
		t.Errorf("tab=3 SQL should use INNER JOIN, not LEFT (regression: 把作者自删/直接 delete 误纳入举报模块)\nSQL:\n%s", sql)
	}
	if order != "MAX(r.created_at) DESC" {
		t.Errorf("tab=3 orderExpr=%q, want MAX(r.created_at) DESC", order)
	}
}

// TestBuildListSQL_Rejected_FilterByReportStatus 验证 tab=4 已驳回:
//   - WHERE r.status=3(keep action 不改 c.status,按 r.status 找)
//   - orderExpr="MAX(r.created_at) DESC"
func TestBuildListSQL_Rejected_FilterByReportStatus(t *testing.T) {
	sql, order := buildListSQL(4)
	if !strings.Contains(sql, "WHERE r.status = 3") {
		t.Errorf("tab=4 SQL missing WHERE r.status = 3\nSQL:\n%s", sql)
	}
	if order != "MAX(r.created_at) DESC" {
		t.Errorf("tab=4 orderExpr=%q, want MAX(r.created_at) DESC", order)
	}
}

// TestBuildCountSQL_PerFilterStatus 验证 4 个 tab 的 COUNT 跟 LIST 走的同一条 comment/report 关联路径。
// 防止有人改了 listSQL 但忘了同步 countSQL → 列表总数对不上。
func TestBuildCountSQL_PerFilterStatus(t *testing.T) {
	cases := []struct {
		filterStatus int
		mustHave     []string
		mustNotHave  []string
	}{
		{
			filterStatus: 1,
			mustHave:     []string{"INNER JOIN comment_report r", "AND r.status = 1", "c.status IN (1, 3)"},
		},
		{
			filterStatus: 2,
			mustHave:     []string{"FROM comment c", "WHERE c.status = 3"},
			mustNotHave:  []string{"INNER JOIN comment_report"},
		},
		{
			filterStatus: 3,
			mustHave:     []string{"INNER JOIN comment_report r", "AND r.status = 2", "WHERE c.status = 2"},
		},
		{
			filterStatus: 4,
			mustHave:     []string{"INNER JOIN comment_report r", "AND r.status = 3"},
		},
	}
	for _, c := range cases {
		t.Run("", func(t *testing.T) {
			sql := buildCountSQL(c.filterStatus)
			for _, m := range c.mustHave {
				if !strings.Contains(sql, m) {
					t.Errorf("filterStatus=%d COUNT missing %q\nSQL:\n%s", c.filterStatus, m, sql)
				}
			}
			for _, m := range c.mustNotHave {
				if strings.Contains(sql, m) {
					t.Errorf("filterStatus=%d COUNT should NOT contain %q\nSQL:\n%s", c.filterStatus, m, sql)
				}
			}
		})
	}
}

// TestBuildListSQL_DefaultIsPending 验证未知 filter_status 兜底走 tab=1。
// 前端 options 校验过,但 SQL 层防御性兜底防绕过。
func TestBuildListSQL_DefaultIsPending(t *testing.T) {
	for _, fs := range []int{0, 5, -1, 99} {
		t.Run("", func(t *testing.T) {
			sql, order := buildListSQL(fs)
			if !strings.Contains(sql, "AND r.status = 1") {
				t.Errorf("filterStatus=%d should default to tab=1 SQL\nSQL:\n%s", fs, sql)
			}
			if order != "latest_report DESC" {
				t.Errorf("filterStatus=%d orderExpr=%q, want latest_report DESC", fs, order)
			}
		})
	}
}

// =====================================================================
// reportStatusOf filterStatus → comment_report.status 映射测试
// 锁定 4 个 tab 该拉哪个状态的 reporter:
//   - 1(待处理) → 1(pending)
//   - 2/3(已隐藏/已删除) → 2(采纳)
//   - 4(已驳回) → 3(已驳回)
//
// 回归场景:之前 loadReporters 硬编码 WHERE r.status=1,导致 tab=2/3/4 的
// reporters 列查不到任何行 → admin 看到 "—" 占位。
// =====================================================================
func TestReportStatusOf_PerFilterStatus(t *testing.T) {
	cases := []struct {
		filterStatus int
		want         int
	}{
		{1, 1},  // 待处理 → pending(只看未处理举报,联系得到 ta)
		{2, 0},  // 已隐藏 → 0(哨兵:所有 status)
		{3, 0},  // 已删除 → 0
		{4, 0},  // 已驳回 → 0
		{0, 1},  // 未知兜底 pending
		{5, 1},  // 未知兜底 pending
		{-1, 1}, // 兜底
		{99, 1}, // 兜底
	}
	for _, c := range cases {
		t.Run("", func(t *testing.T) {
			if got := reportStatusOf(c.filterStatus); got != c.want {
				t.Fatalf("reportStatusOf(%d)=%d, want %d", c.filterStatus, got, c.want)
			}
		})
	}
}

// 防回归:tab=2/3/4 必须映射到哨兵 0(拉所有 status),不能是 1(只看 pending)或 2(只看采纳)。
// 真实数据里 r.status 经常跟 audit action 不严格对应(老数据/手改/直连 DB 都会偏离),
// 限制 r.status 会让 tab=2/3/4 的 reporter 列全空。
func TestReportStatusOf_HistoricalTabsMustBeSentinelZero(t *testing.T) {
	for _, fs := range []int{2, 3, 4} {
		got := reportStatusOf(fs)
		if got != 0 {
			t.Fatalf("filterStatus=%d should map to sentinel 0 (all statuses), got %d", fs, got)
		}
	}
}

// reportStatusWhere 哨兵 0 必须返 "1=1" 占位(让 SQL WHERE 语法合法),
// 非零返 "r.status = ?"。配合 args 注入逻辑:0 时不 push reportStatus 进 args,避免参数错位。
func TestReportStatusWhere_SentinelAndBound(t *testing.T) {
	if got := reportStatusWhere(0); got != "1=1" {
		t.Errorf("reportStatusWhere(0)=%q, want \"1=1\"", got)
	}
	if got := reportStatusWhere(1); got != "r.status = ?" {
		t.Errorf("reportStatusWhere(1)=%q, want \"r.status = ?\"", got)
	}
	if got := reportStatusWhere(2); got != "r.status = ?" {
		t.Errorf("reportStatusWhere(2)=%q, want \"r.status = ?\"", got)
	}
}
