package user

import (
	"context"

	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type MyPostDetailLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewMyPostDetailLogic(ctx context.Context, svcCtx *svc.ServiceContext) *MyPostDetailLogic {
	return &MyPostDetailLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *MyPostDetailLogic) MyPostDetail(req *types.IdRequest) (resp *types.PostItem, err error) {
	return NewPostDetailLogic(l.ctx, l.svcCtx).PostDetail(req)
}
