package user

import (
	"context"
	"strings"

	"local-life-api/common/jwtx"
	"local-life-api/internal/errorx"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

// pickTopReplies 在子回复列表里取前 max 条
func pickTopReplies(items []*types.CommentItem, max int) []*types.CommentItem {
	if len(items) <= max {
		return items
	}
	return items[:max]
}

// normalizeAvatarURL 把 DB 存的 avatar 字段统一转成前端可直接加载的 URL。
//   - 已含 http(s):// 前缀视为完整 URL,原样返回(容错历史 snapshot 已被拼接过的场景)
//   - 裸 file_key → 走 OSS.Client.URL 拼 CDN 前缀
//   - 空串 / nil svcCtx: 原样返回,前端用 cat avatar 兜底
func normalizeAvatarURL(svcCtx *svc.ServiceContext, raw string) string {
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	if svcCtx != nil {
		return svcCtx.OSS.Client.URL(raw)
	}
	return raw
}

// parentRow 是从 SQL 扫出的评论行结构(父/子共用)
//   - 字段顺序必须与 SELECT 一致,字段个数必须严格匹配(go-zero sqlx strict mode)
//   - CreatedAt 用 string + DATE_FORMAT,与项目其他 logic 一致
//   - ParentId/RootId 在父评论上 ParentId=0、RootId=自增 id;
//     在子回复上 ParentId=direct parent、RootId=顶层一级评论 id(aggregation 锚点)
type parentRow struct {
	Id           int64  `db:"id"`
	PostId       int64  `db:"post_id"`
	ParentId     int64  `db:"parent_id"`
	RootId       int64  `db:"root_id"`
	AuthorId     string `db:"author_id"`
	AuthorType   int    `db:"author_type"`
	AuthorName   string `db:"author_name_snapshot"`
	AuthorAvatar string `db:"author_avatar_snapshot"`
	Content      string `db:"content"`
	LikeCount    int    `db:"like_count"`
	ReplyCount   int    `db:"reply_count"`
	Status       int    `db:"status"`
	ReplyToUid   string `db:"reply_to_uid"`
	ReplyToType  int    `db:"reply_to_type"`
	CreatedAt    string `db:"created_at"`
}

// topReplyMax 一个一级评论下,列表里只展示前 N 条子回复(避免三屏滚动)
// 全部子回复由 CommentReplies 接口接管
const topReplyMax = 3

// buildInClause 拼 "(?,?,?)" 和 []interface{}
// 长度 0 时返回 ("NULL", nil),调用方需先检查 len>0 跳过整段查询
func buildInClause(ids []int64) (string, []interface{}) {
	if len(ids) == 0 {
		return "NULL", nil
	}
	ph := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args[i] = id
	}
	return strings.Join(ph, ","), args
}

// buildCommentItem 把 sqlx 出来的 row 转成 *types.CommentItem
//   - currentUID 是当前登录 openid(空=未登录,影响 is_mentioned_me 判定)
//   - IsMentionedMe 仅在 reply_to_uid == currentUID 时置 true,避免泄露他人的 @
func buildCommentItem(svcCtx *svc.ServiceContext, r *parentRow, currentUID string) *types.CommentItem {
	avatar := normalizeAvatarURL(svcCtx, r.AuthorAvatar)
	// 已删除评论(status=2)正文不再下发,避免用户/admin 删除内容随接口泄露;C 端仅作"楼层占位"展示
	content := r.Content
	if r.Status == 2 {
		content = ""
	}
	it := &types.CommentItem{
		Id:         r.Id,
		PostId:     r.PostId,
		ParentId:   r.ParentId,
		RootId:     r.RootId,
		Content:    content,
		LikeCount:  r.LikeCount,
		ReplyCount: r.ReplyCount,
		Status:     r.Status,
		CreatedAt:  r.CreatedAt,
		Author: types.CommentAuthor{
			Id:     r.AuthorId,
			Type:   r.AuthorType,
			Name:   r.AuthorName,
			Avatar: avatar,
			IsShop: r.AuthorType == 2,
		},
	}
	if r.ReplyToUid != "" {
		it.ReplyToUser = &types.CommentAuthor{
			Id:   r.ReplyToUid,
			Type: r.ReplyToType,
		}
		// name/avatar 不在这里连表查(性能),前端如需展示被 @ 人姓名,可用 top_replies 已拉到的子项代替
	}
	if r.ReplyToUid != "" && r.ReplyToUid == currentUID {
		it.IsMentionedMe = true
	}
	return it
}

// currentUserFromCtx 取出当前登录用户的 (openid, author_type)
//   - 未登录 / 解析失败 → 返回 ("", 0),调用方决定是否 401
//   - 用 jwtx.FromContext(项目既有用法)而非裸 ctx.Value,避免依赖 string 类型断言细节
//   - 共享给 CommentCreate / CommentList / CommentReplies / CommentDelete / CommentLike / CommentReport
func currentUserFromCtx(ctx context.Context) (string, int) {
	c, err := jwtx.FromContext(ctx)
	if err != nil || c == nil || c.Openid == "" {
		return "", 0
	}
	// JWT role 是字符串("user"/"admin"/"dev");目前 C 端登录只产出 "user"
	// 未来若加商家端登录(role="shop")直接加分支即可
	authorType := 1
	if c.Role == "shop" {
		authorType = 2
	}
	return c.Openid, authorType
}

