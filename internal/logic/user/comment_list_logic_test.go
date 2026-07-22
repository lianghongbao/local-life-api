package user

import (
	"reflect"
	"testing"

	"local-life-api/internal/types"

	"github.com/stretchr/testify/assert"
)

// TestPickTopRepliesReturnsAtMostMax 验证 pickTopReplies 截断至 max
func TestPickTopRepliesReturnsAtMostMax(t *testing.T) {
	got := pickTopReplies(makeReplies(5), 3)
	assert.Len(t, got, 3)
}

// TestPickTopRepliesAllItemsWhenBelowMax 验证 max > len 时返回全部
func TestPickTopRepliesAllItemsWhenBelowMax(t *testing.T) {
	got := pickTopReplies(makeReplies(5), 10)
	assert.Len(t, got, 5)
}

// makeReplies test fixture
func makeReplies(n int) []*types.CommentItem {
	out := make([]*types.CommentItem, n)
	for i := 0; i < n; i++ {
		out[i] = &types.CommentItem{Id: int64(i + 1)}
	}
	return out
}

// TestBuildCommentItemParent 验证父评论(无 reply_to)各字段映射正确
func TestBuildCommentItemParent(t *testing.T) {
	r := &parentRow{
		Id:           100,
		PostId:       5,
		ParentId:     0,
		RootId:       100,
		AuthorId:     "user_openid_42",
		AuthorType:   1, // user
		AuthorName:   "张大哥",
		AuthorAvatar: "https://oss/x.png",
		Content:      "hello",
		LikeCount:    3,
		ReplyCount:   2,
		Status:       1,
		ReplyToUid:   "", // 一级评论,无 @
		ReplyToType:  0,
		CreatedAt:    "2026-07-14T10:00:00+08:00",
	}
	it := buildCommentItem(nil, r, "user_openid_99")

	assert.Equal(t, int64(100), it.Id)
	assert.Equal(t, int64(5), it.PostId)
	assert.Equal(t, int64(0), it.ParentId)
	assert.Equal(t, int64(100), it.RootId)
	assert.Equal(t, "张大哥", it.Author.Name)
	assert.False(t, it.Author.IsShop)
	assert.Nil(t, it.ReplyToUser)
	assert.False(t, it.IsMentionedMe)
	assert.False(t, it.IsLiked) // 默认 false
	assert.Equal(t, "hello", it.Content)
}

// TestBuildCommentItemShopAuthor 验证商家 author (author_type=2) 时 IsShop=true
func TestBuildCommentItemShopAuthor(t *testing.T) {
	r := &parentRow{
		Id:           200,
		PostId:       5,
		ParentId:     0,
		RootId:       200,
		AuthorId:     "shop_99",
		AuthorType:   2,
		AuthorName:   "商家C",
		AuthorAvatar: "",
		Content:      "店主自评",
		LikeCount:    0,
		ReplyCount:   0,
		Status:       1,
		ReplyToUid:   "",
		ReplyToType:  0,
		CreatedAt:    "2026-07-14T10:00:00+08:00",
	}
	it := buildCommentItem(nil, r, "user_openid_42")
	assert.True(t, it.Author.IsShop)
	assert.Equal(t, "shop_99", it.Author.Id)
}

// TestBuildCommentItemIsMentionedMe 验证 reply_to_uid == currentUID 时被标 true
func TestBuildCommentItemIsMentionedMe(t *testing.T) {
	r := &parentRow{
		Id:          200,
		PostId:      5,
		ParentId:    1,
		RootId:      1,
		AuthorId:    "shop_99",
		AuthorType:  2,
		AuthorName:  "商家C",
		Content:     "回复提醒",
		LikeCount:   0,
		ReplyCount:  0,
		Status:      1,
		ReplyToUid:  "user_openid_42", // 被回复的是登录用户本人
		ReplyToType: 1,
		CreatedAt:   "2026-07-14T10:00:00+08:00",
	}
	it := buildCommentItem(nil, r, "user_openid_42")
	assert.True(t, it.IsMentionedMe)
	assert.NotNil(t, it.ReplyToUser)
	assert.Equal(t, "user_openid_42", it.ReplyToUser.Id)
	assert.Equal(t, 1, it.ReplyToUser.Type)
}

