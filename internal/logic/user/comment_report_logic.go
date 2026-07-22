package user

import (
	"context"
	"errors"

	"local-life-api/internal/errorx"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/sqlc"
	xerrors "github.com/zeromicro/x/errors"
)

type CommentReportLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewCommentReportLogic(ctx context.Context, svcCtx *svc.ServiceContext) *CommentReportLogic {
	return &CommentReportLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// defaultReportThreshold 项目默认自隐藏阈值兜底
//   - 当 config.ReportThreshold 未配置或 <=0 时使用
const defaultReportThreshold = 3

// CommentReport 举报评论 + 达阈值自动隐藏(status=3 审核中)
//   - 登录校验: uid 空 → 401
//   - 前置校验: 评论必须存在(status=1)→ 否则 404 / 400
//   - 幂等: comment_report (comment_id, reporter_uid) 唯一键 + ON DUPLICATE KEY UPDATE
//     重复举报 RowsAffected=0 → 返回 already_reported=true 且不再累加 report_count
//   - 首次举报: report_count+1, 达阈值时 status 置 3(审核中)
//   - 计数与状态切换合并到一条原子 UPDATE(去 read-modify-write race)
func (l *CommentReportLogic) CommentReport(req *types.CommentReportRequest) (*types.CommentReportResponse, error) {
	// 1) 登录校验
	uid, role := currentUserFromCtx(l.ctx)
	if uid == "" {
		return nil, xerrors.New(401, "请先登录")
	}

	// 2) 前置校验: 评论必须存在且未删除(status=1 正常;2 已删/3 审核中都不可举报)
	var commentStatus int
	if err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &commentStatus,
		`SELECT status FROM comment WHERE id = ?`, req.Id,
	); err != nil {
		if errors.Is(err, sqlc.ErrNotFound) {
			return nil, xerrors.New(404, "评论不存在")
		}
		l.Errorf("read comment status failed: comment_id=%d, err=%v", req.Id, err)
		return nil, errorx.ErrInternal
	}
	if commentStatus == 2 {
		return nil, xerrors.New(400, "评论已删除,无法举报")
	}

	threshold := l.svcCtx.Config.ReportThreshold
	if threshold <= 0 {
		threshold = defaultReportThreshold
	}

	// 3) INSERT 举报记录(幂等);唯一键 (comment_id, reporter_uid) 命中时 no-op
	res, err := l.svcCtx.DBConn.ExecCtx(l.ctx,
		`INSERT INTO comment_report (comment_id, reporter_uid, reporter_type, reason, description, status, created_at)
		 VALUES (?, ?, ?, ?, ?, 1, NOW())
		 ON DUPLICATE KEY UPDATE comment_id = comment_id`,
		req.Id, uid, role, req.Reason, req.Description,
	)
	if err != nil {
		l.Errorf("insert comment_report failed: comment_id=%d, user_id=%s, err=%v", req.Id, uid, err)
		return nil, errorx.ErrInternal
	}

	// RowsAffected=0 → 已存在(被 ON DUPLICATE KEY UPDATE 拦截),不累加计数
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return &types.CommentReportResponse{AlreadyReported: true}, nil
	}

	// 4) 原子累加 report_count;达阈值时 status 置 3(审核中)
	//    MySQL: report_count + 1 是单条 UPDATE,无读改分离 race;
	//    IF() 同一语句内判定,避免「先 SET report_count = N → 另一并发 SET report_count = N+1 → status 漏判」
	if _, err := l.svcCtx.DBConn.ExecCtx(l.ctx,
		`UPDATE comment
		 SET report_count = report_count + 1,
		     status = IF(report_count + 1 >= ?, 3, status)
		 WHERE id = ? AND status IN (1, 3)`,
		threshold, req.Id,
	); err != nil {
		// 影子错误: 举报已落库,计数更新失败不影响业务返回
		l.Errorf("update comment report_count failed: comment_id=%d, err=%v", req.Id, err)
	}

	return &types.CommentReportResponse{AlreadyReported: false}, nil
}