// currentUID 从 ctx 取登录用户的 openid。未登录时返回 ""
func (l *CommentListLogic) currentUID() string {
	uid, _ := currentUserFromCtx(l.ctx)
	return uid
}

type CommentListLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewCommentListLogic(ctx context.Context, svcCtx *svc.ServiceContext) *CommentListLogic {
	return &CommentListLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// CommentList 帖子一级评论列表 + 每个一级评论下前 3 条子回复 + is_liked + is_mentioned_me
//   - 排序: 一级评论 created_at ASC(老评论在前,对话感)
//   - 分页: limit+1 法判定 hasMore
//   - 子回复: 一次 IN 拉所有候选,内存里按 root_id 分组
//   - is_liked: 一次 IN 拿当前用户点过赞的 comment_id set
//   - is_mentioned_me: 仅当 reply_to_uid == currentUID;未登录时一律 false
func (l *CommentListLogic) CommentList(req *types.CommentListRequest) (*types.CommentListResponse, error) {
	// 1) 拿 currentUID(未登录 → "")
	currentUID := l.currentUID()

	// 2) 拉父评论(parent_id=0 AND status IN (1,2))
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	var parents []parentRow
	parentSQL := `SELECT id, post_id, parent_id, root_id, author_id, author_type,
	             author_name_snapshot, author_avatar_snapshot, content,
	             like_count, reply_count, status, reply_to_uid, reply_to_type,
	             DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') AS created_at
	             FROM comment
	             WHERE post_id = ? AND parent_id = 0 AND status IN (1, 2)
	             ORDER BY created_at ASC, id ASC LIMIT ?`
	if err := l.svcCtx.DBConn.QueryRowsCtx(l.ctx, &parents, parentSQL, req.PostId, limit+1); err != nil {
		l.Errorf("查询父评论失败: post_id=%d, err=%v", req.PostId, err)
		return nil, errorx.ErrInternal
	}
	hasMore := len(parents) > limit
	if hasMore {
		parents = parents[:limit]
	}

	// 3) 组父 items + 收 root_id
	items := make([]*types.CommentItem, 0, len(parents))
	rootIDs := make([]int64, 0, len(parents))
	for i := range parents {
		items = append(items, buildCommentItem(l.svcCtx, &parents[i], currentUID))
		rootIDs = append(rootIDs, parents[i].Id)
	}

	// 4) 批量拉子回复(每个 root_id 下 status IN (1,2) 子回复)
	if len(rootIDs) > 0 {
		placeholders, args := buildInClause(rootIDs)
		childrenSQL := `SELECT id, post_id, parent_id, root_id, author_id, author_type,
		                author_name_snapshot, author_avatar_snapshot, content,
		                like_count, reply_count, status, reply_to_uid, reply_to_type,
		                DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') AS created_at
		                FROM comment
		                WHERE post_id = ? AND root_id IN (` + placeholders + `)
		                  AND parent_id > 0 AND status IN (1, 2)
		                ORDER BY created_at ASC, id ASC`
		childArgs := append([]interface{}{req.PostId}, args...)
		var children []parentRow
		if err := l.svcCtx.DBConn.QueryRowsCtx(l.ctx, &children, childrenSQL, childArgs...); err != nil {
			l.Errorf("查询子回复失败: post_id=%d, err=%v", req.PostId, err)
			return nil, errorx.ErrInternal
		}
		// 按 root_id 分组并累加 total
		grouped := map[int64][]*types.CommentItem{}
		totalByRoot := map[int64]int{}
		for i := range children {
			c := children[i]
			ci := buildCommentItem(l.svcCtx, &c, currentUID)
			grouped[c.RootId] = append(grouped[c.RootId], ci)
			totalByRoot[c.RootId]++
		}
		for _, item := range items {
			if cs, ok := grouped[item.Id]; ok {
				item.TopReplies = pickTopReplies(cs, topReplyMax)
				item.TotalReplies = totalByRoot[item.Id]
				item.HasMoreReplies = item.TotalReplies > len(item.TopReplies)
			}
		}
	}

	// 5) 批量 is_liked(仅登录用户),失败仅记日志,不阻塞主链路
	allCommentIDs := make([]int64, 0)
	for _, it := range items {
		allCommentIDs = append(allCommentIDs, it.Id)
		for _, sub := range it.TopReplies {
			allCommentIDs = append(allCommentIDs, sub.Id)
		}
	}
	if currentUID != "" && len(allCommentIDs) > 0 {
		placeholders, args := buildInClause(allCommentIDs)
		likeSQL := `SELECT comment_id FROM comment_like
		            WHERE user_id = ? AND comment_id IN (` + placeholders + `)`
		likeArgs := append([]interface{}{currentUID}, args...)
		var likedIDs []int64
		if err := l.svcCtx.DBConn.QueryRowsCtx(l.ctx, &likedIDs, likeSQL, likeArgs...); err != nil {
			l.Errorf("查询点赞集合失败: user_id=%s, err=%v", currentUID, err)
		} else {
			likedSet := make(map[int64]struct{}, len(likedIDs))
			for _, id := range likedIDs {
				likedSet[id] = struct{}{}
			}
			for _, it := range items {
				if _, ok := likedSet[it.Id]; ok {
					it.IsLiked = true
				}
				for _, sub := range it.TopReplies {
					if _, ok := likedSet[sub.Id]; ok {
						sub.IsLiked = true
					}
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