// TestBuildCommentItemIsMentionedMeOtherUser 验证被别人 @ 时不误标
func TestBuildCommentItemIsMentionedMeOtherUser(t *testing.T) {
	r := &parentRow{
		Id:          300,
		PostId:      5,
		ParentId:    1,
		RootId:      1,
		AuthorId:    "shop_99",
		AuthorType:  2,
		AuthorName:  "商家C",
		Content:     "@别人",
		ReplyToUid:  "user_openid_other", // @ 别人
		ReplyToType: 1,
		CreatedAt:   "2026-07-14T10:00:00+08:00",
	}
	it := buildCommentItem(nil, r, "user_openid_42")
	assert.False(t, it.IsMentionedMe)
}

// TestBuildInClauseEmpty 验证 ids=[]int64{} 的边界
func TestBuildInClauseEmpty(t *testing.T) {
	ph, args := buildInClause([]int64{})
	assert.Equal(t, "NULL", ph)
	assert.Nil(t, args)
}

// TestBuildInClauseMultiple 验证多个 id
func TestBuildInClauseMultiple(t *testing.T) {
	ph, args := buildInClause([]int64{1, 2, 3})
	assert.Equal(t, "?,?,?", ph)
	assert.Equal(t, []interface{}{int64(1), int64(2), int64(3)}, args)
}

// TestBuildInClauseSingle 验证单个 id
func TestBuildInClauseSingle(t *testing.T) {
	ph, args := buildInClause([]int64{42})
	assert.Equal(t, "?", ph)
	assert.Equal(t, []interface{}{int64(42)}, args)
}

// TestParentRow_字段数匹配SELECT 验证 SQL SELECT 列数 == parentRow struct 字段数
//   - 防 SELECT 增加列时只改一边导致 go-zero sqlx strict mode 返回 ErrNotMatchDestination → 500
//   - 镜像 post_list_logic_test.go 的 TestPostListRow_字段数匹配SELECT,复用同款
//     splitSelectColumns / postListRowCount 包级辅助函数
//   - parentSQL / childrenSQL 是 comment_list_logic.go 里两条 SELECT 的列(FROM 之前那段)
func TestParentRow_字段数匹配SELECT(t *testing.T) {
	// comment_list_logic.go 父评论 SELECT 的列
	const parentSelectColumns = `
		id, post_id, parent_id, root_id, author_id, author_type,
		author_name_snapshot, author_avatar_snapshot, content,
		like_count, reply_count, status, reply_to_uid, reply_to_type,
		DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') AS created_at
	`
	// 子回复 SELECT 的列(与父评论列表完全一致,须同步)
	const childrenSelectColumns = `
		id, post_id, parent_id, root_id, author_id, author_type,
		author_name_snapshot, author_avatar_snapshot, content,
		like_count, reply_count, status, reply_to_uid, reply_to_type,
		DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') AS created_at
	`

	// parentRow 用 reflect 数可导出、非 db:"-" 字段数
	wantCols := postListRowCount(reflect.TypeOf(parentRow{}))

	parentCols := splitSelectColumns(parentSelectColumns)
	assert.Equal(t, wantCols, len(parentCols),
		"parentSQL 列数 ≠ parentRow 字段数,go-zero sqlx strict mode 会返回 ErrNotMatchDestination: %v", parentCols)

	childrenCols := splitSelectColumns(childrenSelectColumns)
	assert.Equal(t, wantCols, len(childrenCols),
		"childrenSQL 列数 ≠ parentRow 字段数,go-zero sqlx strict mode 会返回 ErrNotMatchDestination: %v", childrenCols)
}
