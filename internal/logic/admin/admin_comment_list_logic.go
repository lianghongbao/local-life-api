package admin

import (
	"context"
	"fmt"
	"strings"

	"local-life-api/common/jwtx"
	"local-life-api/internal/errorx"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
	xerrors "github.com/zeromicro/x/errors"
)

type AdminCommentListLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAdminCommentListLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AdminCommentListLogic {
	return &AdminCommentListLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// AdminCommentRow LEFT JOIN post 拿 title;INNER JOIN comment_report 聚拢举报原因
type AdminCommentRow struct {
	Id             int64  `db:"id"`
	Content        string `db:"content"`
	Status         int    `db:"status"`
	PostId         int64  `db:"post_id"`
	PostTitle      string `db:"title"`
	AuthorId       string `db:"author_id"`
	AuthorType     int    `db:"author_type"`
	AuthorName     string `db:"author_name_snapshot"`
	AuthorAvatar   string `db:"author_avatar_snapshot"`
	ReportCount    int    `db:"report_count"`
	LatestReport   string `db:"latest_report"`
	ReasonSummary  string `db:"reason_summary"`
	PendingReports int64  `db:"pending_count"`
}

// reporterRow loadReporters 扫描行。name=COALESCE(user.nickname, shop.name);avatar 仅来自 user
type reporterRow struct {
	CommentID  int64  `db:"comment_id"`
	ReporterID string `db:"reporter_uid"`
	Type       int    `db:"reporter_type"`
	Name       string `db:"name"`
	Avatar     string `db:"avatar"`
}

// AdminCommentList 后台评论举报队列。按 filter_status 分 4 个 tab:
//   - 1=待处理 INNER JOIN report(status=1)
//   - 2=已隐藏 WHERE c.status=3(LEFT JOIN report,admin 可能直接 hide 无 report 的 comment)
//   - 3=已删除 WHERE c.status=2
//   - 4=已驳回 INNER JOIN report(status=3);keep action 不改 c.status,按 r.status 找
//
// 不缓存
func (l *AdminCommentListLogic) AdminCommentList(req *types.AdminCommentListRequest) (resp *types.AdminCommentListResponse, err error) {
	adminID, _, err := jwtx.GetAdminIdentity(l.ctx)
	if err != nil || adminID == 0 {
		return nil, xerrors.New(401, "需要管理员权限")
	}

	page := req.Page
	pageSize := req.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	// COUNT(DISTINCT c.id) 防同一 comment 被多条 report 重复统计
	var total int64
	countSQL := buildCountSQL(req.FilterStatus)
	countArgs := []interface{}{}
	if req.AuthorType > 0 {
		countSQL += ` AND c.author_type = ?`
		countArgs = append(countArgs, req.AuthorType)
	}
	if req.Keyword != "" {
		countSQL += ` AND c.content LIKE ?`
		countArgs = append(countArgs, "%"+req.Keyword+"%")
	}
	if err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &total, countSQL, countArgs...); err != nil {
		l.Errorf("admin 查询举报队列总数失败: err=%v", err)
		return nil, errorx.ErrInternal
	}

	if total == 0 {
		return &types.AdminCommentListResponse{
			List:  []types.AdminCommentReportItem{},
			Total: 0,
		}, nil
	}

	listSQL, orderExpr := buildListSQL(req.FilterStatus)
	args := []interface{}{}
	if req.AuthorType > 0 {
		listSQL += ` AND c.author_type = ?`
		args = append(args, req.AuthorType)
	}
	if req.Keyword != "" {
		listSQL += ` AND c.content LIKE ?`
		args = append(args, "%"+req.Keyword+"%")
	}
	// tab=2/3 没 report 关联,orderExpr="" → 直接按 c.id DESC 兜底
	if orderExpr != "" {
		listSQL += ` GROUP BY c.id ORDER BY ` + orderExpr + ` LIMIT ? OFFSET ?`
	} else {
		listSQL += ` GROUP BY c.id ORDER BY c.id DESC LIMIT ? OFFSET ?`
	}
	args = append(args, pageSize, offset)

	var rows []AdminCommentRow
	if err := l.svcCtx.DBConn.QueryRowsCtx(l.ctx, &rows, listSQL, args...); err != nil {
		l.Errorf("admin 查询举报队列失败: err=%v", err)
		return nil, errorx.ErrInternal
	}

	items := make([]types.AdminCommentReportItem, 0, len(rows))
	commentIDs := make([]int64, 0, len(rows))
	for _, r := range rows {
		items = append(items, types.AdminCommentReportItem{
			ReportId:       r.Id,
			CommentId:      r.Id,
			CommentContent: r.Content,
			CommentStatus:  r.Status,
			PostId:         r.PostId,
			PostTitle:      r.PostTitle,
			Author: types.CommentAuthor{
				Id:     r.AuthorId,
				Type:   r.AuthorType,
				Name:   r.AuthorName,
				Avatar: r.AuthorAvatar,
				IsShop: r.AuthorType == 2,
			},
			ReportCount:   r.ReportCount,
			Status:        req.FilterStatus, // 输出与 tab 一致,前端不用再推断
			ReportedAt:    r.LatestReport,
			ReasonSummary: r.ReasonSummary,
			Reporters:     []types.CommentAuthor{},
		})
		commentIDs = append(commentIDs, r.Id)
	}

	// 二次查询拉 reporter:user.reporter_uid JOIN user.openid;shop.reporter_uid CAST AS shop.id
	// reportStatus 由 filterStatus 推导:1→1(pending)、2/3→2(采纳)、4→3(已驳回)
	if len(commentIDs) > 0 {
		reporters, err := l.loadReporters(commentIDs, reportStatusOf(req.FilterStatus))
		if err != nil {
			l.Errorf("admin 查询 reporter 列表失败: err=%v", err)
			return nil, errorx.ErrInternal
		}
		for i := range items {
			if rs, ok := reporters[items[i].CommentId]; ok && len(rs) > 0 {
				items[i].Reporters = rs
			}
		}
	}

	return &types.AdminCommentListResponse{
		List:  items,
		Total: total,
	}, nil
}

