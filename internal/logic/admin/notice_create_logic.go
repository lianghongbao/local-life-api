package admin

import (
	"context"
	"database/sql"

	"local-life-api/internal/errorx"
	"local-life-api/internal/model"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type NoticeCreateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewNoticeCreateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *NoticeCreateLogic {
	return &NoticeCreateLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// NoticeCreate 后台创建公告
//   - 默认 status=1（正常）
//   - C 端 NoticeList 缓存最多 60s 后失效（依赖 TTL，不主动清）
func (l *NoticeCreateLogic) NoticeCreate(req *types.NoticeCreateRequest) (resp *types.EmptyResponse, err error) {
	notice := &model.Notice{
		Title:   req.Title,
		Content: sql.NullString{String: req.Content, Valid: req.Content != ""},
		IsTop:   int64(req.IsTop),
		Status:  1, // 1正常
	}

	if _, err := l.svcCtx.NoticeModel.Insert(l.ctx, notice); err != nil {
		l.Errorf("创建公告失败: title=%q, err=%v", req.Title, err)
		return nil, errorx.ErrInternal
	}

	l.Infof("创建公告成功: id=%d, title=%q", notice.Id, req.Title)
	return &types.EmptyResponse{}, nil
}
