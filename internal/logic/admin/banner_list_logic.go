package admin

import (
	"context"

	"local-life-api/internal/errorx"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type BannerListLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewBannerListLogic(ctx context.Context, svcCtx *svc.ServiceContext) *BannerListLogic {
	return &BannerListLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// BannerList 后台轮播图列表
//   - 全量展示（含 status=2 下架），便于后台排查
//   - 按 sort_order ASC（值小靠前），同序按 id DESC
//   - 不缓存
func (l *BannerListLogic) BannerList(req *types.PageRequest) (resp *types.BannerListResponse, err error) {
	// 1. 分页兜底
	page := req.Page
	pageSize := req.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	offset := (page - 1) * pageSize

	// 2. COUNT
	var total int64
	if err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &total, "SELECT COUNT(*) FROM `banner`"); err != nil {
		l.Errorf("查询轮播图总数失败: err=%v", err)
		return nil, errorx.ErrInternal
	}

	if total == 0 {
		return &types.BannerListResponse{
			List:  []types.BannerItem{},
			Total: 0,
		}, nil
	}

	// 3. 分页查询
	listSQL := `
		SELECT
			id, image, jump_type, jump_target, sort_order, title, status,
			DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') AS created_at
		FROM banner
		ORDER BY sort_order ASC, id DESC
		LIMIT ? OFFSET ?
	`

	type bannerRow struct {
		Id         int64  `db:"id"`
		Image      string `db:"image"`
		JumpType   int64  `db:"jump_type"`
		JumpTarget string `db:"jump_target"`
		SortOrder  int64  `db:"sort_order"`
		Title      string `db:"title"`
		Status     int64  `db:"status"`
		CreatedAt  string `db:"created_at"`
	}

	var rows []bannerRow
	if err := l.svcCtx.DBConn.QueryRowsCtx(l.ctx, &rows, listSQL, pageSize, offset); err != nil {
		l.Errorf("查询轮播图列表失败: err=%v", err)
		return nil, errorx.ErrInternal
	}

	// 4. image: file_key → URL(永久有效)
	items := make([]types.BannerItem, len(rows))
	for i, r := range rows {
		items[i] = types.BannerItem{
			Id:         r.Id,
			Image:      l.svcCtx.OSS.Client.URL(r.Image),
			JumpType:   int(r.JumpType),
			JumpTarget: r.JumpTarget,
			SortOrder:  int(r.SortOrder),
			Title:      r.Title,
			Status:     int(r.Status),
			CreatedAt:  r.CreatedAt,
		}
	}

	return &types.BannerListResponse{
		List:  items,
		Total: total,
	}, nil
}