// buildCountSQL 返回按 filter_status 分流的 COUNT 子句。
// 4 个 tab 跟 buildListSQL 共享同一套 comment/report 关联策略,保证 count 和 list 一致。
// tab=2/3 不 JOIN report(看 c.status 即可),tab=1/4 必须 JOIN report(看 r.status)。
func buildCountSQL(filterStatus int) string {
	switch filterStatus {
	case 2:
		// 已隐藏:admin 直接 hide 没 report 的 comment 也要算进去(纯 c.status)
		return `SELECT COUNT(DISTINCT c.id) FROM comment c WHERE c.status = 3`
	case 3:
		// 已删除:仅"被举报后被采纳删除"的(必须有 report.status=2 关联)
		return `SELECT COUNT(DISTINCT c.id) FROM comment c
		        INNER JOIN comment_report r ON r.comment_id = c.id AND r.status = 2
		        WHERE c.status = 2`
	case 4:
		// 已驳回:按 r.status=3 找(keep action 不改 c.status)
		return `SELECT COUNT(DISTINCT c.id) FROM comment c
		        INNER JOIN comment_report r ON r.comment_id = c.id AND r.status = 3`
	default:
		// 1=待处理:只有 pending report 的 comment
		return `SELECT COUNT(DISTINCT c.id) FROM comment c
		        INNER JOIN comment_report r ON r.comment_id = c.id AND r.status = 1
		        WHERE c.status IN (1, 3)`
	}
}

// reportStatusWhere 把 reportStatus 转成 SQL WHERE 子句:
//   - 0 = 哨兵值 "所有 status" → 返空串(让 caller 拼 "WHERE " + 空 + " AND ...")
//     为安全,实际用 "1=1" 占位让 SQL 语法合法。
//   - 其他 → "r.status = ?"
func reportStatusWhere(reportStatus int) string {
	if reportStatus == 0 {
		return "1=1"
	}
	return "r.status = ?"
}

// reportStatusOf filterStatus → comment_report.status 映射
//   - 1 → 1(pending)
//   - 2/3/4 → 0(哨兵:所有 status)
//     历史结果 tab 要展示所有举报过的人,不限 r.status,
//     否则老数据/手改会让 reporter 列空。
//   - 未知 → 1(兜底)
//
// 返回 0 时 loadReporters 跳过 WHERE r.status,等价"所有 status"。
func reportStatusOf(filterStatus int) int {
	switch filterStatus {
	case 2, 3, 4:
		return 0
	default:
		return 1
	}
}

