package user

import (
	"context"
	"database/sql"
	"fmt"

	"local-life-api/common/jwtx"
	"local-life-api/internal/errorx"
	"local-life-api/internal/model"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/sqlc"
)

type ShopUpdateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewShopUpdateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ShopUpdateLogic {
	return &ShopUpdateLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// ShopUpdate C 端更新自己的商家信息
//   - 校验 JWT → openid → user
//   - 校验 shop 所有权（user_id 匹配）
//   - 更新后状态回到 1（待审核），重新走审核流程
func (l *ShopUpdateLogic) ShopUpdate(req *types.ShopUpdateRequest) (resp *types.EmptyResponse, err error) {
	id := req.Id

	// 1. JWT
	openid, err := jwtx.GetOpenid(l.ctx)
	if err != nil {
		l.Errorf("JWT 解析失败: %v", err)
		return nil, errorx.ErrUnauthorized
	}

	// 2. 反查 user
	user, err := l.svcCtx.UserModel.FindOneByOpenid(l.ctx, openid)
	if err != nil {
		return nil, errorx.ErrUnauthorized
	}
	if user.Status != 1 {
		return nil, errorx.ErrUserBanned
	}

	// 3. 查 shop，校验所有权
	shop, err := l.svcCtx.ShopModel.FindOne(l.ctx, uint64(id))
	if err != nil {
		if err == sqlc.ErrNotFound || err == model.ErrNotFound {
			return nil, errorx.ErrShopNotFound
		}
		l.Errorf("查询商家失败: id=%d, err=%v", id, err)
		return nil, errorx.ErrInternal
	}
	if shop.UserId != user.Id {
		l.Errorf("无权编辑商家: shop.userId=%d, user.id=%d", shop.UserId, user.Id)
		return nil, fmt.Errorf("%w: 只能编辑自己的商家", errorx.ErrUnauthorized)
	}

	// 4. 校验手机号
	if !phoneRE.MatchString(req.Phone) {
		return nil, errorx.ErrInvalidParam
	}

	// 5. 校验 images
	images, err := ValidateAndCleanImages(req.Images, BizShop, int64(user.Id), shopImgExts, l.svcCtx.OSS.HostCfg)
	if err != nil {
		return nil, errorx.ErrInvalidParam
	}

	// 6. reverse geocoding(失败不阻塞,5 字段留空)
	//   坐标变化时重新解析 → 让 DB 的 city_adcode/area_adcode 与最新坐标对齐
	cityAdcode, cityName, areaAdcode, areaName, streetName := ResolveGeoFields(l.ctx, l.svcCtx, req.Latitude, req.Longitude)
	if areaAdcode == "" || areaName == "" || streetName == "" || cityAdcode == "" || cityName == "" {
		l.Infof("[shop_update] WARN geo fields partial: id=%d, lat=%f, lng=%f, "+
			"city_adcode=%q city_name=%q area_adcode=%q area_name=%q street_name=%q",
			id, req.Latitude, req.Longitude, cityAdcode, cityName, areaAdcode, areaName, streetName)
	}

	// 7. 更新字段,状态回到待审核
	shop.Name = req.Name
	shop.Category = int64(req.Category)
	shop.Address = req.Address
	shop.Latitude = req.Latitude
	shop.Longitude = req.Longitude
	shop.AreaAdcode = areaAdcode
	shop.AreaName = areaName
	shop.StreetName = streetName
	shop.CityAdcode = cityAdcode
	shop.CityName = cityName
	shop.Phone = req.Phone
	shop.Description = sql.NullString{String: req.Description, Valid: req.Description != ""}
	shop.Images = sql.NullString{String: images, Valid: images != ""}
	shop.Status = 1
	shop.RejectReason = ""

	if err := l.svcCtx.ShopModel.Update(l.ctx, shop); err != nil {
		l.Errorf("更新商家失败: id=%d, err=%v", id, err)
		return nil, errorx.ErrInternal
	}

	l.Infof("商家更新成功(id=%d, 待审核)", id)
	return &types.EmptyResponse{}, nil
}
