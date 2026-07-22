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

type NoticeDeleteLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewNoticeDeleteLogic(ctx context.Context, svcCtx *svc.ServiceContext) *NoticeDeleteLogic {
	return &NoticeDeleteLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// NoticeDelete 后台删除公告（硬删，依赖 model.Delete 缓存清理）
//   - C 端 NoticeList 缓存最多 60s 后失效（依赖 TTL）
func (l *NoticeDeleteLogic) NoticeDelete(req *types.IdRequest) (resp *types.EmptyResponse, err error) {
	if err := l.svcCtx.NoticeModel.Delete(l.ctx, uint64(req.Id)); err != nil {
		if errors.Is(err, sqlc.ErrNotFound) || errors.Is(err, model.ErrNotFound) {
			l.Infof("删除失败: 公告不存在, id=%d", req.Id)
			return nil, errorx.ErrNoticeNotFound
		}
		l.Errorf("删除公告失败: id=%d, err=%v", req.Id, err)
		return nil, errorx.ErrInternal
	}

	l.Infof("删除公告成功: id=%d", req.Id)
	return &types.EmptyResponse{}, nil
}
