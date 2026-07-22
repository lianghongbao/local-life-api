package background

import (
	"reflect"
	"strings"
	"testing"
)

// TestBuildCaseUpdateSQL_NormalCases 验证 buildCaseUpdateSQL 拼出的 SQL 与 args 的正确性。
//
//	SQL 形如：
//	  UPDATE `post_info` SET `view_count` = CASE `id`
//	      WHEN 1001 THEN 5
//	      WHEN 1002 THEN 7
//	  END
//	  WHERE `id` IN (1001, 1002)
//
// 参数顺序：先所有 WHEN 段两两一组 (id1, value1, id2, value2, ...)，
// 后面跟 WHERE 段所有 id 列表 (id1, id2, ...)。
func TestBuildCaseUpdateSQL_NormalCases(t *testing.T) {
	type tc struct {
		name        string
		tableName   string
		entries     []viewCountFlushEntry
		wantSQL     string
		wantArgsLen int
		// 由于 args 顺序依赖 entries 顺序,以下两个字段作精细断言
		wantFirstArg       any
		wantLastArg        any
		wantSQLContains    []string
		wantSQLNotContains []string
	}

	cases := []tc{
		{
			name:      "空 entries - 不应进入函数（保护性测试）",
			tableName: "post_info",
			entries:   nil,
			// 实际空 entries 时: SQL 仍是 UPDATE 形式但无 WHEN/IN 段, 也不应 panic
			wantSQL:         "UPDATE `post_info` SET `view_count` = CASE `id` END WHERE `id` IN ()",
			wantArgsLen:     0,
			wantSQLContains: []string{"UPDATE", "`post_info`", "CASE", "WHERE", "IN ()"},
			wantFirstArg:    nil,
			wantLastArg:     nil,
		},
		{
			name:      "单条 entry",
			tableName: "post_info",
			entries: []viewCountFlushEntry{
				{Id: 1001, Incr: 5},
			},
			wantSQL:         "UPDATE `post_info` SET `view_count` = CASE `id` WHEN ? THEN ? END WHERE `id` IN (?)",
			wantArgsLen:     3, // 1 WHEN 段 2 args + 1 IN 段 1 arg
			wantSQLContains: []string{"UPDATE", "`post_info`", "CASE `id`", "WHEN ? THEN ?", "WHERE `id` IN (?)"},
			wantFirstArg:    uint64(1001),
			wantLastArg:     uint64(1001),
		},
		{
			name:      "多条 entry",
			tableName: "shop",
			entries: []viewCountFlushEntry{
				{Id: 1001, Incr: 5},
				{Id: 1002, Incr: 7},
				{Id: 1003, Incr: 12},
			},
			wantSQL: "UPDATE `shop` SET `view_count` = CASE `id` WHEN ? THEN ? WHEN ? THEN ? WHEN ? THEN ? END WHERE `id` IN (?,?,?)",
			// 3 个 WHEN 段 = 6 args; 3 个 IN 段 = 3 args; 总 9
			wantArgsLen:     9,
			wantSQLContains: []string{"UPDATE", "`shop`", "WHEN ? THEN ?", "WHERE `id` IN (?,?,?)"},
			wantFirstArg:    uint64(1001), // 第一个 WHEN 的 id
			wantLastArg:     uint64(1003), // 最后一个 IN 的 id
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			table := FlushTableConfig{Name: c.tableName}
			gotSQL, gotArgs := buildCaseUpdateSQL(table, c.entries)

			if !strings.Contains(gotSQL, c.wantSQL) && gotSQL != c.wantSQL {
				// contains 模式 OR 完全相等
				matched := false
				for _, sub := range c.wantSQLContains {
					if !strings.Contains(gotSQL, sub) {
						t.Errorf("buildCaseUpdateSQL() SQL 缺段: want contains %q, got: %s", sub, gotSQL)
					}
				}
				_ = matched
			}
			if len(gotArgs) != c.wantArgsLen {
				t.Errorf("buildCaseUpdateSQL() args 数量: want %d, got %d (args=%v)", c.wantArgsLen, len(gotArgs), gotArgs)
			}
			if c.wantFirstArg != nil && len(gotArgs) > 0 {
				if !reflect.DeepEqual(gotArgs[0], c.wantFirstArg) {
					t.Errorf("buildCaseUpdateSQL() args[0]: want %v, got %v", c.wantFirstArg, gotArgs[0])
				}
			}
			if c.wantLastArg != nil && len(gotArgs) > 0 {
				if !reflect.DeepEqual(gotArgs[len(gotArgs)-1], c.wantLastArg) {
					t.Errorf("buildCaseUpdateSQL() args[last]: want %v, got %v", c.wantLastArg, gotArgs[len(gotArgs)-1])
				}
			}
		})
	}
}

