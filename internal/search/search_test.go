package search

import "testing"

// TestNormalize 验证关键词规范化
func TestNormalize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"普通中文", "自行车", "自行车"},
		{"去首尾空格", "  自行车  ", "自行车"},
		{"全角空格转半角", "　自行车　", "自行车"},
		{"连续空格折叠", "二手  自行车", "二手 自行车"},
		{"纯空白", "   ", ""},
		{"空串", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Normalize(c.in); got != c.want {
				t.Errorf("Normalize(%q) = %q, 期望 %q", c.in, got, c.want)
			}
		})
	}
}

// TestNormalize_超长截断 验证按 rune 裁剪且不切半 UTF-8 字符
func TestNormalize_超长截断(t *testing.T) {
	long := ""
	for i := 0; i < 100; i++ {
		long += "中"
	}
	got := Normalize(long)
	if RuneLen(got) != MaxKeywordRunes {
		t.Errorf("超长关键词裁剪后 rune 数 = %d, 期望 %d", RuneLen(got), MaxKeywordRunes)
	}
	// 裁剪后必须仍是合法 UTF-8(不能切出半个汉字)
	for _, r := range got {
		if r != '中' {
			t.Errorf("裁剪结果含非预期字符 %q", r)
		}
	}
}

// TestIsValid 验证单字关键词被拒绝
func TestIsValid(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"自", false},   // 单字:ngram 噪音太大,拒绝
		{"自行车", true}, // 2 字以上:允许
		{"二手 自行车", true},
	}
	for _, c := range cases {
		if got := IsValid(c.in); got != c.want {
			t.Errorf("IsValid(%q) = %v, 期望 %v", c.in, got, c.want)
		}
	}
}

// TestBuildBooleanQuery_单词 验证单词走整词必含(不拆 2-gram)
func TestBuildBooleanQuery_单词(t *testing.T) {
	got := BuildBooleanQuery("自行车")
	if got != "+自行车" {
		t.Errorf("BuildBooleanQuery(\"自行车\") = %q, 期望 %q", got, "+自行车")
	}
}

// TestBuildBooleanQuery_多词 验证多词用 + 全必含(AND 语义)
func TestBuildBooleanQuery_多词(t *testing.T) {
	got := BuildBooleanQuery("二手 自行车")
	if got != "+二手 +自行车" {
		t.Errorf("BuildBooleanQuery(\"二手 自行车\") = %q, 期望 %q", got, "+二手 +自行车")
	}
}

// TestBuildBooleanQuery_转义 验证 BOOLEAN 运算符字符被转义
//   关键词里的 + - > < ~ * " ( ) 都有 BOOLEAN 特殊含义,不转义会改变语义
func TestBuildBooleanQuery_转义(t *testing.T) {
	got := BuildBooleanQuery("C++")
	if got != "+C\\+\\+" {
		t.Errorf("BuildBooleanQuery(\"C++\") = %q, 期望 %q", got, "+C\\+\\+")
	}

	got2 := BuildBooleanQuery("-排除词")
	if got2 != "+\\-排除词" {
		t.Errorf("BuildBooleanQuery(\"-排除词\") = %q, 期望 %q", got2, "+\\-排除词")
	}
}

// TestBuildBooleanQuery_含空格单词不被拆 验证 "二手 自行车" 拆成两个必含词
func TestBuildBooleanQuery_含空格单词不被拆(t *testing.T) {
	got := BuildBooleanQuery("北京 朝阳")
	if got != "+北京 +朝阳" {
		t.Errorf("BuildBooleanQuery(\"北京 朝阳\") = %q, 期望 %q", got, "+北京 +朝阳")
	}
}

// TestCacheKeyHash 验证缓存 key 哈希:稳定、可区分、不含原始字符
func TestCacheKeyHash(t *testing.T) {
	// 空关键词返回空串(调用方转成 "0")
	if got := CacheKeyHash(""); got != "" {
		t.Errorf("CacheKeyHash(\"\") = %q, 期望空串", got)
	}

	// 相同关键词必须产生相同 hash(否则缓存永远不命中)
	h1 := CacheKeyHash("自行车")
	h2 := CacheKeyHash("自行车")
	if h1 != h2 {
		t.Errorf("相同关键词 hash 不一致: %q vs %q", h1, h2)
	}

	// 不同关键词必须产生不同 hash
	h3 := CacheKeyHash("摩托车")
	if h1 == h3 {
		t.Errorf("不同关键词 hash 相同: %q", h1)
	}

	// 长度受控
	if len(h1) > CacheKeyHashLen {
		t.Errorf("hash 长度 %d 超过上限 %d", len(h1), CacheKeyHashLen)
	}

	// 不得包含原始中文字符(会污染 Redis key)
	if HasCJK(h1) {
		t.Errorf("hash %q 含中文字符", h1)
	}
}

// TestCacheKeyHash_不含分隔符 验证 hash 不含冒号等 Redis key 分隔符
func TestCacheKeyHash_不含分隔符(t *testing.T) {
	h := CacheKeyHash("二手:自行车")
	for _, bad := range []string{":", "\n", " ", "\r"} {
		for _, c := range h {
			if string(c) == bad {
				t.Errorf("hash %q 含非法字符 %q", h, bad)
			}
		}
	}
}

// TestTooShort 验证「有内容但太短」与「没输入」的区分
func TestTooShort(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},      // 没输关键词:不是太短,是"不搜索"
		{"猫", true},      // 单字:太短
		{"自", true},      // 单字:太短
		{"自行车", false},  // 2 字:正常搜索
		{"二手 自行车", false}, // 含空格也算够长
	}
	for _, c := range cases {
		if got := TooShort(c.in); got != c.want {
			t.Errorf("TooShort(%q) = %v, 期望 %v", c.in, got, c.want)
		}
	}
}

// TestContainsAllRunes 验证包含判断
func TestContainsAllRunes(t *testing.T) {
	if !ContainsAllRunes("自行车", "出售二手自行车一辆") {
		t.Error("应包含「自行车」")
	}
	if ContainsAllRunes("自行车", "出售二手摩托车一辆") {
		t.Error("不应包含「自行车」")
	}
	// 空关键词视为全部包含
	if !ContainsAllRunes("", "任意文本") {
		t.Error("空关键词应视为全部包含")
	}
}
