package user

import (
	"context"
	"errors"

	"local-life-api/internal/errorx"
	"local-life-api/internal/model"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
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

// noticeVisibleStatus 对 C 端可见的公告状态：1=正常
const noticeVisibleStatus = 1

// NoticeDetail C 端公告详情
//   - 仅展示 status=1（正常）；其他状态对 C 端透明（统一返回 not-found，防信息泄露）
func (l *NoticeDetailLogic) NoticeDetail(req *types.IdRequest) (resp *types.NoticeItem, err error) {
	// 1. 通过 model 查（命中 cache:notice:id:{id}）
	notice, err := l.svcCtx.NoticeModel.FindOne(l.ctx, uint64(req.Id))
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil, errorx.ErrNoticeNotFound
		}
		l.Errorf("查询 notice 失败: id=%d, err=%v", req.Id, err)
		return nil, errorx.ErrInternal
	}

	// 2. 仅展示正常公告；其他状态对 C 端透明（防信息泄露）
	if notice.Status != noticeVisibleStatus {
		return nil, errorx.ErrNoticeNotFound
	}

	// 3. 构造响应
	resp = &types.NoticeItem{
		Id:        int64(notice.Id),
		Title:     notice.Title,
		Content:   notice.Content.String,
		IsTop:     int(notice.IsTop),
		Status:    int(notice.Status),
		CreatedAt: notice.CreatedAt.Format("2006-01-02 15:04:05"),
		UpdatedAt: notice.UpdatedAt.Format("2006-01-02 15:04:05"),
	}

	return resp, nil
}
