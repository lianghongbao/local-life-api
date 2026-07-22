package user

import (
	"context"

	"local-life-api/internal/errorx"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type CommentRepliesLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewCommentRepliesLogic(ctx context.Context, svcCtx *svc.ServiceContext) *CommentRepliesLogic {
	return &CommentRepliesLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// currentUID 从 ctx 取登录用户的 openid(业务标识符,与 comment.author_id / reply_to_uid 同源)
//   - 未登录时返回 "" → is_liked 整段跳过、is_mentioned_me 一律 false
//   - 委托给 package-level currentUserFromCtx 共享 helper(与 CommentListLogic 共用)
func (l *CommentRepliesLogic) currentUID() string {
	uid, _ := currentUserFromCtx(l.ctx)
	return uid
}

// CommentReplies 帖子下指定 root_id 的全量子回复(无 top-3 截断)
//   - 排序: 子回复 created_at ASC(老回复在前,对话感)
//   - 分页: limit+1 法判定 hasMore(取 limit+1 行,第 limit 行被截掉)
//   - is_liked: 一次 IN(comment_id IN (?,...)) 拿当前用户点过赞的 comment_id set
//   - is_mentioned_me: 仅当 reply_to_uid == currentUID;未登录时一律 false
//   - NextCursor 暂留空,后续升级为 created_at + id 复合 cursor
//   - root 已删除或不存在 → 优雅降级,返回空 items(不报错,前端不再显示"展开更多"按钮)
func (l *CommentRepliesLogic) CommentReplies(req *types.CommentRepliesRequest) (*types.CommentListResponse, error) {
	// 1) 拿 currentUID(未登录 → "")
	currentUID := l.currentUID()

	// 2) 校验 root 存在(且 parent_id=0,status 正常)
	var rootExists int
	checkSQL := `SELECT COUNT(*) FROM comment
	             WHERE id = ? AND post_id = ? AND parent_id = 0 AND status IN (1, 2)`
	if err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &rootExists, checkSQL, req.RootId, req.PostId); err != nil {
		l.Errorf("check root comment failed: post_id=%d, root_id=%d, err=%v", req.PostId, req.RootId, err)
		return nil, errorx.ErrInternal
	}
	if rootExists == 0 {
		// root 已删除/不存在 → 优雅降级返回空
		return &types.CommentListResponse{
			Items:   []*types.CommentItem{},
			HasMore: false,
		}, nil
	}

	// 3) 拉子回复(parent_id > 0,status IN (1,2))
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	sql := `SELECT id, post_id, parent_id, root_id, author_id, author_type,
	        author_name_snapshot, author_avatar_snapshot, content,
	        like_count, reply_count, status, reply_to_uid, reply_to_type,
	        DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') AS created_at
	        FROM comment
	        WHERE post_id = ? AND root_id = ? AND parent_id > 0 AND status IN (1, 2)
	        ORDER BY created_at ASC, id ASC LIMIT ?`
	var rows []parentRow
	if err := l.svcCtx.DBConn.QueryRowsCtx(l.ctx, &rows, sql, req.PostId, req.RootId, limit+1); err != nil {
		l.Errorf("list replies failed: post_id=%d, root_id=%d, err=%v", req.PostId, req.RootId, err)
		return nil, errorx.ErrInternal
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	// 4) 组 items + 收 comment_id(供 is_liked 批量查询)
	items := make([]*types.CommentItem, 0, len(rows))
	allIDs := make([]int64, 0, len(rows))
	for i := range rows {
		items = append(items, buildCommentItem(l.svcCtx, &rows[i], currentUID))
		allIDs = append(allIDs, rows[i].Id)
	}

	// 5) 批量 is_liked(仅登录用户),失败仅记日志,不阻塞主链路
	if currentUID != "" && len(allIDs) > 0 {
		ph, args := buildInClause(allIDs)
		likedSQL := `SELECT comment_id FROM comment_like
		             WHERE user_id = ? AND comment_id IN (` + ph + `)`
		likedArgs := append([]interface{}{currentUID}, args...)
		var likedIDs []int64
		if err := l.svcCtx.DBConn.QueryRowsCtx(l.ctx, &likedIDs, likedSQL, likedArgs...); err != nil {
			l.Errorf("查询子回复点赞集合失败: user_id=%s, err=%v", currentUID, err)
		} else {
			likedSet := make(map[int64]struct{}, len(likedIDs))
			for _, id := range likedIDs {
				likedSet[id] = struct{}{}
			}
			for _, it := range items {
				if _, ok := likedSet[it.Id]; ok {
					it.IsLiked = true
				}
			}
		}
	}

	return &types.CommentListResponse{
		Items:      items,
		HasMore:    hasMore,
		NextCursor: "", // cursor 分页当前未启用,用 offset/limit 代替
	}, nil
}
