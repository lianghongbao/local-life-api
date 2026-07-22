package admin

import (
	"context"

	"local-life-api/common/jwtx"
	"local-life-api/internal/errorx"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
	xerrors "github.com/zeromicro/x/errors"
)

type AdminCommentFeedListLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAdminCommentFeedListLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AdminCommentFeedListLogic {
	return &AdminCommentFeedListLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// AdminCommentFeedRow DB 扫描行
//   - reply_count 用 (SELECT COUNT(*) FROM comment WHERE parent_id = c.id AND status != 3) 子查询
//     "status != 3" 而非 "IN(1,2)" — 因为排查"二级是否被删"时,status=2(已删)也算历史
type AdminCommentFeedRow struct {
	Id           uint64 `db:"id"`
	ParentId     uint64 `db:"parent_id"`
	ReplyToUid   string `db:"reply_to_uid"`
	ReplyToName  string `db:"reply_to_name"`
	Content      string `db:"content"`
	Status       int    `db:"status"`
	AuthorType   int    `db:"author_type"`
	AuthorId     string `db:"author_id"`
	AuthorName   string `db:"author_name_snapshot"`
	AuthorAvatar string `db:"author_avatar_snapshot"`
	PostId       uint64 `db:"post_id"`
	PostTitle    string `db:"post_title"`
	ReplyCount   int    `db:"reply_count"`
}

// AdminCommentFeedList 后台评论 feed 一级列表
//   - SELECT 一级评论 (parent_id = 0), JOIN post_info 拿 title
//   - 7 字段筛选: post_id / author_type / status / keyword / date_from / date_to + 分页
//   - sort: ctime DESC
//   - 不缓存（admin 改动频次低,要求强一致）
func (l *AdminCommentFeedListLogic) AdminCommentFeedList(req *types.AdminCommentFeedListReq) (resp *types.AdminCommentFeedListResp, err error) {
	// 1) admin 身份校验
	adminID, _, err := jwtx.GetAdminIdentity(l.ctx)
	if err != nil || adminID == 0 {
		return nil, xerrors.New(401, "需要管理员权限")
	}
	_ = adminID // 当前未写 audit log,保留以备将来

	// 2) date 合法性校验
	if req.DateFrom != "" && req.DateTo != "" && req.DateFrom > req.DateTo {
		return nil, xerrors.New(400, "date_from 不能晚于 date_to")
	}

	// 3) 分页兜底
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

	// 4) WHERE 构造(共享给 COUNT 与 LIST)
	whereSQL := ` WHERE c.parent_id = 0 `
	args := []interface{}{}

	if req.PostId > 0 {
		whereSQL += ` AND c.post_id = ? `
		args = append(args, req.PostId)
	}
	if req.AuthorType > 0 {
		whereSQL += ` AND c.author_type = ? `
		args = append(args, req.AuthorType)
	}
	if req.Status > 0 {
		whereSQL += ` AND c.status = ? `
		args = append(args, req.Status)
	}
	if req.Keyword != "" {
		// 参数化 LIKE;% 和 _ 是 MySQL LIKE 通配,admin 后台可接受
		whereSQL += ` AND c.content LIKE ? `
		args = append(args, "%"+req.Keyword+"%")
	}
	if req.DateFrom != "" {
		whereSQL += ` AND c.created_at >= ? `
		args = append(args, req.DateFrom+" 00:00:00")
	}
	if req.DateTo != "" {
		whereSQL += ` AND c.created_at <= ? `
		args = append(args, req.DateTo+" 23:59:59")
	}

	// 5) COUNT
	var total int64
	if err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &total,
		`SELECT COUNT(c.id) FROM comment c `+whereSQL, args...,
	); err != nil {
		l.Errorf("admin feed list count 失败: err=%v", err)
		return nil, errorx.ErrInternal
	}
	if total == 0 {
		return &types.AdminCommentFeedListResp{
			List:  []types.AdminCommentFeedItem{},
			Total: 0,
		}, nil
	}

	// 6) LIST — JOIN post + reply_count 子查询
	listSQL := `SELECT c.id, c.parent_id, c.reply_to_uid, '' AS reply_to_name,
	             c.content, c.status, c.author_type, c.author_id,
	             c.author_name_snapshot, c.author_avatar_snapshot,
	             c.post_id, p.title AS post_title,
	             (SELECT COUNT(*) FROM comment WHERE parent_id = c.id AND status != 3) AS reply_count
	             FROM comment c
	             LEFT JOIN post_info p ON p.id = c.post_id ` + whereSQL +
		` ORDER BY c.created_at DESC LIMIT ? OFFSET ?`
	listArgs := append(args, pageSize, offset)

	var rows []AdminCommentFeedRow
	if err := l.svcCtx.DBConn.QueryRowsCtx(l.ctx, &rows, listSQL, listArgs...); err != nil {
		l.Errorf("admin feed list query 失败: err=%v", err)
		return nil, errorx.ErrInternal
	}

	// 7) 拼装响应
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

	return &types.AdminCommentFeedListResp{
		List:  items,
		Total: total,
	}, nil
}

// statusTextOf 数字 → 中文标签
//   - comment.status 真实语义: 1=正常 2=已删除 3=审核中
func statusTextOf(status int) string {
	switch status {
	case 1:
		return "正常"
	case 2:
		return "已删除"
	case 3:
		return "审核中"
	default:
		return "未知"
	}
}
