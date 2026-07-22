package user

import (
	"context"
	"database/sql"
	"errors"

	"local-life-api/common/jwtx"
	"local-life-api/internal/errorx"
	"local-life-api/internal/model"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

// shopImgExts 商家证照图片后缀白名单
var shopImgExts = []string{"jpg", "jpeg", "png", "webp", "gif"}

type ShopCreateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewShopCreateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ShopCreateLogic {
	return &ShopCreateLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// shopCreateStatusInitial 新入驻商家状态：1=待审核
const shopCreateStatusInitial = 1

// ShopCreate C 端入驻商家
//
//  1. JWT 取 openid（业务标识符）
//  2. 反查 user → 拿 user.id 写外键 + 校验封禁
//  3. 校验 images 字段（host 白名单 + path 前缀 + 后缀白名单）
//  4. 写 Shop 表（status=1 待审核）
func (l *ShopCreateLogic) ShopCreate(req *types.ShopCreateRequest) (resp *types.EmptyResponse, err error) {
	// 1. JWT 取 openid
	openid, err := jwtx.GetOpenid(l.ctx)
	if err != nil {
		l.Errorf("JWT 解析失败: %v", err)
		return nil, errorx.ErrUnauthorized
	}

	// 2. 用 openid 反查 user
	user, err := l.svcCtx.UserModel.FindOneByOpenid(l.ctx, openid)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil, errorx.ErrUnauthorized
		}
		l.Errorf("查询用户失败: openid=%s, err=%v", openid, err)
		return nil, errorx.ErrInternal
	}
	if user.Status != 1 {
		l.Errorf("用户已被封禁: openid=%s, status=%d", openid, user.Status)
		return nil, errorx.ErrUserBanned
	}
	uid := int64(user.Id)

	// 3. 校验并规范化 images 字段
	images, err := ValidateAndCleanImages(req.Images, BizShop, uid, shopImgExts, l.svcCtx.OSS.HostCfg)
	if err != nil {
		l.Errorf("images 字段非法: openid=%s, raw=%q, err=%v", openid, req.Images, err)
		return nil, errorx.ErrInvalidParam
	}

	// 4. 校验商家电话手机号格式
	if !phoneRE.MatchString(req.Phone) {
		l.Errorf("商家电话手机号格式非法: openid=%s, phone=%q", openid, req.Phone)
		return nil, errorx.ErrInvalidParam
	}

	// 5. reverse geocoding(失败不阻塞主链路,5 字段留空字符串)
	cityAdcode, cityName, areaAdcode, areaName, streetName := ResolveGeoFields(l.ctx, l.svcCtx, req.Latitude, req.Longitude)
	if areaAdcode == "" || areaName == "" || streetName == "" || cityAdcode == "" || cityName == "" {
		l.Infof("[shop_create] WARN geo fields partial: openid=%s, lat=%f, lng=%f, "+
			"city_adcode=%q city_name=%q area_adcode=%q area_name=%q street_name=%q",
			openid, req.Latitude, req.Longitude, cityAdcode, cityName, areaAdcode, areaName, streetName)
	}

	// 6. 构造 Shop（sql.NullString 处理可空字段）
	shop := &model.Shop{
		UserId:      uint64(uid),
		Name:        req.Name,
		Category:    int64(req.Category),
		Address:     req.Address,
		Latitude:    req.Latitude,
		Longitude:   req.Longitude,
		AreaAdcode:  areaAdcode,
		AreaName:    areaName,
		StreetName:  streetName,
		CityAdcode:  cityAdcode,
		CityName:    cityName,
		Phone:       req.Phone,
		Description: sql.NullString{String: req.Description, Valid: req.Description != ""},
		Images:      sql.NullString{String: images, Valid: images != ""},
		Status:      shopCreateStatusInitial,
		ViewCount:   0,
	}

	// 7. INSERT
	if _, err = l.svcCtx.ShopModel.Insert(l.ctx, shop); err != nil {
		l.Errorf("入驻商家失败: openid=%s, err=%v", openid, err)
		return nil, errorx.ErrInternal
	}

	l.Infof("入驻商家成功: openid=%s, name=%s", openid, req.Name)
	return &types.EmptyResponse{}, nil
}
