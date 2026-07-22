package user

import (
	"context"
	"errors"

	"local-life-api/internal/background"
	"local-life-api/internal/errorx"
	"local-life-api/internal/model"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type ShopDetailLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewShopDetailLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ShopDetailLogic {
	return &ShopDetailLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// shopVisibleStatus 对 C 端可见的 shop 状态：2=已通过
const shopVisibleStatus = 2

// ShopDetail C 端公开商家详情
//   - 仅展示 status=2（已通过）；其他状态对 C 端透明（统一返回 not-found）
//   - 通过 ShopModel 走缓存（cache:shop:id:{id}）
//   - view_count：直接读 Redis hash 累加值（业务完全走 Redis，不依赖 mysql.view_count）
//     详见 internal/background/view_count_flusher.go 头部注释。
func (l *ShopDetailLogic) ShopDetail(req *types.IdRequest) (resp *types.ShopItem, err error) {
	// 1. 通过 model 查 shop（命中 cache:shop:id:{id}）
	shop, err := l.svcCtx.ShopModel.FindOne(l.ctx, uint64(req.Id))
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil, errorx.ErrShopNotFound
		}
		l.Errorf("查询 shop 失败: id=%d, err=%v", req.Id, err)
		return nil, errorx.ErrInternal
	}

	// 2. 仅展示已通过；其他状态对 C 端透明（防信息泄露）
	if shop.Status != shopVisibleStatus {
		return nil, errorx.ErrShopNotFound
	}

	// 3. 浏览计数：业务进入 ShopDetail 时 HINCRBY + 标 dirty（一起原子提交）。
	//    IncrViewCount 内部封装：HINCRBY + best-effort SADD，失败返回降级 HGET 值。
	//    返回值即"当前真实 view_count"，直接给用户，**不叠加** mysql.view_count。
	viewCount := int(background.IncrViewCount(
		l.svcCtx.RedisCli,
		background.ShopViewKeyPrefix,
		background.ShopViewDirtyKey,
		uint64(req.Id),
	))

	// 4. 构造响应
	resp = &types.ShopItem{
		Id:          int64(shop.Id),
		Name:        shop.Name,
		Category:    int(shop.Category),
		Address:     shop.Address,
		Latitude:    shop.Latitude,
		Longitude:   shop.Longitude,
		Phone:       shop.Phone,
		Description: shop.Description.String,
		Images:      BuildURLsInJSON(l.ctx, shop.Images.String, l.svcCtx.OSS.Client),
		Status:      int(shop.Status),
		ViewCount:   viewCount,
		CreatedAt:   shop.CreatedAt.Format("2006-01-02 15:04:05"),
		AreaName:    shop.AreaName,
		AreaAdcode:  shop.AreaAdcode,
		StreetName:  shop.StreetName,
		CityAdcode:  shop.CityAdcode,
	}

	return resp, nil
}
