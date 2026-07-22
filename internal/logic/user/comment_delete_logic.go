package user

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

type CommentDeleteLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewCommentDeleteLogic(ctx context.Context, svcCtx *svc.ServiceContext) *CommentDeleteLogic {
	return &CommentDeleteLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// CommentDelete 删除评论(影子删除)
//   - 权限:作者本人(author_id==uid AND author_type==role)或 admin
//   - 幂等:评论 status=2(已删除) → 直接返回 200,不报错
//   - 物理数据保留:UPDATE status=2 + deleted_by(自删=uid,admin 删="admin")
//   - post.comment_count 不减(spec 决策,保留历史评论数)
//   - 未登录 → 401;非作者非 admin → 403;评论不存在 → 404
func (l *CommentDeleteLogic) CommentDelete(req *types.CommentDeleteRequest) (resp *types.EmptyResponse, err error) {
	// 1) 登录态(C 端)
	uid, role := currentUserFromCtx(l.ctx)
	if uid == "" {
		return nil, xerrors.New(401, "请先登录")
	}

	// 2) admin 检测(admin JWT 用单独 secret + role="admin")
	//   - GetAdminIdentity 内部已校验 role=admin 且 userId+username 完整
	//   - 这里只关心"是不是 admin",所以忽略返回的 id/username
	isAdmin := false
	if _, _, errAdmin := jwtx.GetAdminIdentity(l.ctx); errAdmin == nil {
		isAdmin = true
	}

	// 3) 查评论的 author + status + parent_id(用于子回复时回写父级 reply_count)
	var c struct {
		AuthorID   string `db:"author_id"`
		AuthorType int    `db:"author_type"`
		Status     int    `db:"status"`
		ParentID   int64  `db:"parent_id"`
	}
	rowSQL := `SELECT author_id, author_type, status, parent_id FROM comment WHERE id = ? LIMIT 1`
	if err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &c, rowSQL, req.Id); err != nil {
		if errors.Is(err, sqlc.ErrNotFound) {
			return nil, xerrors.New(404, "评论不存在")
		}
		l.Errorf("read comment failed: comment_id=%d, err=%v", req.Id, err)
		return nil, errorx.ErrInternal
	}

	// 4) 幂等保护:已删除评论直接返回 200
	if c.Status == 2 {
		return &types.EmptyResponse{}, nil
	}

	// 5) 权限校验:作者本人 OR admin
	isAuthor := c.AuthorID == uid && c.AuthorType == role
	if !isAuthor && !isAdmin {
		return nil, xerrors.New(403, "无权删除")
	}

	// 6) 影子删除:UPDATE status=2 + deleted_by
	//   - admin 删 → deleted_by = "admin"(避免把 admin id 暴露在 user 体系)
	//   - 自删 → deleted_by = uid(openid)
	deletedBy := uid
	if isAdmin && !isAuthor {
		deletedBy = "admin"
	}
	if _, err := l.svcCtx.DBConn.ExecCtx(l.ctx,
		`UPDATE comment SET status = 2, deleted_by = ?, updated_at = NOW() WHERE id = ?`,
		deletedBy, req.Id,
	); err != nil {
		l.Errorf("soft-delete comment failed: comment_id=%d, err=%v", req.Id, err)
		return nil, errorx.ErrInternal
	}

	// 注:post.comment_count 不减(spec §5.4.3 决策)
	//   - 影子删除保留历史评论数,避免后期恢复评论时计数错乱
	//   - 详情页 / 列表页用 status=2 过滤即可

	// 7) 子回复被删时,父级 reply_count - 1(影子错误,不阻塞主响应)
	//   - 只对 parent_id > 0 的子回复生效,一级评论删时不动 reply_count
	//   - if reply_count > 0 防止历史脏数据导致负数
	if c.ParentID > 0 {
		if _, err := l.svcCtx.DBConn.ExecCtx(l.ctx,
			`UPDATE comment SET reply_count = reply_count - 1 WHERE id = ? AND reply_count > 0`,
			c.ParentID,
		); err != nil {
			l.Errorf("decr parent.reply_count failed: parent_id=%d, err=%v", c.ParentID, err)
		}
	}

	return &types.EmptyResponse{}, nil
}