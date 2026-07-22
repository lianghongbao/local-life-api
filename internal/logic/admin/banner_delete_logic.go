package admin

import (
	"context"
	"errors"

	"local-life-api/internal/errorx"
	"local-life-api/internal/model"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/sqlc"
)

type BannerDeleteLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewBannerDeleteLogic(ctx context.Context, svcCtx *svc.ServiceContext) *BannerDeleteLogic {
	return &BannerDeleteLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// BannerDelete 后台删除轮播图（硬删）
func (l *BannerDeleteLogic) BannerDelete(req *types.IdRequest) (resp *types.EmptyResponse, err error) {
	if err := l.svcCtx.BannerModel.Delete(l.ctx, uint64(req.Id)); err != nil {
		if errors.Is(err, sqlc.ErrNotFound) || errors.Is(err, model.ErrNotFound) {
			l.Infof("删除失败: 轮播图不存在, id=%d", req.Id)
			return nil, errorx.ErrNotFound
		}
		l.Errorf("删除轮播图失败: id=%d, err=%v", req.Id, err)
		return nil, errorx.ErrInternal
	}

	l.Infof("删除轮播图成功: id=%d", req.Id)
	return &types.EmptyResponse{}, nil
}
