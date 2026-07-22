package admin

import (
	"context"
	"database/sql"
	"errors"

	"local-life-api/internal/errorx"
	"local-life-api/internal/model"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/sqlc"
)

type NoticeUpdateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewNoticeUpdateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *NoticeUpdateLogic {
	return &NoticeUpdateLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// NoticeUpdate 后台更新公告
//   - 校验存在（id 必须存在）
//   - 标题/内容/置顶全部覆盖写
//   - C 端 NoticeList 缓存最多 60s 后失效（依赖 TTL）
func (l *NoticeUpdateLogic) NoticeUpdate(req *types.NoticeUpdateRequest) (resp *types.EmptyResponse, err error) {
	// 1. 校验存在
	notice, err := l.svcCtx.NoticeModel.FindOne(l.ctx, uint64(req.Id))
	if err != nil {
		if errors.Is(err, sqlc.ErrNotFound) || errors.Is(err, model.ErrNotFound) {
			l.Infof("更新失败: 公告不存在, id=%d", req.Id)
			return nil, errorx.ErrNoticeNotFound
		}
		l.Errorf("查询公告失败: id=%d, err=%v", req.Id, err)
		return nil, errorx.ErrInternal
	}

	// 2. 覆盖写
	notice.Title = req.Title
	notice.Content = sql.NullString{String: req.Content, Valid: req.Content != ""}
	notice.IsTop = int64(req.IsTop)

	if err := l.svcCtx.NoticeModel.Update(l.ctx, notice); err != nil {
		l.Errorf("更新公告失败: id=%d, err=%v", notice.Id, err)
		return nil, errorx.ErrInternal
	}

	l.Infof("更新公告成功: id=%d", notice.Id)
	return &types.EmptyResponse{}, nil
}
