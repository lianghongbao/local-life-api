package user

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"local-life-api/common/jwtx"
	"local-life-api/internal/errorx"
	"local-life-api/internal/model"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type MyShopsLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewMyShopsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *MyShopsLogic {
	return &MyShopsLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

type myShopRow struct {
	Id           uint64         `db:"id"`
	Name         string         `db:"name"`
	Category     int64          `db:"category"`
	Address      string         `db:"address"`
	Latitude     float64        `db:"latitude"`
	Longitude    float64        `db:"longitude"`
	Phone        string         `db:"phone"`
	Description  sql.NullString `db:"description"`
	Images       sql.NullString `db:"images"`
	Status       int64          `db:"status"`
	RejectReason string         `db:"reject_reason"`
	ViewCount    uint64         `db:"view_count"`
	AreaName     sql.NullString `db:"area_name"`
	AreaAdcode   string         `db:"area_adcode"`
	StreetName   string         `db:"street_name"`
	CityAdcode   string         `db:"city_adcode"`
	CreatedAt    string         `db:"created_at"`
}

const myShopsCacheTTL = 10 * time.Second

func myShopsCacheKey(userId int64, page, pageSize int) string {
	return fmt.Sprintf("my:shops:%d:p%d:s%d", userId, page, pageSize)
}

// MyShops 查询当前用户入驻的商家列表（所有状态，按时间倒序）
//
// 标识体系：JWT 取 openid → FindOneByOpenid 反查 uid → 用 uid 查 shop 表
func (l *MyShopsLogic) MyShops(req *types.PageRequest) (resp *types.ShopListResponse, err error) {
	// 1. JWT 取 openid
	openid, err := jwtx.GetOpenid(l.ctx)
	if err != nil {
		l.Errorf("JWT 解析失败: %v", err)
		return nil, errorx.ErrUnauthorized
	}

	// 2. 反查 user.id
	user, err := l.svcCtx.UserModel.FindOneByOpenid(l.ctx, openid)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil, errorx.ErrUnauthorized
		}
		l.Errorf("查询用户失败: openid=%s, err=%v", openid, err)
		return nil, errorx.ErrInternal
	}
	uid := int64(user.Id)

	// 3. 分页参数兜底
	page := req.Page
	pageSize := req.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	offset := (page - 1) * pageSize

	// 4. 查缓存
	cacheKey := myShopsCacheKey(uid, page, pageSize)
	cached, cacheErr := l.svcCtx.RedisCli.Get(cacheKey)
	if cacheErr == nil && cached != "" {
		var hit types.ShopListResponse
		if err := json.Unmarshal([]byte(cached), &hit); err == nil {
			return &hit, nil
		}
		l.Errorf("我的商家缓存反序列化失败: key=%s, err=%v", cacheKey, err)
	}

	// 5. 查总数
	var total int64
	countSQL := "SELECT COUNT(*) FROM `shop` WHERE `user_id` = ?"
	if err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &total, countSQL, uint64(uid)); err != nil {
		l.Errorf("查询我的商家总数失败: userId=%d, err=%v", uid, err)
		return nil, errorx.ErrInternal
	}

	// 6. 空结果短路
	if total == 0 {
		resp = &types.ShopListResponse{List: []types.ShopItem{}, Total: 0}
		l.cacheMyShops(cacheKey, resp)
		return resp, nil
	}

	// 7. 分页查询
	listSQL := `
		SELECT
			id, name, category, address, latitude, longitude, phone, description, images,
			status, reject_reason, view_count,
			area_name, area_adcode, street_name, city_adcode,
			DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') AS created_at
		FROM shop
		WHERE user_id = ?
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`

	var rows []myShopRow
	if err := l.svcCtx.DBConn.QueryRowsCtx(l.ctx, &rows, listSQL, uint64(uid), pageSize, offset); err != nil {
		l.Errorf("查询我的商家列表失败: userId=%d, err=%v", uid, err)
		return nil, errorx.ErrInternal
	}

	// 8. 映射到 ShopItem
	items := make([]types.ShopItem, len(rows))
	for i, r := range rows {
		items[i] = types.ShopItem{
			Id:           int64(r.Id),
			Name:         r.Name,
			Category:     int(r.Category),
			Address:      r.Address,
			Latitude:     r.Latitude,
			Longitude:    r.Longitude,
			Phone:        r.Phone,
			Description:  r.Description.String,
			Images:       BuildURLsInJSON(l.ctx, r.Images.String, l.svcCtx.OSS.Client),
			Status:       int(r.Status),
			RejectReason: r.RejectReason,
			ViewCount:    int(r.ViewCount),
			CreatedAt:    r.CreatedAt,
			AreaName:     r.AreaName.String,
			AreaAdcode:   r.AreaAdcode,
			StreetName:   r.StreetName,
			CityAdcode:   r.CityAdcode,
		}
	}

	resp = &types.ShopListResponse{List: items, Total: total}

	// 9. 写缓存
	l.cacheMyShops(cacheKey, resp)

	return resp, nil
}

func (l *MyShopsLogic) cacheMyShops(key string, resp *types.ShopListResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		l.Errorf("我的商家缓存序列化失败: key=%s, err=%v", key, err)
		return
	}
	if setErr := l.svcCtx.RedisCli.Setex(key, string(data), int(myShopsCacheTTL.Seconds())); setErr != nil {
		l.Errorf("我的商家缓存写入失败: key=%s, err=%v", key, setErr)
	}
}
