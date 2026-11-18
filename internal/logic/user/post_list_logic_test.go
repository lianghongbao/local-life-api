package user

import (
	"reflect"
	"strings"
	"testing"
)

// postListRowCount 通过反射统计 struct 的可赋值字段数(模拟 go-zero sqlx 的 unwrapFields)
//   用来强制保证:SELECT 列数 == 目标 struct 字段数(strict mode 不允许列数 < 字段数)
func postListRowCount(t reflect.Type) int {
	n := 0
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		// 忽略 `db:"-"` 的字段
		if strings.TrimSpace(strings.Split(f.Tag.Get("db"), ",")[0]) == "-" {
			continue
		}
		n++
	}
	return n
}

// TestPostListRow_字段数匹配SELECT 确保 local postListRow 的字段数 = post_list_logic.go 的 SELECT 列数。
//
// go-zero sqlx strict 模式下,Scan 目标 struct 字段数与 SELECT 列数必须相等,
// 否则报 ErrNotMatchDestination("not matching destination to scan"),500。
//
// 加新列时(post_info 加字段,例如区划四字段):SELECT 与 postListRow 必须同步增减,任一边漏改都炸。
//
// 注意:搜索模式下 SELECT 多一列 MATCH 相关度(search_score),非搜索模式补常量 0,
// 两条路径列数都是 19。
func TestPostListRow_字段数匹配SELECT(t *testing.T) {
	// post_list_logic.go 当前 SELECT 的列(以换行 + 逗号拆开,手工列)
	const selectColumns = `
		p.id, p.user_id,
		u.nickname, u.avatar,
		p.type, p.title, p.images, p.content,
		p.area, p.latitude, p.longitude, p.contact,
		p.status,
		DATE_FORMAT(p.created_at, '%Y-%m-%d %H:%i:%s') AS created_at,
		p.area_adcode, p.area_name, p.street_name, p.city_adcode,
		MATCH(p.title, p.content) AGAINST (? IN BOOLEAN MODE) AS search_score
	`
	// 把 SQL 切成列名列表(去注释 / 逗号 / 换行)
	cols := splitSelectColumns(selectColumns)
	wantCols := 19
	if len(cols) != wantCols {
		t.Fatalf("SQL 列数变了(预期 %d, 实际 %d): %v", wantCols, len(cols), cols)
	}

	// 修复后:postListRow 是 post_list_logic.go 内的本地 struct,
	// 字段数必须 == SELECT 列数,否则会回到老 bug。
	rowType := reflect.TypeOf(postListRow{})
	if got := postListRowCount(rowType); got != wantCols {
		t.Errorf("postListRow 字段数(%d) ≠ SELECT 列数(%d),go-zero sqlx strict mode 会返回 ErrNotMatchDestination", got, wantCols)
	}
}

// splitSelectColumns 简单切 SQL:顶层逗号分列,跳过括号/引号内的逗号
//   够用就行 — 这只是测试辅助,不要求是 SQL parser
func splitSelectColumns(sql string) []string {
	var cols []string
	depth := 0    // 括号深度
	inStr := byte(0)
	last := 0
	for i := 0; i < len(sql); i++ {
		c := sql[i]
		switch {
		case inStr != 0:
			if c == inStr {
				inStr = 0
			}
		case c == '\'' || c == '"':
			inStr = c
		case c == '(':
			depth++
		case c == ')':
			depth--
		case c == ',' && depth == 0:
			cols = append(cols, strings.TrimSpace(sql[last:i]))
			last = i + 1
		}
	}
	// 收尾
	if tail := strings.TrimSpace(sql[last:]); tail != "" {
		cols = append(cols, tail)
	}
	// 过滤空行 / 注释
	out := cols[:0]
	for _, c := range cols {
		if c == "" || strings.HasPrefix(c, "--") {
			continue
		}
		out = append(out, c)
	}
	return out
}
