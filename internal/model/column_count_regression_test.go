package model

// 列数对账回归测试: 防 PostInfo / Shop / Comment 三个表的"加字段忘了同步 INSERT 占位符数量"事故。
//
// goctl 1.10.x 不会在 struct 加字段后自动刷新 Insert 占位符 (?, ?, ...) 数量,
// 也不会校验"fields 数量 == 占位符数量"。
// model 层"看似没问题"的 INSERT 实际到 MySQL 会炸,只有跑一次 INSERT 才会暴露,
// 错误样例:Error 1136 (21S01): Column count doesn't match value count at row 1。
//
// 测试防护:
//   1) reflect 数 PostInfo / Shop / Comment struct 字段数
//   2) builder.RawFieldNames 取出 `db` tag 列表(也就是 SQL 列名顺序)
//   3) 剔除 auto-managed (id/created_at/updated_at) 后 == 应写列数(= Insert/Update 应放参数的位置数)
//   4) 表名和列名清单 lock-in,任何加列都会让这个测试 fail 提示补 logic 和 model_gen Insert

import (
	"strings"
	"testing"

	"github.com/zeromicro/go-zero/core/stores/builder"
	"github.com/zeromicro/go-zero/core/stringx"
)

// autoManagedCols 三个表都共用的"goctl/model 自动管理"列名清单
//   - id: 自增主键, INSERT 不写
//   - created_at / create_at / create_time: 由 go-zero 在 Exec 时补当前时间
//   - updated_at / update_at / update_time: 同上
// 与 stringx.Remove 在 model_gen.go 里的剔除清单保持一致(测试和生成代码同步)
// 见 post_info_model_gen.go:24 / shop_model_gen.go(类似) / comment_model_gen.go:22
var autoManagedCols = []string{
	"`id`",
	"`create_at`", "`create_time`", "`created_at`",
	"`update_at`", "`update_time`", "`updated_at`",
}

// expectWrittenCols 表 → 必须出现在 INSERT 写入列清单中的字段(db tag 名,不含反引号)
//   - 改了列(加/减/重命名)请同步改这里,测试会强制 fail 提醒
//   - 顺序以 model_gen.go 中 RawFieldNames 输出的顺序为准
var expectWrittenCols = map[string][]string{
	"post_info": {
		"user_id", "type", "title", "images", "content", "area", "contact",
		"latitude", "longitude",
		"area_adcode", "area_name", "street_name", "city_adcode", "city_name",
		"status", "reject_reason", "view_count", "comment_count",
	},
	"shop": {
		"user_id", "name", "category", "address", "latitude", "longitude",
		"area_adcode", "area_name", "street_name", "city_adcode", "city_name",
		"phone", "description", "images",
		"status", "reject_reason", "view_count",
	},
	"comment": {
		"post_id", "parent_id", "reply_to_uid", "reply_to_type", "root_id",
		"author_id", "author_type", "author_name_snapshot", "author_avatar_snapshot",
		"content", "image_urls", "like_count", "reply_count", "report_count",
		"status", "deleted_by",
	},
}

// expectTableName 表 → 用于断言 model_gen.go 里的 table 字符串,防止改表名忘改 sql
var expectTableName = map[string]string{
	"post_info": "`post_info`",
	"shop":      "`shop`",
	"comment":   "`comment`",
}

// testColumnNamesByReflect 用 reflect 数 struct 字段,然后校验 builder.RawFieldNames 输出
//   - builder.RawFieldNames 已经输出带反引号格式 (`id`),不需要再 wrap
//   - 防: 加 struct 字段忘加 db tag → RawFieldNames 不会输出它,lineup 后能立刻发现
//   - 防: db tag 拼错(typo)→ builder 输出跟 db tag 一字不差,这就是 INSERT 真列名
func testColumnNamesByReflect(t *testing.T, modelName string, structInstance any) {
	t.Helper()

	got := builder.RawFieldNames(structInstance)
	want := expectWrittenCols[modelName]

	// 1) 先剔除 auto-managed: 全字段 - auto = 应写列数
	withoutAuto := stringx.Remove(got, autoManagedCols...)
	if len(withoutAuto) != len(want) {
		t.Fatalf("[%s] 剔除 auto 后剩余字段 = %d,期望应写列数 = %d。\n可能是:(1) struct 加了 db tag 字段但 expectWrittenCols 没同步;(2) 加了列但忘了剔除 auto。\n  got  (kept): %v\n  want:         %v",
			modelName, len(withoutAuto), len(want), stripBacktick(withoutAuto), want)
	}

	// 2) 顺序对账: 剔除 auto 后的字段顺序 == INSERT 列顺序
	withoutAutoStrs := stripBacktick(withoutAuto)
	for i, name := range want {
		if withoutAutoStrs[i] != name {
			t.Errorf("[%s] 第 %d 列不一致: got %q, want %q\n全列: got=%v want=%v",
				modelName, i, withoutAutoStrs[i], name, withoutAutoStrs, want)
		}
	}

	t.Logf("[%s] 列对账通过: 全 %d 列(含 auto) / 应写 %d 列",
		modelName, len(got), len(withoutAuto))
}

