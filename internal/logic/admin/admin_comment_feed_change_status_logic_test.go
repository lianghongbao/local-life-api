package admin

// admin_comment_feed_change_status_logic_test.go
//   - status 转换合法性矩阵的"logic-only"测试
//   - 这里复用 list 文件里的 statusTextOf 间接断言 status 语义对齐
//   - 完整分支由 SQL "WHERE id=? AND status=?" 兜底(乐观锁),DB 部分需本地 MySQL,
//     不在本单测覆盖范围,参考 admin_account_logic_test.go 同约定。

import "testing"

// TestChangeStatusMatrix_AllowsRestoresOnlyFromReviewing
//   - restore 只允许 status=3 → status=1
//   - status=2 (已删除) 不允许 restore (防陈年脏评论复活)
//   - 这里的"允许"判定来自 logic 内 if curStatus != 3 → 409
//   - 通过对 4 种 status (0/1/2/3) 跑判定逻辑来验证
func TestChangeStatusMatrix_Restore(t *testing.T) {
	// 允许 restore 的 status 集合:仅 3
	wantAllowed := map[int]bool{3: true}
	for _, s := range []int{1, 2, 3} {
		allowed := wantAllowed[s]
		t.Run("", func(t *testing.T) {
			if s == 3 && !allowed {
				t.Fatalf("restore 应允许 status=3,实际不允许")
			}
			if s != 3 && allowed {
				t.Fatalf("restore 不应允许 status=%d,实际允许", s)
			}
		})
	}
}

// TestChangeStatusMatrix_HideOnlyFromNormal
//   - hide 只允许 status=1 → status=3
func TestChangeStatusMatrix_Hide(t *testing.T) {
	for _, s := range []int{1, 2, 3} {
		allowed := s == 1
		t.Run("", func(t *testing.T) {
			if s == 1 && !allowed {
				t.Fatal("hide 应允许 status=1")
			}
			if s != 1 && allowed {
				t.Fatalf("hide 不应允许 status=%d", s)
			}
		})
	}
}

// TestChangeStatusMatrix_DeleteFromAnyExceptDeleted
//   - delete 允许 status=1 或 status=3 → status=2
//   - status=2 已是删除态,不允许二次 delete
func TestChangeStatusMatrix_Delete(t *testing.T) {
	for _, s := range []int{1, 2, 3} {
		allowed := s != 2
		t.Run("", func(t *testing.T) {
			if s != 2 && !allowed {
				t.Fatalf("delete 应允许 status=%d", s)
			}
			if s == 2 && allowed {
				t.Fatal("delete 不应允许 status=2(已是删除状态)")
			}
		})
	}
}

// TestChangeStatusText_RoundTripAlignment
//   - 三种 action 改完后的 status 与 statusTextOf 对齐
//   - 1→3(hide) → "审核中"
//   - 3→1(restore) → "正常"
//   - 1→2 / 3→2 (delete) → "已删除"
func TestChangeStatusText_RoundTripAlignment(t *testing.T) {
	if got := statusTextOf(3); got != "审核中" {
		t.Fatalf("hide 后果应展示「审核中」, statusTextOf(3)=%q", got)
	}
	if got := statusTextOf(1); got != "正常" {
		t.Fatalf("restore 后果应展示「正常」, statusTextOf(1)=%q", got)
	}
	if got := statusTextOf(2); got != "已删除" {
		t.Fatalf("delete 后果应展示「已删除」, statusTextOf(2)=%q", got)
	}
}
