package admin

import (
	"context"
	"errors"

	"local-life-api/internal/errorx"
	"local-life-api/internal/logic/shared/uploadpolicy"
	"local-life-api/internal/model"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/sqlc"
)

type BannerUpdateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewBannerUpdateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *BannerUpdateLogic {
	return &BannerUpdateLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// BannerUpdate 后台更新轮播图
//   - 全字段覆盖(image/jump_type/jump_target/sort_order/title/status)
//   - image 落库前强校验:host 白名单(CDN 或 OSS)+ jpg/png/webp 后缀 + banner/ 路径前缀
//   - status 由 types 校验 oneof=1|2
func (l *BannerUpdateLogic) BannerUpdate(req *types.BannerUpdateRequest) (resp *types.EmptyResponse, err error) {
	banner, err := l.svcCtx.BannerModel.FindOne(l.ctx, uint64(req.Id))
	if err != nil {
		if errors.Is(err, sqlc.ErrNotFound) || errors.Is(err, model.ErrNotFound) {
			l.Infof("更新失败: 轮播图不存在, id=%d", req.Id)
			return nil, errorx.ErrNotFound
		}
		l.Errorf("查询轮播图失败: id=%d, err=%v", req.Id, err)
		return nil, errorx.ErrInternal
	}

	// 落库前: 单图 URL 强校验(host 白名单 + 后缀白名单 + banner/ 路径前缀)
	cleaned, err := uploadpolicy.ValidateSingleImage(req.Image, uploadpolicy.BizBanner, 0, []string{"jpg", "png", "webp"}, l.svcCtx.OSS.HostCfg)
	if err != nil {
		l.Errorf("banner 图片 URL 校验失败: image=%q, err=%v", req.Image, err)
		return nil, errorx.ErrInvalidParam
	}

	banner.Image = cleaned
	banner.JumpType = int64(req.JumpType)
	banner.JumpTarget = req.JumpTarget
	banner.SortOrder = int64(req.SortOrder)
	banner.Title = req.Title
	banner.Status = int64(req.Status)

	if err := l.svcCtx.BannerModel.Update(l.ctx, banner); err != nil {
		l.Errorf("更新轮播图失败: id=%d, err=%v", banner.Id, err)
		return nil, errorx.ErrInternal
	}

	l.Infof("更新轮播图成功: id=%d", banner.Id)
	return &types.EmptyResponse{}, nil
}
