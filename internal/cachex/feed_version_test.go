package cachex

import (
	"testing"
)

// TestFeedVersionKey 验证 version key 拼接格式稳定
//   - 格式一变会导致旧缓存命中错位 → 全站首页脏数据,关键安全测试
//   - local-life: 前缀不能少 —— 所有 key 都要带应用前缀
func TestFeedVersionKey(t *testing.T) {
	cases := []struct {
		scope, adcode string
		want          string
	}{
		{"city", "510100", "local-life:feed:ver:city:510100"},
		{"area", "510105", "local-life:feed:ver:area:510105"},
		{"city", "", "local-life:feed:ver:city:"},
	}
	for _, c := range cases {
		got := FeedVersionKey(c.scope, c.adcode)
		if got != c.want {
			t.Errorf("FeedVersionKey(%q,%q) = %q, want %q", c.scope, c.adcode, got, c.want)
		}
	}
}

// TestUniqueNonEmpty 验证 DML 后构造失效桶列表的边界:
//   - 空串过滤(无效 adcode 不生成 Redis key)
//   - 去重(同一 adcode 不重复 INCR)
//   - 顺序无关(主要看去重完整性)
func TestUniqueNonEmpty(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"all empty", []string{"", "", ""}, []string{}},
		{"single", []string{"510100"}, []string{"510100"}},
		{"dup filter", []string{"510100", "510100", "510105"}, []string{"510100", "510105"}},
		{"empty mixed", []string{"", "510100", "", "510105"}, []string{"510100", "510105"}},
		{"new + old 跨 adcode", []string{"510100", "510200"}, []string{"510100", "510200"}},
	}
	for _, c := range cases {
		got := UniqueNonEmpty(c.in...)
		if !equalSlice(got, c.want) {
			t.Errorf("%s: UniqueNonEmpty(%v) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}