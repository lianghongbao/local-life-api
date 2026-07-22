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

type AdminCommentFeedChangeStatusLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAdminCommentFeedChangeStatusLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AdminCommentFeedChangeStatusLogic {
	return &AdminCommentFeedChangeStatusLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// AdminCommentFeedChangeStatus admin feed 改 comment 状态
//   - comment.status 真实语义: 1=正常 2=已删除 3=审核中
//   - hide    1 → 3 (审核中,前端不可见,admin feed 仍可见)
//   - restore 3 → 1 (恢复);status=2(已删除) 不允许 restore(防陈年脏评论复活)
//   - delete  1|3 → 2 (影子删除)
//   - 不做物理删,二级不被级联改 status
//   - 写 admin_operation_log(影子,表若不存在 _ = 忽略;参照 audit logic)
func (l *AdminCommentFeedChangeStatusLogic) AdminCommentFeedChangeStatus(req *types.AdminCommentFeedChangeStatusReq) (resp *types.AdminCommentFeedChangeStatusResp, err error) {
	// 1) admin 身份校验
	adminID, _, err := jwtx.GetAdminIdentity(l.ctx)
	if err != nil || adminID == 0 {
		return nil, xerrors.New(401, "需要管理员权限")
	}

	// 2) 校验 comment 存在
	var curStatus int
	if err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &curStatus,
		`SELECT status FROM comment WHERE id = ?`, req.CommentId,
	); err != nil {
		if errors.Is(err, sqlc.ErrNotFound) {
			return nil, xerrors.New(404, "评论不存在")
		}
		l.Errorf("查询 comment 失败: id=%d, err=%v", req.CommentId, err)
		return nil, errorx.ErrInternal
	}

	// 3) 按 action 分支(乐观锁:UPDATE WHERE status = ? 受影响 0 行则 409)
	switch req.Action {
	case "hide":
		if curStatus != 1 {
			return nil, xerrors.New(409, "仅正常状态(status=1)可隐藏")
		}
		res, err := l.svcCtx.DBConn.ExecCtx(l.ctx,
			`UPDATE comment SET status = 3, deleted_by = 'admin', updated_at = NOW()
			 WHERE id = ? AND status = 1`, req.CommentId,
		)
		if err != nil {
			l.Errorf("hide 失败: id=%d, err=%v", req.CommentId, err)
			return nil, errorx.ErrInternal
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, xerrors.New(409, "评论状态已被其他管理员改动")
		}

	case "restore":
		// 仅允许 审核中(3) → 正常(1);status=2(已删除) 不允许
		if curStatus != 3 {
			return nil, xerrors.New(409, "仅审核中状态(status=3)可恢复")
		}
		res, err := l.svcCtx.DBConn.ExecCtx(l.ctx,
			`UPDATE comment SET status = 1, deleted_by = '', updated_at = NOW()
			 WHERE id = ? AND status = 3`, req.CommentId,
		)
		if err != nil {
			l.Errorf("restore 失败: id=%d, err=%v", req.CommentId, err)
			return nil, errorx.ErrInternal
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, xerrors.New(409, "评论状态已被其他管理员改动")
		}

	case "delete":
		if curStatus == 2 {
			return nil, xerrors.New(409, "评论已是删除状态")
		}
		res, err := l.svcCtx.DBConn.ExecCtx(l.ctx,
			`UPDATE comment SET status = 2, deleted_by = 'admin', updated_at = NOW()
			 WHERE id = ? AND status != 2`, req.CommentId,
		)
		if err != nil {
			l.Errorf("delete 失败: id=%d, err=%v", req.CommentId, err)
			return nil, errorx.ErrInternal
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, xerrors.New(409, "评论状态已被其他管理员改动")
		}

	default:
		return nil, xerrors.New(400, "未知 action")
	}

	// 4) 写 admin_operation_log(影子;表若不存在 _ = 忽略)
	_, _ = l.svcCtx.DBConn.ExecCtx(l.ctx,
		`INSERT INTO admin_operation_log (admin_id, action, target_type, target_id, reason, created_at)
		 VALUES (?, ?, 'comment', ?, ?, NOW())`,
		adminID, "comment_feed_"+req.Action, req.CommentId, req.Reason,
	)

	l.Infof("admin comment feed status 改动成功: admin_id=%d, comment_id=%d, action=%s, from=%d",
		adminID, req.CommentId, req.Action, curStatus)

	return &types.AdminCommentFeedChangeStatusResp{Ok: true}, nil
}
