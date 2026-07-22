package admin

import (
	"context"

	"local-life-api/internal/errorx"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type NoticeListLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewNoticeListLogic(ctx context.Context, svcCtx *svc.ServiceContext) *NoticeListLogic {
	return &NoticeListLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// NoticeList B 端后台公告列表
//   - 不限 status（管理员要看全部：正常/下架/草稿）
//   - 不走缓存(后台编辑要立即看到,缓存会与 DB 状态短时不一致)
//   - 置顶优先，再按 created_at DESC
func (l *NoticeListLogic) NoticeList(req *types.PageRequest) (resp *types.NoticeListResponse, err error) {
	// 1. 分页参数兜底
	page := req.Page
	pageSize := req.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	offset := (page - 1) * pageSize

	// 2. 查总数（不限状态）
	var total int64
	countSQL := "SELECT COUNT(*) FROM `notice`"
	if err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &total, countSQL); err != nil {
		l.Errorf("查询公告总数失败: err=%v", err)
		return nil, errorx.ErrInternal
	}

	// 3. 空结果短路
	if total == 0 {
		return &types.NoticeListResponse{
			List:  []types.NoticeItem{},
			Total: 0,
		}, nil
	}

	// 4. 分页查询（置顶优先 + 时间倒序，不限 status）
	listSQL := `
		SELECT
			id, title, content, is_top, status,
			DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') AS created_at,
			DATE_FORMAT(updated_at, '%Y-%m-%d %H:%i:%s') AS updated_at
		FROM notice
		ORDER BY is_top DESC, created_at DESC
		LIMIT ? OFFSET ?
	`

	type noticeRow struct {
		Id        int64  `db:"id"`
		Title     string `db:"title"`
		Content   string `db:"content"`
		IsTop     int64  `db:"is_top"`
		Status    int64  `db:"status"`
		CreatedAt string `db:"created_at"`
		UpdatedAt string `db:"updated_at"`
	}

	var rows []noticeRow
	if err := l.svcCtx.DBConn.QueryRowsCtx(l.ctx, &rows, listSQL, pageSize, offset); err != nil {
		l.Errorf("查询公告列表失败: err=%v", err)
		return nil, errorx.ErrInternal
	}

	// 5. 映射
	items := make([]types.NoticeItem, len(rows))
	for i, r := range rows {
		items[i] = types.NoticeItem{
			Id:        r.Id,
			Title:     r.Title,
			Content:   r.Content,
			IsTop:     int(r.IsTop),
			Status:    int(r.Status),
			CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt,
		}
	}

	return &types.NoticeListResponse{
		List:  items,
		Total: total,
	}, nil
}
