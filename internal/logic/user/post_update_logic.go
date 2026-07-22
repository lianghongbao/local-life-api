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

type PostUpdateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewPostUpdateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *PostUpdateLogic {
	return &PostUpdateLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// PostUpdate C 端编辑自己的发布信息
//   - 校验 JWT → openid → user
//   - 校验 post 所有权（user_id 匹配）
//   - 更新后状态回到 1（待审核），重新走审核流程
func (l *PostUpdateLogic) PostUpdate(req *types.PostUpdateRequest) (resp *types.EmptyResponse, err error) {
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

	// 3. 查 post，校验所有权
	post, err := l.svcCtx.PostInfoModel.FindOne(l.ctx, uint64(id))
	if err != nil {
		if err == sqlc.ErrNotFound || err == model.ErrNotFound {
			return nil, errorx.ErrPostNotFound
		}
		l.Errorf("查询 post 失败: id=%d, err=%v", id, err)
		return nil, errorx.ErrInternal
	}
	if post.UserId != user.Id {
		l.Errorf("无权编辑 post: post.userId=%d, user.id=%d", post.UserId, user.Id)
		return nil, fmt.Errorf("%w: 只能编辑自己的发布", errorx.ErrUnauthorized)
	}

	// 4. 校验手机号
	if !phoneRE.MatchString(req.Contact) {
		return nil, errorx.ErrInvalidParam
	}

	// 5. 校验 images
	images, err := ValidateAndCleanImages(req.Images, BizPost, int64(user.Id), postImgExts, l.svcCtx.OSS.HostCfg)
	if err != nil {
		return nil, errorx.ErrInvalidParam
	}

	// 6. reverse geocoding(失败不阻塞,5 字段留空)
	//   坐标变化时重新解析 → 让 DB 的 city_adcode/area_adcode 与最新坐标对齐
	cityAdcode, cityName, areaAdcode, areaName, streetName := ResolveGeoFields(l.ctx, l.svcCtx, req.Latitude, req.Longitude)
	if areaAdcode == "" || areaName == "" || streetName == "" || cityAdcode == "" || cityName == "" {
		l.Infof("[post_update] WARN geo fields partial: id=%d, lat=%f, lng=%f, "+
			"city_adcode=%q city_name=%q area_adcode=%q area_name=%q street_name=%q",
			id, req.Latitude, req.Longitude, cityAdcode, cityName, areaAdcode, areaName, streetName)
	}

	// 7. 更新字段,状态回到待审核
	post.Type = int64(req.Type)
	post.Title = req.Title
	post.Content = sql.NullString{String: req.Content, Valid: req.Content != ""}
	post.Images = sql.NullString{String: images, Valid: images != ""}
	post.Area = req.Area
	post.Latitude = req.Latitude
	post.Longitude = req.Longitude
	post.AreaAdcode = areaAdcode
	post.AreaName = areaName
	post.StreetName = streetName
	post.CityAdcode = cityAdcode
	post.CityName = cityName
	post.Contact = req.Contact
	post.Status = 1
	post.RejectReason = ""

	if err := l.svcCtx.PostInfoModel.Update(l.ctx, post); err != nil {
		l.Errorf("更新 post 失败: id=%d, err=%v", id, err)
		return nil, errorx.ErrInternal
	}

	l.Infof("信息更新成功(id=%d, 待审核)", id)
	return &types.EmptyResponse{}, nil
}
