package admin

import (
	"context"

	"local-life-api/internal/errorx"
	"local-life-api/internal/logic/shared/uploadpolicy"
	"local-life-api/internal/model"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type BannerCreateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewBannerCreateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *BannerCreateLogic {
	return &BannerCreateLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// BannerCreate 后台新建轮播图
//   - jump_type / jump_target 由 req 校验（options=0|1|2|3|4 + optional target）
//   - status 默认 1（正常），由 types 校验过
//   - image 落库前强校验: host 白名单 + 后缀白名单 + banner/ 路径前缀
func (l *BannerCreateLogic) BannerCreate(req *types.BannerCreateRequest) (resp *types.EmptyResponse, err error) {
	// 落库前: 单图 URL 强校验(host 白名单 + 后缀白名单 + banner/ 路径前缀)
	cleaned, err := uploadpolicy.ValidateSingleImage(req.Image, uploadpolicy.BizBanner, 0, []string{"jpg", "png", "webp"}, l.svcCtx.OSS.HostCfg)
	if err != nil {
		l.Errorf("banner 图片 URL 校验失败: image=%q, err=%v", req.Image, err)
		return nil, errorx.ErrInvalidParam
	}

	banner := &model.Banner{
		Image:      cleaned,
		JumpType:   int64(req.JumpType),
		JumpTarget: req.JumpTarget,
		SortOrder:  int64(req.SortOrder),
		Title:      req.Title,
		Status:     1, // 默认正常
	}

	if _, err := l.svcCtx.BannerModel.Insert(l.ctx, banner); err != nil {
		l.Errorf("创建轮播图失败: image=%q, err=%v", req.Image, err)
		return nil, errorx.ErrInternal
	}

	l.Infof("创建轮播图成功: id=%d", banner.Id)
	return &types.EmptyResponse{}, nil
}
