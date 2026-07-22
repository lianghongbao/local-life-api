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

type NoticeDetailLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewNoticeDetailLogic(ctx context.Context, svcCtx *svc.ServiceContext) *NoticeDetailLogic {
	return &NoticeDetailLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// NoticeDetail 后台查看公告详情
//   - 返回 status/created_at/updated_at 完整字段（与 C 端 NoticeList 的 NoticeItem 复用类型）
//   - 与 C 端 notice_detail（user 包）共用 types.NoticeItem，避免重复定义
func (l *NoticeDetailLogic) NoticeDetail(req *types.IdRequest) (resp *types.NoticeItem, err error) {
	notice, err := l.svcCtx.NoticeModel.FindOne(l.ctx, uint64(req.Id))
	if err != nil {
		if errors.Is(err, sqlc.ErrNotFound) || errors.Is(err, model.ErrNotFound) {
			l.Infof("查询失败: 公告不存在, id=%d", req.Id)
			return nil, errorx.ErrNoticeNotFound
		}
		l.Errorf("查询公告失败: id=%d, err=%v", req.Id, err)
		return nil, errorx.ErrInternal
	}

	return &types.NoticeItem{
		Id:        int64(notice.Id),
		Title:     notice.Title,
		Content:   notice.Content.String,
		IsTop:     int(notice.IsTop),
		Status:    int(notice.Status),
		CreatedAt: notice.CreatedAt.Format("2006-01-02 15:04:05"),
		UpdatedAt: notice.UpdatedAt.Format("2006-01-02 15:04:05"),
	}, nil
}
