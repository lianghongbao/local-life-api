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

type PostAuditLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewPostAuditLogic(ctx context.Context, svcCtx *svc.ServiceContext) *PostAuditLogic {
	return &PostAuditLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// clearBannerRef 清除关联轮播图的跳转（改为无跳转），避免 C 端点击 404
func (l *PostAuditLogic) clearBannerRef(jumpType int, targetId int64) {
	sql := "UPDATE `banner` SET `jump_type` = 0, `jump_target` = '' WHERE `jump_type` = ? AND `jump_target` = ?"
	if _, err := l.svcCtx.DBConn.ExecCtx(l.ctx, sql, jumpType, targetId); err != nil {
		l.Errorf("清除轮播图关联失败: jumpType=%d, targetId=%d, err=%v", jumpType, targetId, err)
	}
}

// PostAudit 后台审核 post
//   - status: 2=通过 4=拒绝（types 已校验 oneof=2 4）
//   - 通过 → C 端 PostList 可见；拒绝 → 不可见，reject_reason 入库
//   - 允许对已通过的 post 重新拒绝、对已拒绝的重新通过（覆盖式）
//   - 不主动清缓存（依赖 10s TTL 自然失效）
func (l *PostAuditLogic) PostAudit(req *types.PostAuditRequest) (resp *types.EmptyResponse, err error) {
	post, err := l.svcCtx.PostInfoModel.FindOne(l.ctx, uint64(req.Id))
	if err != nil {
		if errors.Is(err, sqlc.ErrNotFound) || errors.Is(err, model.ErrNotFound) {
			l.Infof("审核失败: post 不存在, id=%d", req.Id)
			return nil, errorx.ErrPostNotFound
		}
		l.Errorf("查询 post 失败: id=%d, err=%v", req.Id, err)
		return nil, errorx.ErrInternal
	}

	// 幂等保护：状态一致直接返回
	if post.Status == int64(req.Status) {
		l.Infof("post 状态无需变更: id=%d, status=%d", post.Id, post.Status)
		return &types.EmptyResponse{}, nil
	}

	post.Status = int64(req.Status)
	if req.Status == 4 {
		// 拒绝 → 写拒绝原因；通过 → 清空
		post.RejectReason = req.RejectReason
		// 清除关联的轮播图跳转（改为无跳转），避免 C 端点击 404
		l.clearBannerRef(3, req.Id)
	} else {
		post.RejectReason = ""
	}

	if err := l.svcCtx.PostInfoModel.Update(l.ctx, post); err != nil {
		l.Errorf("更新 post 审核状态失败: id=%d, status=%d, err=%v", post.Id, post.Status, err)
		return nil, errorx.ErrInternal
	}

	action := "通过"
	if req.Status == 4 {
		action = "拒绝"
	}
	l.Infof("post 审核%s成功: id=%d, reason=%q", action, post.Id, req.RejectReason)
	return &types.EmptyResponse{}, nil
}
