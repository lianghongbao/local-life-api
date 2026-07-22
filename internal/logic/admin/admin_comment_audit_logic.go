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

type AdminCommentAuditLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAdminCommentAuditLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AdminCommentAuditLogic {
	return &AdminCommentAuditLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// AdminCommentAudit 后台评论审核决策
//   - action: keep / hide / delete (types 层已 validate oneof)
//   - keep   → comment.status=1 (恢复)+ 关联 pending/采纳的 report → status=3 (驳回)
//     适用场景:tab=1 驳回待处理举报;tab=2 恢复已隐藏;tab=3 恢复已删除(影子)
//   - hide   → comment.status=3 (审核中/隐藏), 关联 report → status=2 (采纳)
//   - delete → comment.status=2 (影子删除), 关联 report → status=2 (采纳)
//   - 顺手写 admin_operation_log(表若不存在则 _ = 忽略,审计非关键路径)
func (l *AdminCommentAuditLogic) AdminCommentAudit(req *types.AdminCommentAuditRequest) (resp *types.EmptyResponse, err error) {
	// 1) admin 身份校验
	adminID, _, err := jwtx.GetAdminIdentity(l.ctx)
	if err != nil || adminID == 0 {
		return nil, xerrors.New(401, "需要管理员权限")
	}

	// 2) 校验 comment 存在(避免对不存在的 id 误操作)
	var cStatus int
	if err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &cStatus,
		`SELECT status FROM comment WHERE id = ?`, req.Id,
	); err != nil {
		if errors.Is(err, sqlc.ErrNotFound) {
			return nil, xerrors.New(404, "评论不存在")
		}
		l.Errorf("查询 comment 失败: id=%d, err=%v", req.Id, err)
		return nil, errorx.ErrInternal
	}

	// 3) 按 action 分支
	switch req.Action {
	case "keep":
		// 恢复 comment + 驳回所有未结案 report
		//   - c.status: 任意 → 1(正常)
		//   - r.status: 1(pending)或 2(采纳) → 3(驳回);r.status=3(已驳回)不动(幂等)
		if _, err := l.svcCtx.DBConn.ExecCtx(l.ctx,
			`UPDATE comment SET status = 1, deleted_by = '', updated_at = NOW() WHERE id = ?`, req.Id,
		); err != nil {
			l.Errorf("恢复 comment 失败: id=%d, err=%v", req.Id, err)
			return nil, errorx.ErrInternal
		}
		if _, err := l.svcCtx.DBConn.ExecCtx(l.ctx,
			`UPDATE comment_report
			 SET status = 3, processed_at = NOW(), processed_by = ?
			 WHERE comment_id = ? AND status IN (1, 2)`,
			adminID, req.Id,
		); err != nil {
			l.Errorf("驳回举报失败: comment_id=%d, err=%v", req.Id, err)
			return nil, errorx.ErrInternal
		}

	case "hide":
		// comment.status=3(审核中/隐藏),关联 report → status=2(采纳)
		if _, err := l.svcCtx.DBConn.ExecCtx(l.ctx,
			`UPDATE comment SET status = 3, updated_at = NOW() WHERE id = ?`, req.Id,
		); err != nil {
			l.Errorf("隐藏 comment 失败: id=%d, err=%v", req.Id, err)
			return nil, errorx.ErrInternal
		}
		if _, err := l.svcCtx.DBConn.ExecCtx(l.ctx,
			`UPDATE comment_report
			 SET status = 2, processed_at = NOW(), processed_by = ?
			 WHERE comment_id = ? AND status = 1`,
			adminID, req.Id,
		); err != nil {
			l.Errorf("采纳举报失败 (hide): comment_id=%d, err=%v", req.Id, err)
			return nil, errorx.ErrInternal
		}

	case "delete":
		// comment.status=2(影子删除),deleted_by=admin,关联 report → status=2(采纳)
		if _, err := l.svcCtx.DBConn.ExecCtx(l.ctx,
			`UPDATE comment SET status = 2, deleted_by = 'admin', updated_at = NOW() WHERE id = ?`, req.Id,
		); err != nil {
			l.Errorf("删除 comment 失败: id=%d, err=%v", req.Id, err)
			return nil, errorx.ErrInternal
		}
		if _, err := l.svcCtx.DBConn.ExecCtx(l.ctx,
			`UPDATE comment_report
			 SET status = 2, processed_at = NOW(), processed_by = ?
			 WHERE comment_id = ? AND status = 1`,
			adminID, req.Id,
		); err != nil {
			l.Errorf("采纳举报失败 (delete): comment_id=%d, err=%v", req.Id, err)
			return nil, errorx.ErrInternal
		}

	default:
		// types 层已校验,但兜底防止前端绕过
		return nil, xerrors.New(400, "未知 action")
	}

	// 4) 写 admin_operation_log(影子;表若不存在则 _ = 忽略)
	_, _ = l.svcCtx.DBConn.ExecCtx(l.ctx,
		`INSERT INTO admin_operation_log (admin_id, action, target_type, target_id, reason, created_at)
		 VALUES (?, ?, 'comment', ?, ?, NOW())`,
		adminID, "comment_audit_"+req.Action, req.Id, req.Reason,
	)

	l.Infof("admin 评论审核成功: admin_id=%d, comment_id=%d, action=%s, reason=%q",
		adminID, req.Id, req.Action, req.Reason)

	return &types.EmptyResponse{}, nil
}
