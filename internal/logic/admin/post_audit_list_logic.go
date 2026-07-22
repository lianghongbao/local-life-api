package admin

import (
	"context"

	"local-life-api/internal/errorx"
	"local-life-api/internal/logic/shared/uploadpolicy"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type PostAuditListLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewPostAuditListLogic(ctx context.Context, svcCtx *svc.ServiceContext) *PostAuditListLogic {
	return &PostAuditListLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// buildPostAuditListSQL 返回审核列表分页查询 SQL。
//
// 所有可空 string 列统一 COALESCE(col, ''),确保 user 删号/字段为空时
// database/sql Scan 不会因 NULL→string 报错。
func buildPostAuditListSQL() string {
	return `
		SELECT
			p.id, p.user_id,
			COALESCE(u.nickname, '') AS nickname,
			COALESCE(u.avatar, '')   AS avatar,
			p.type, p.title, p.images, p.content,
			COALESCE(p.area, '')         AS area,
			p.latitude, p.longitude,
			COALESCE(p.contact, '')      AS contact,
			p.status,
			COALESCE(p.reject_reason, '') AS reject_reason,
			p.view_count,
			DATE_FORMAT(p.created_at, '%Y-%m-%d %H:%i:%s') AS created_at,
			DATE_FORMAT(p.updated_at, '%Y-%m-%d %H:%i:%s') AS updated_at,
			COALESCE(p.area_adcode, '')  AS area_adcode,
			COALESCE(p.area_name, '')    AS area_name,
			COALESCE(p.street_name, '')  AS street_name,
			COALESCE(p.city_adcode, '')  AS city_adcode,
			COALESCE(p.city_name, '')    AS city_name
		FROM post_info p
		LEFT JOIN user u ON u.id = p.user_id
		ORDER BY (p.status = 1) DESC, p.created_at DESC
		LIMIT ? OFFSET ?
	`
}

// PostAuditList 后台信息审核列表
//   - 全状态展示（含待审核/已通过/已下架/已拒绝），便于审核人员排查
//   - 排序：待审核(status=1)优先 → created_at DESC（审核队列体验）
//   - 不缓存
func (l *PostAuditListLogic) PostAuditList(req *types.PageRequest) (resp *types.PostListResponse, err error) {
	page := req.Page
	pageSize := req.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	offset := (page - 1) * pageSize

	var total int64
	if err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &total, "SELECT COUNT(*) FROM `post_info`"); err != nil {
		l.Errorf("查询 post 审核总数失败: err=%v", err)
		return nil, errorx.ErrInternal
	}

	if total == 0 {
		return &types.PostListResponse{
			List:  []types.PostItem{},
			Total: 0,
		}, nil
	}

	listSQL := buildPostAuditListSQL()

	type postRow struct {
		Id           uint64  `db:"id"`
		UserId       uint64  `db:"user_id"`
		Nickname     string  `db:"nickname"`
		Avatar       string  `db:"avatar"`
		Type         int64   `db:"type"`
		Title        string  `db:"title"`
		Images       string  `db:"images"`
		Content      string  `db:"content"`
		Area         string  `db:"area"`
		Latitude     float64 `db:"latitude"`
		Longitude    float64 `db:"longitude"`
		Contact      string  `db:"contact"`
		Status       int64   `db:"status"`
		RejectReason string  `db:"reject_reason"`
		ViewCount    uint64  `db:"view_count"`
		CreatedAt    string  `db:"created_at"`
		UpdatedAt    string  `db:"updated_at"`
		AreaAdcode   string  `db:"area_adcode"`
		AreaName     string  `db:"area_name"`
		StreetName   string  `db:"street_name"`
		CityAdcode   string  `db:"city_adcode"`
		CityName     string  `db:"city_name"`
	}

	var rows []postRow
	if err := l.svcCtx.DBConn.QueryRowsCtx(l.ctx, &rows, listSQL, pageSize, offset); err != nil {
		l.Errorf("查询 post 审核列表失败: err=%v", err)
		return nil, errorx.ErrInternal
	}

	// 拼接 完整 URL(file_key → https://static.lamborai.com/...)
	avatarKeys := make([]string, len(rows))
	for i, r := range rows {
		avatarKeys[i] = r.Avatar
	}
	avatars := l.svcCtx.OSS.Client.URLs(avatarKeys)

	items := make([]types.PostItem, len(rows))
	for i, r := range rows {
		images := uploadpolicy.BuildURLsInJSON(l.ctx, r.Images, l.svcCtx.OSS.Client)
		items[i] = types.PostItem{
			Id:           int64(r.Id),
			UserId:       int64(r.UserId),
			Nickname:     r.Nickname,
			Avatar:       avatars[i],
			Type:         int(r.Type),
			Title:        r.Title,
			Images:       images,
			Content:      r.Content,
			Area:         r.Area,
			Latitude:     r.Latitude,
			Longitude:    r.Longitude,
			Contact:      r.Contact,
			Status:       int(r.Status),
			RejectReason: r.RejectReason,
			ViewCount:    int(r.ViewCount),
			CreatedAt:    r.CreatedAt,
			UpdatedAt:    r.UpdatedAt,
			AreaAdcode:   r.AreaAdcode,
			AreaName:     r.AreaName,
			StreetName:   r.StreetName,
			CityAdcode:   r.CityAdcode,
			CityName:     r.CityName,
		}
	}

	return &types.PostListResponse{
		List:  items,
		Total: total,
	}, nil
}
