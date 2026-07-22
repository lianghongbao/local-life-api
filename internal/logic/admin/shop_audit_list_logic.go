package admin

import (
	"context"
	"database/sql"

	"local-life-api/internal/errorx"
	"local-life-api/internal/logic/shared/uploadpolicy"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type ShopAuditListLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewShopAuditListLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ShopAuditListLogic {
	return &ShopAuditListLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// ShopAuditList 后台商家审核列表
//   - 全状态展示，待审核(status=1)优先 → created_at DESC
//   - 不缓存
func (l *ShopAuditListLogic) ShopAuditList(req *types.PageRequest) (resp *types.ShopListResponse, err error) {
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
	if err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &total, "SELECT COUNT(*) FROM `shop`"); err != nil {
		l.Errorf("查询 shop 审核总数失败: err=%v", err)
		return nil, errorx.ErrInternal
	}

	if total == 0 {
		return &types.ShopListResponse{
			List:  []types.ShopItem{},
			Total: 0,
		}, nil
	}

	// 3. 分页查询（JOIN user 拿发布者昵称/头像）
	listSQL := `
		SELECT
			s.id, s.user_id,
			u.nickname, u.avatar,
			s.name, s.category, s.address,
			s.latitude, s.longitude,
			s.phone, s.description, s.images,
			s.status, s.reject_reason, s.view_count,
			DATE_FORMAT(s.created_at, '%Y-%m-%d %H:%i:%s') AS created_at,
			DATE_FORMAT(s.updated_at, '%Y-%m-%d %H:%i:%s') AS updated_at,
			s.area_name, s.area_adcode, s.street_name, s.city_adcode, s.city_name
		FROM shop s
		LEFT JOIN user u ON u.id = s.user_id
		ORDER BY (s.status = 1) DESC, s.created_at DESC
		LIMIT ? OFFSET ?
	`

	type shopRow struct {
		Id           uint64  `db:"id"`
		UserId       uint64  `db:"user_id"`
		Nickname     string  `db:"nickname"`
		Avatar       string  `db:"avatar"`
		Name         string  `db:"name"`
		Category     int64   `db:"category"`
		Address      string  `db:"address"`
		Latitude     float64 `db:"latitude"`
		Longitude    float64 `db:"longitude"`
		Phone        string  `db:"phone"`
		Description  string         `db:"description"`
		Images       sql.NullString `db:"images"`
		Status       int64          `db:"status"`
		RejectReason sql.NullString `db:"reject_reason"`
		ViewCount    uint64  `db:"view_count"`
		CreatedAt    string  `db:"created_at"`
		UpdatedAt    string  `db:"updated_at"`
		AreaName     string  `db:"area_name"`
		AreaAdcode   string  `db:"area_adcode"`
		StreetName   string  `db:"street_name"`
		CityAdcode   string  `db:"city_adcode"`
		CityName     string  `db:"city_name"`
	}

	var rows []shopRow
	if err := l.svcCtx.DBConn.QueryRowsCtx(l.ctx, &rows, listSQL, pageSize, offset); err != nil {
		l.Errorf("查询 shop 审核列表失败: err=%v", err)
		return nil, errorx.ErrInternal
	}

	// 拼接 完整 URL(file_key → https://static.lamborai.com/...)
	avatarKeys := make([]string, len(rows))
	for i, r := range rows {
		avatarKeys[i] = r.Avatar
	}
	avatars := l.svcCtx.OSS.Client.URLs(avatarKeys)

	items := make([]types.ShopItem, len(rows))
	for i, r := range rows {
		images := uploadpolicy.BuildURLsInJSON(l.ctx, r.Images.String, l.svcCtx.OSS.Client)
		items[i] = types.ShopItem{
			Id:           int64(r.Id),
			UserId:       int64(r.UserId),
			Nickname:     r.Nickname,
			Avatar:       avatars[i],
			Name:         r.Name,
			Category:     int(r.Category),
			Address:      r.Address,
			Latitude:     r.Latitude,
			Longitude:    r.Longitude,
			Phone:        r.Phone,
			Description:  r.Description,
			Images:       images,
			Status:       int(r.Status),
			RejectReason: r.RejectReason.String,
			ViewCount:    int(r.ViewCount),
			CreatedAt:    r.CreatedAt,
			UpdatedAt:    r.UpdatedAt,
			AreaName:     r.AreaName,
			AreaAdcode:   r.AreaAdcode,
			StreetName:   r.StreetName,
			CityAdcode:   r.CityAdcode,
			CityName:     r.CityName,
		}
	}

	return &types.ShopListResponse{
		List:  items,
		Total: total,
	}, nil
}