// buildListSQL 返回 (selectClause, orderExpr) 两段:
//   - selectClause: 从 SELECT 到 WHERE 完毕,不含 ORDER/GROUP/LIMIT
//   - orderExpr: GROUP BY 后的 ORDER BY 表达式(tab=2/3 没 report 关联,返 "")
//
// 调用方负责拼 author_type / keyword / GROUP BY / LIMIT 子句,避免 4 个 case 重复同样的尾部逻辑。
func buildListSQL(filterStatus int) (string, string) {
	// tab=1/4 必须有 report(INNER);tab=2/3 admin 直接 hide/delete 没 report 的 comment 也要展示(LEFT)
	// LEFT JOIN 时 GROUP_CONCAT/MAX(r.created_at)/COUNT(r.id) 在没 report 时返 NULL/0,Scan 容忍。
	selectCols := `SELECT c.id, c.content, c.status, c.post_id, p.title,
	               c.author_id, c.author_type, c.author_name_snapshot, c.author_avatar_snapshot,
	               c.report_count,
	               COALESCE(DATE_FORMAT(MAX(r.created_at), '%Y-%m-%d %H:%i:%s'), '') AS latest_report,
	               COALESCE(GROUP_CONCAT(r.reason SEPARATOR ','), '') AS reason_summary,
	               COALESCE(COUNT(r.id), 0) AS pending_count
	               FROM comment c
	               LEFT JOIN post_info p ON p.id = c.post_id`

	switch filterStatus {
	case 2:
		// 已隐藏:admin 直接 hide 无 report 的 comment 也要展示(LEFT JOIN)
		return selectCols + ` LEFT JOIN comment_report r ON r.comment_id = c.id
		                      WHERE c.status = 3`, ""
	case 3:
		// 已删除:仅"被举报后被采纳删除"的(INNER JOIN report.status=2)
		//   排除作者自删/用户自删/admin 直接 delete 等没有 report 关联的 case
		//   (那些应该走"评论管理"模块,不是"评论举报"模块)
		return selectCols + ` INNER JOIN comment_report r ON r.comment_id = c.id AND r.status = 2
		                      WHERE c.status = 2`, "MAX(r.created_at) DESC"
	case 4:
		// 已驳回:keep action 改 r.status=3 但不改 c.status,按 r.status=3 找(INNER)
		return selectCols + ` INNER JOIN comment_report r ON r.comment_id = c.id
		                      WHERE r.status = 3`, "MAX(r.created_at) DESC"
	default:
		// 1=待处理:只有 pending report 且 comment 还可见/审核中(INNER)
		return selectCols + ` INNER JOIN comment_report r ON r.comment_id = c.id AND r.status = 1
		                      WHERE c.status IN (1, 3)`, "latest_report DESC"
	}
}

// loadReporters 批量拉一批 comment 的 reporter,按 comment_id 分组,每组最多 3 人
//   - reportStatus: 1=pending(待处理)/ 2=采纳(已隐藏/已删除)/ 3=已驳回
//   - tab=1 拉 r.status=1;tab=2/3 拉 r.status=2(admin 点了 hide/delete 后);
//     tab=4 拉 r.status=3(admin 点了 keep 后)
//   - avatar 走 normalizeAvatarURL 拼 CDN 前缀,避免 admin 域名下 404 灰色占位
func (l *AdminCommentListLogic) loadReporters(commentIDs []int64, reportStatus int) (map[int64][]types.CommentAuthor, error) {
	if len(commentIDs) == 0 {
		return map[int64][]types.CommentAuthor{}, nil
	}

	placeholders := make([]string, len(commentIDs))
	args := make([]interface{}, len(commentIDs))
	for i, id := range commentIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	sql := fmt.Sprintf(`SELECT r.comment_id, r.reporter_uid, r.reporter_type,
	             COALESCE(u.nickname, s.name, '') AS name,
	             COALESCE(u.avatar, '') AS avatar
	             FROM comment_report r
	             LEFT JOIN user u ON r.reporter_type = 1 AND u.openid = r.reporter_uid
	             LEFT JOIN shop s ON r.reporter_type = 2 AND s.id = CAST(r.reporter_uid AS UNSIGNED)
	             WHERE %s AND r.comment_id IN (%s)
	             ORDER BY r.comment_id, r.created_at DESC`,
		reportStatusWhere(reportStatus), strings.Join(placeholders, ","))
	// reportStatus>0 时作为第一个 ? 拼进去;reportStatus=0(哨兵"所有 status")无 ? 占位,不入 args
	if reportStatus > 0 {
		args = append([]interface{}{reportStatus}, args...)
	}

	var rrows []reporterRow
	if err := l.svcCtx.DBConn.QueryRowsCtx(l.ctx, &rrows, sql, args...); err != nil {
		return nil, err
	}

	for i := range rrows {
		rrows[i].Avatar = normalizeAvatarURL(l.svcCtx, rrows[i].Avatar)
	}

	return pickTopReporters(rrows, 3), nil
}

// normalizeAvatarURL file_key → CDN URL。已是 http(s):// 原样;空串/nil svcCtx 原样。
// 与 user/comment_list_logic.go 行为等价
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

// pickTopReporters in-memory 分组,每组最多 maxPerComment 人。空 reporter_id 跳过;cap<=0 兜底 3
func pickTopReporters(rows []reporterRow, maxPerComment int) map[int64][]types.CommentAuthor {
	const defaultCap = 3
	if maxPerComment <= 0 {
		maxPerComment = defaultCap
	}
	out := make(map[int64][]types.CommentAuthor, len(rows))
	for _, r := range rows {
		if r.ReporterID == "" {
			continue
		}
		if len(out[r.CommentID]) >= maxPerComment {
			continue
		}
		out[r.CommentID] = append(out[r.CommentID], toCommentAuthor(r))
	}
	return out
}

// toCommentAuthor reporterRow → CommentAuthor。Type=2 → IsShop=true
func toCommentAuthor(r reporterRow) types.CommentAuthor {
	return types.CommentAuthor{
		Id:     r.ReporterID,
		Type:   r.Type,
		Name:   r.Name,
		Avatar: r.Avatar,
		IsShop: r.Type == 2,
	}
}