// TestBuildCaseUpdateSQL_ArgsOrder 验证参数顺序：
// 先所有 WHEN 段两两 (id, value) 排列, 然后所有 IN 段的 id。
//
// 这是该函数最关键的契约, 如果错乱会导致 SQL 拼装和 args 错位
// → mysql 上 view_count 写到错误的行 / 错误值。
func TestBuildCaseUpdateSQL_ArgsOrder(t *testing.T) {
	entries := []viewCountFlushEntry{
		{Id: 1001, Incr: 5},
		{Id: 1002, Incr: 7},
		{Id: 1003, Incr: 12},
	}
	table := FlushTableConfig{Name: "post_info"}

	_, gotArgs := buildCaseUpdateSQL(table, entries)

	// 期望顺序: [1001, 5, 1002, 7, 1003, 12, 1001, 1002, 1003]
	want := []any{
		uint64(1001), int64(5),
		uint64(1002), int64(7),
		uint64(1003), int64(12),
		uint64(1001), uint64(1002), uint64(1003),
	}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Errorf("buildCaseUpdateSQL() args 顺序不匹配: want %v, got %v", want, gotArgs)
	}
}

// TestBuildCaseUpdateSQL_ShapeContains 验证 SQL 包含关键片段（不分顺序）。
func TestBuildCaseUpdateSQL_ShapeContains(t *testing.T) {
	entries := []viewCountFlushEntry{
		{Id: 1, Incr: 100},
		{Id: 2, Incr: 200},
	}
	table := FlushTableConfig{Name: "shop"}

	gotSQL, _ := buildCaseUpdateSQL(table, entries)

	// 关键片段
	mustContain := []string{
		"UPDATE `shop`",
		"SET `view_count` = CASE `id`",
		"WHEN ? THEN ?",
		"END",
		"WHERE `id` IN (?,?)",
	}
	for _, sub := range mustContain {
		if !strings.Contains(gotSQL, sub) {
			t.Errorf("SQL 缺关键段: %q in %q", sub, gotSQL)
		}
	}

	// 防"清表"等注入：表名必须包在反引号中
	if !strings.Contains(gotSQL, "`shop`") {
		t.Errorf("表名未加反引号保护: %q", gotSQL)
	}
}

// TestBuildCaseUpdateSQL_TableNameEscaping 验证表名 escape 一致性。
// 表名通过 FlushTableConfig 传入, 必须包在反引号中以防 SQL 注入。
// 我们的 FlushTableConfig 应当只接受 hardcode 常量, 但函数层做防
// 御性 escape 是良好实践。
func TestBuildCaseUpdateSQL_TableNameEscaping(t *testing.T) {
	cases := []struct {
		name      string
		tableName string
		wantInSQL string
	}{
		{"post_info", "post_info", "`post_info`"},
		{"shop", "shop", "`shop`"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			table := FlushTableConfig{Name: c.tableName}
			entries := []viewCountFlushEntry{{Id: 1, Incr: 1}}
			gotSQL, _ := buildCaseUpdateSQL(table, entries)
			if !strings.Contains(gotSQL, c.wantInSQL) {
				t.Errorf("表名未正确 escape: want %q in %q", c.wantInSQL, gotSQL)
			}
		})
	}
}
