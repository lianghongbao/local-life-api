package user

import (
	"context"
	"errors"

	"local-life-api/common/jwtx"
	"local-life-api/internal/cachex"
	"local-life-api/internal/errorx"
	"local-life-api/internal/model"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type PostOfflineLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewPostOfflineLogic(ctx context.Context, svcCtx *svc.ServiceContext) *PostOfflineLogic {
	return &PostOfflineLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// PostOffline C 端下架信息（状态 2→3）
//
//  1. JWT 取 openid（业务标识符）→ 反查 user.id 用于权限校验
//  2. 查帖子（必须是自己的）
//  3. 只能下架已通过(status=2)的帖子
//  4. 更新 status 为 3（已下架），走 model.Update（自动清缓存）
func (l *PostOfflineLogic) PostOffline(req *types.IdRequest) (resp *types.EmptyResponse, err error) {
	// 1. JWT 取 openid → 反查 user.id 用于权限校验
	openid, err := jwtx.GetOpenid(l.ctx)
	if err != nil {
		l.Errorf("JWT 解析失败: %v", err)
		return nil, errorx.ErrUnauthorized
	}
	user, err := l.svcCtx.UserModel.FindOneByOpenid(l.ctx, openid)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil, errorx.ErrUnauthorized
		}
		l.Errorf("查询用户失败: openid=%s, err=%v", openid, err)
		return nil, errorx.ErrInternal
	}
	uid := int64(user.Id)

	// 2. 查帖子
	post, err := l.svcCtx.PostInfoModel.FindOne(l.ctx, uint64(req.Id))
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil, errorx.ErrPostNotFound
		}
		l.Errorf("查询帖子失败: id=%d, err=%v", req.Id, err)
		return nil, errorx.ErrInternal
	}

	// 3. 权限校验：只能操作自己的帖子
	if post.UserId != uint64(uid) {
		l.Errorf("无权操作他人帖子: openid=%s, post.userId=%d, postId=%d", openid, post.UserId, req.Id)
		return nil, errorx.ErrForbidden
	}

	// 4. 状态校验：只能下架已通过的(status=2)
	if post.Status != 2 {
		l.Errorf("帖子状态不允许下架: id=%d, status=%d", req.Id, post.Status)
		return nil, errorx.ErrInvalidParam
	}

	// 5. 更新状态为已下架(status=3)
	post.Status = 3
	if err = l.svcCtx.PostInfoModel.Update(l.ctx, post); err != nil {
		l.Errorf("下架帖子失败: id=%d, err=%v", req.Id, err)
		return nil, errorx.ErrInternal
	}

	// 首页混合流缓存失效:下架后 status=3 不再出现在首页候选池,
	// INCR feed:ver 让旧首页缓存(还在展示这条)立即失效
	cachex.InvalidateFeedVersion(l.ctx, l.svcCtx.RedisCli,
		[]string{"city", "area"},
		cachex.UniqueNonEmpty(post.CityAdcode, post.AreaAdcode),
	)

	l.Infof("下架帖子成功: id=%d, openid=%s", req.Id, openid)
	return &types.EmptyResponse{}, nil
}
