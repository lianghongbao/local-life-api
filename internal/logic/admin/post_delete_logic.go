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

type PostDeleteLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewPostDeleteLogic(ctx context.Context, svcCtx *svc.ServiceContext) *PostDeleteLogic {
	return &PostDeleteLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// clearBannerRef 清除关联轮播图的跳转（改为无跳转），避免 C 端点击 404
func (l *PostDeleteLogic) clearBannerRef(jumpType int, targetId int64) {
	sql := "UPDATE `banner` SET `jump_type` = 0, `jump_target` = '' WHERE `jump_type` = ? AND `jump_target` = ?"
	if _, err := l.svcCtx.DBConn.ExecCtx(l.ctx, sql, jumpType, targetId); err != nil {
		l.Errorf("清除轮播图关联失败: jumpType=%d, targetId=%d, err=%v", jumpType, targetId, err)
	}
}

// PostDelete 后台硬删 post
//   - 走 model.Delete 自带缓存清理（cache:postInfo:id:{id}）
//   - C 端 PostList 缓存最多 10s 后失效
func (l *PostDeleteLogic) PostDelete(req *types.IdRequest) (resp *types.EmptyResponse, err error) {
	// 先清轮播关联，再删
	l.clearBannerRef(3, req.Id)

	if err := l.svcCtx.PostInfoModel.Delete(l.ctx, uint64(req.Id)); err != nil {
		if errors.Is(err, sqlc.ErrNotFound) || errors.Is(err, model.ErrNotFound) {
			l.Infof("删除失败: post 不存在, id=%d", req.Id)
			return nil, errorx.ErrPostNotFound
		}
		l.Errorf("删除 post 失败: id=%d, err=%v", req.Id, err)
		return nil, errorx.ErrInternal
	}

	l.Infof("删除 post 成功: id=%d", req.Id)
	return &types.EmptyResponse{}, nil
}
