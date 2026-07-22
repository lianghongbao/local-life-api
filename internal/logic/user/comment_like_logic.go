package user

import (
	"context"

	"local-life-api/internal/errorx"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
	xerrors "github.com/zeromicro/x/errors"
)

type CommentLikeLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewCommentLikeLogic(ctx context.Context, svcCtx *svc.ServiceContext) *CommentLikeLogic {
	return &CommentLikeLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// CommentLike 幂等 toggle 点赞/取消点赞
//   - 登录校验:uid 空 → 401
//   - 商家拦截:role==2 → 403(主动防刷赞)
//   - 已点赞 → DELETE + like_count - 1(带 if like_count>0 防负数)
//   - 未点赞 → INSERT + like_count + 1(UNIQUE KEY 兜底双击 race)
//   - 返回最新 like_count 给前端(避免前端基于本地缓存)
func (l *CommentLikeLogic) CommentLike(req *types.CommentLikeRequest) (*types.CommentLikeResponse, error) {
	// 1) 登录校验
	uid, role := currentUserFromCtx(l.ctx)
	if uid == "" {
		return nil, xerrors.New(401, "请先登录")
	}

	// 2) 商家不能点赞(主动防刷赞)
	if role == 2 {
		return nil, xerrors.New(403, "商家不能点赞评论")
	}

	// 3) 看是否已点赞
	var existing int
	if err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &existing,
		`SELECT COUNT(*) FROM comment_like WHERE comment_id = ? AND user_id = ?`,
		req.Id, uid,
	); err != nil {
		l.Errorf("check existing like failed: comment_id=%d, user_id=%s, err=%v", req.Id, uid, err)
		return nil, errorx.ErrInternal
	}

	liked := false

	if existing > 0 {
		// 取消点赞
		if _, err := l.svcCtx.DBConn.ExecCtx(l.ctx,
			`DELETE FROM comment_like WHERE comment_id = ? AND user_id = ?`, req.Id, uid,
		); err != nil {
			l.Errorf("delete like failed: comment_id=%d, user_id=%s, err=%v", req.Id, uid, err)
			return nil, errorx.ErrInternal
		}
		if _, err := l.svcCtx.DBConn.ExecCtx(l.ctx,
			`UPDATE comment SET like_count = like_count - 1 WHERE id = ? AND like_count > 0`, req.Id,
		); err != nil {
			// like_count 维护失败仅记日志,不阻塞 toggle 主链路
			l.Errorf("decr like_count failed: comment_id=%d, err=%v", req.Id, err)
		}
	} else {
		// 点赞
		if _, err := l.svcCtx.DBConn.ExecCtx(l.ctx,
			`INSERT INTO comment_like (comment_id, user_id) VALUES (?, ?)`,
			req.Id, uid,
		); err != nil {
			// UNIQUE KEY(comment_id, user_id)兜底双击 race
			l.Errorf("insert like failed: comment_id=%d, user_id=%s, err=%v", req.Id, uid, err)
			return nil, errorx.ErrInternal
		}
		if _, err := l.svcCtx.DBConn.ExecCtx(l.ctx,
			`UPDATE comment SET like_count = like_count + 1 WHERE id = ?`, req.Id,
		); err != nil {
			// like_count 维护失败仅记日志,不阻塞 toggle 主链路
			l.Errorf("incr like_count failed: comment_id=%d, err=%v", req.Id, err)
		}
		liked = true
	}

	// 4) 拿最新 like_count 给前端(避免基于本地缓存)
	var newCount int
	if err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &newCount,
		`SELECT like_count FROM comment WHERE id = ?`, req.Id,
	); err != nil {
		l.Errorf("read fresh like_count failed: comment_id=%d, err=%v", req.Id, err)
		return nil, errorx.ErrInternal
	}

	return &types.CommentLikeResponse{Liked: liked, LikeCount: newCount}, nil
}