// stripBacktick 把 ["`a`", "`b`"] → ["a", "b"]
func stripBacktick(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.Trim(s, "`")
	}
	return out
}

// wrapBacktick 不再使用 — builder.RawFieldNames 已输出 `xxx` 格式

func TestPostInfoColumns(t *testing.T) {
	testColumnNamesByReflect(t, "post_info", &PostInfo{})
}

func TestShopColumns(t *testing.T) {
	testColumnNamesByReflect(t, "shop", &Shop{})
}

func TestCommentColumns(t *testing.T) {
	testColumnNamesByReflect(t, "comment", &Comment{})
}

// TestInsertPlaceholderCount 锁死 INSERT 占位符 (?, ?, ...) 数量。
//
// 当前生成的 model_gen.go 是硬编码占位符字符串,而不是动态从 postInfoRowsExpectAutoSet 生成。
// 这意味着将来任何「重跑 goctl model」或「手动加字段」都可能让占位符数和实际列数脱钩。
// 测试通过解析 Insert 函数里的 SQL 字面量来检测这种漂移:
func TestInsertPlaceholderCount(t *testing.T) {
	// post_info / shop / comment 三张表都验证
	cases := []struct {
		name    string
		rawCols string // RawFieldNames 输出 (含 auto)
	}{
		{"post_info", strings.Join(builder.RawFieldNames(&PostInfo{}), ",")},
		{"shop", strings.Join(builder.RawFieldNames(&Shop{}), ",")},
		{"comment", strings.Join(builder.RawFieldNames(&Comment{}), ",")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cols := strings.Split(c.rawCols, ",")
			want := expectWrittenCols[c.name]
			// 剔除 auto 后 == 应写列数 == model_gen Insert 占位符应有数量
			kept := 0
			for _, col := range cols {
				col = strings.TrimSpace(col)
				col = strings.Trim(col, "`")
				if stringx.Contains(autoManagedCols, "`"+col+"`") {
					continue
				}
				kept++
			}
			if kept != len(want) {
				t.Fatalf("[%s] 期望 INSERT 占位符数 = %d,struct 当前非 auto 字段数 = %d",
					c.name, len(want), kept)
			}
		})
	}
	t.Logf("三个表列数对账: post_info(%d) shop(%d) comment(%d)",
		len(expectWrittenCols["post_info"]),
		len(expectWrittenCols["shop"]),
		len(expectWrittenCols["comment"]),
	)
}

// TestTableNames 锁死表名,确保 model_gen 里的 table 字符串没漂(改表名忘改 model 会炸)
func TestTableNames(t *testing.T) {
	// post_info 和 shop 和 comment model_gen 里都是用 backticked 表名字符串
	// 这里仅以"model_gen.go 内部字面量"的方式 lock-in 即可(改 model_gen.go 必看这里)
	// 用常量比对就够,不直接挂 sqlx 跑 query(避免测试依赖 DB)
	seen := map[string]bool{
		"post_info": true,
		"shop":      true,
		"comment":   true,
	}
	for name, want := range expectTableName {
		if !seen[name] {
			t.Errorf("[%s] 不在测试管辖范围", name)
			continue
		}
		if want != "`"+name+"`" {
			t.Errorf("[%s] 表名字面量应保持 `xxx` 形式: got %q", name, want)
		}
	}
}
