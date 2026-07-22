package admin

import (
	"context"
	"errors"

	"local-life-api/common/jwtx"
	"local-life-api/internal/errorx"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/sqlc"
	xerrors "github.com/zeromicro/x/errors"
)

type AdminCommentFeedRepliesLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAdminCommentFeedRepliesLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AdminCommentFeedRepliesLogic {
	return &AdminCommentFeedRepliesLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// AdminCommentRootRow 一级评论头部信息(Drawer header sticky 用)
//   - 不带 status 过滤:即便一级被 hide(admin 视角 status=3),底部二级仍展示
type AdminCommentRootRow struct {
	Id           uint64 `db:"id"`
	Content      string `db:"content"`
	AuthorType   int    `db:"author_type"`
	AuthorName   string `db:"author_name_snapshot"`
	AuthorAvatar string `db:"author_avatar_snapshot"`
	Ctime        string `db:"created_at_text"`
	Status       int    `db:"status"`
}

// AdminCommentFeedReplies 后台二级回复列表 + 一级头部信息
//   - 二级过滤 status=3(已删);一级头部 status 不过滤,便于排查一级被 hide 后底下还有什么
//   - sort: ctime ASC(对话流自然顺序,顶层最新在最下)
//   - 不缓存
func (l *AdminCommentFeedRepliesLogic) AdminCommentFeedReplies(req *types.AdminCommentFeedRepliesReq) (resp *types.AdminCommentFeedRepliesResp, err error) {
	// 1) admin 身份校验
	if _, _, err := jwtx.GetAdminIdentity(l.ctx); err != nil {
		return nil, xerrors.New(401, "需要管理员权限")
	}

	// 2) 分页兜底
	page := req.Page
	pageSize := req.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize

	// 3) 一级 root query（不带 status 过滤）
	var root AdminCommentRootRow
	if err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &root,
		`SELECT c.id, c.content, c.author_type,
		        c.author_name_snapshot, c.author_avatar_snapshot,
		        DATE_FORMAT(c.created_at, '%Y-%m-%d %H:%i:%s') AS created_at_text,
		        c.status
		 FROM comment c WHERE c.id = ?`, req.CommentId,
	); err != nil {
		if errors.Is(err, sqlc.ErrNotFound) {
			return nil, xerrors.New(404, "一级评论不存在")
		}
		l.Errorf("admin reply 查询 root 失败: id=%d, err=%v", req.CommentId, err)
		return nil, errorx.ErrInternal
	}

	// 4) 二级 COUNT
	var total int64
	if err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &total,
		`SELECT COUNT(id) FROM comment WHERE parent_id = ? AND status != 3`,
		req.CommentId,
	); err != nil {
		l.Errorf("admin reply count 失败: err=%v", err)
		return nil, errorx.ErrInternal
	}

	// 5) 二级 LIST（status != 3 ASC by ctime,二级 reply_count 字段恒为 0）
	listSQL := `SELECT id, parent_id, reply_to_uid, '' AS reply_to_name,
	            content, status, author_type, author_id,
	            author_name_snapshot, author_avatar_snapshot,
	            post_id, '' AS post_title, 0 AS reply_count
	            FROM comment
	            WHERE parent_id = ? AND status != 3
	            ORDER BY created_at ASC LIMIT ? OFFSET ?`
	var rows []AdminCommentFeedRow
	if err := l.svcCtx.DBConn.QueryRowsCtx(l.ctx, &rows, listSQL,
		req.CommentId, pageSize, offset,
	); err != nil {
		l.Errorf("admin reply list 失败: err=%v", err)
		return nil, errorx.ErrInternal
	}

	items := make([]types.AdminCommentFeedItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, types.AdminCommentFeedItem{
			CommentId:    int64(r.Id),
			ParentId:     int64(r.ParentId),
			ReplyToUid:   r.ReplyToUid,
			ReplyToName:  r.ReplyToName,
			Content:      r.Content,
			Status:       r.Status,
			AuthorType:   r.AuthorType,
			AuthorId:     r.AuthorId,
			AuthorName:   r.AuthorName,
			AuthorAvatar: r.AuthorAvatar,
			PostId:       int64(r.PostId),
			PostTitle:    r.PostTitle,
			ReplyCount:   r.ReplyCount,
			StatusText:   statusTextOf(r.Status),
		})
	}

	return &types.AdminCommentFeedRepliesResp{
		List:             items,
		Total:            total,
		RootCtime:        root.Ctime,
		RootContent:      root.Content,
		RootAuthorName:   root.AuthorName,
		RootAuthorAvatar: root.AuthorAvatar,
		RootAuthorType:   root.AuthorType,
		RootStatus:       root.Status,
	}, nil
}
