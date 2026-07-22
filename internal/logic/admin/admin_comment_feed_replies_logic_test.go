package admin

// admin_comment_feed_replies_logic_test.go
//   - 同 admin_comment_feed_list_logic_test.go 的"logic-only 不依赖 DB"约定
//   - 测的是 statusTextOf 在二级列表场景下的正确性(同一函数,replies 也调用)

import "testing"

func TestRepliesStatusTextOf_StatusNotEqualsStatus3Hidden(t *testing.T) {
	// 重要保证：admin feed 的二级列表,凡是 status=3 的都不应出现(statusTextOf 是展示用)
	// 我们这里只断言:statusTextOf(3) 仍给出 "审核中",而不被误判为 "已删除"。
	// 后端真正过滤由 SQL "status != 3" 兜底,不在本测试覆盖范围。
	if got := statusTextOf(3); got != "审核中" {
		t.Fatalf("statusTextOf(3) = %q, want 审核中", got)
	}
}
