package user

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"local-life-api/internal/errorx"
	"local-life-api/internal/keys"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/sqlc"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	xerrors "github.com/zeromicro/x/errors"
)

type CommentCreateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewCommentCreateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *CommentCreateLogic {
	return &CommentCreateLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// lookupAuthorSnapshot 读作者昵称/头像快照
//   - role==1 (user): 走 user 表 id = ?(主键自增,但 author_id 存的是 openid,所以按 openid 反查)
//   - role==2 (shop): 走 shop 表 id = ?(shop 主键是 user_id 还是自增 id?见下)
//   - 失败 / 找不到 → 返 ("", ""),优雅降级(显示"匿名用户"或无头像,前端可识别)
//   - 注意:user.author_id 存的是 openid(业务标识符),shop.author_id 存的是 shop.id 的字符串形式
//     → 用户侧按 openid 反查 user 表的 id;商家侧按 shop.id 字符串反查 shop 表
func (l *CommentCreateLogic) lookupAuthorSnapshot(uid string, role int) (string, string) {
	if role == 1 {
		// user.author_id 在 comment 表存的是 openid(参见 login_logic.go 的 JWT 主载荷)
		// user 表主键是 id,openid 是 UNIQUE
		var u struct {
			Nickname string `db:"nickname"`
			Avatar   string `db:"avatar"`
		}
		err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &u,
			`SELECT nickname, avatar FROM user WHERE openid = ? LIMIT 1`, uid)
		if err != nil && !errors.Is(err, sqlc.ErrNotFound) {
			l.Errorf("查询 user 快照失败: openid=%s, err=%v", uid, err)
		}
		return u.Nickname, u.Avatar
	}
	if role == 2 {
		// shop 表暂无 avatar_url 列,只返回 name;avatar 永远空串(前端用默认头像兜底)
		var s struct {
			Name string `db:"name"`
		}
		err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &s,
			`SELECT name FROM shop WHERE id = ? LIMIT 1`, uid)
		if err != nil && !errors.Is(err, sqlc.ErrNotFound) {
			l.Errorf("查询 shop 快照失败: id=%s, err=%v", uid, err)
		}
		return s.Name, ""
	}
	return "", ""
}

// CommentCreate 写新评论或子回复
//   - 校验:登录态 + content 非空(validate tag 已校验 max=1000)
//   - 快照:服务端读 user/shop 的 name + avatar 写入 author_*_snapshot
//     (前端就算传空 / 假名 / 旧头像也无效,防客户端伪造)
//   - parent_id:
//       == 0 → 一级评论,root_id 暂时为 0;insert 后立刻 UPDATE root_id = id
//       >  0 → 必须查到父评论(parent_id 必须 == 0,不允许回复子回复);root_id 复用父的 root_id
//   - 副作用:
//       post.comment_count++(失败仅日志,不影响主响应,数据可后续脚本修复)
//   - 响应:回写完整 CommentItem(本地生成 created_at,免一次 DB roundtrip)
func (l *CommentCreateLogic) CommentCreate(req *types.CommentCreateRequest) (resp *types.CommentCreateResponse, err error) {
	// 1) 登录态
	currentUID, role := currentUserFromCtx(l.ctx)
	if currentUID == "" {
		return nil, xerrors.New(401, "请先登录")
	}

	// 2) content 校验 + trim(go-zero validate tag 已校验 min=1,max=1000)
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, xerrors.New(400, "评论内容不能为空")
	}

	// 3) 父评论处理(parent_id 分支)
	var replyTo string // 子回复时才填
	var replyType int  // 子回复时才填(1=user 2=shop 0=无)
	rootId := req.ParentId
	if req.ParentId > 0 {
		var found struct {
			RootID      int64  `db:"root_id"`
			ParentID    int64  `db:"parent_id"`
			ReplyToUid  string `db:"reply_to_uid"`
			ReplyToType int    `db:"reply_to_type"`
			Status      int    `db:"status"`
		}
		parentSQL := `SELECT root_id, parent_id, reply_to_uid, reply_to_type, status
		              FROM comment WHERE id = ? AND post_id = ?`
		if err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &found, parentSQL, req.ParentId, req.PostId); err != nil {
			if errors.Is(err, sqlc.ErrNotFound) {
				return nil, xerrors.New(404, "父评论不存在")
			}
			l.Errorf("查询父评论失败: post_id=%d, parent_id=%d, err=%v", req.PostId, req.ParentId, err)
			return nil, errorx.ErrInternal
		}
		// 校验:父评论必须 parent_id == 0(不允许回复子回复,避免无限嵌套)
		if found.ParentID != 0 {
			return nil, xerrors.New(400, "不允许回复子回复")
		}
		// 校验:父评论不能已删除(status=2)或审核中(status=3)
		if found.Status != 1 {
			return nil, xerrors.New(400, "父评论不可回复")
		}
		// root_id 兜底:正常情况父一级评论 root_id == parent.id,但 sqlx 0 值兜底
		rootId = found.RootID
		if rootId == 0 {
			rootId = req.ParentId
		}
		replyTo = found.ReplyToUid
		replyType = found.ReplyToType
		// 子回复时 replyTo 必定有(父是一级评论时,一级评论的 reply_to_uid 指向"我要回复的人")
		// 若父一级评论无 reply_to_uid(原始一级评论),则此子回复 @ 父作者
		if replyTo == "" {
			// 取父评论 author 信息(@ 父评论作者本人)
			var parentAuthor struct {
				AuthorID   string `db:"author_id"`
				AuthorType int    `db:"author_type"`
			}
			if err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &parentAuthor,
				`SELECT author_id, author_type FROM comment WHERE id = ?`, req.ParentId); err == nil {
				replyTo = parentAuthor.AuthorID
				replyType = parentAuthor.AuthorType
			}
		}
	}

	// 4) 作者快照(服务端读,防伪造)
	name, avatar := l.lookupAuthorSnapshot(currentUID, role)

	// 5) 事务: INSERT + (一级评论)UPDATE root_id + post.comment_count++ + (子回复)parent.reply_count++
	//    4 步同事务,任一失败整体回滚,避免评论已落库但 root_id=0 或 comment_count 漏增造成数据漂移
	newID := int64(0)
	txErr := l.svcCtx.DBConn.TransactCtx(l.ctx, func(ctx context.Context, session sqlx.Session) error {
		res, err := session.ExecCtx(ctx,
			`INSERT INTO comment (
				post_id, parent_id, reply_to_uid, reply_to_type, root_id,
				author_id, author_type, author_name_snapshot, author_avatar_snapshot,
				content, status, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, NOW(), NOW())`,
			req.PostId, req.ParentId, replyTo, replyType, rootId,
			currentUID, role, name, avatar, content,
		)
		if err != nil {
			return err
		}
		id, lastIDErr := res.LastInsertId()
		if lastIDErr != nil {
			return lastIDErr
		}
		newID = id

		// 一级评论:UPDATE root_id = self(子回复的 root_id 已在 INSERT 时复用父的)
		if req.ParentId == 0 {
			if _, err := session.ExecCtx(ctx,
				`UPDATE comment SET root_id = ? WHERE id = ?`, newID, newID); err != nil {
				return err
			}
			rootId = newID
		}

		// post.comment_count++
		if _, err := session.ExecCtx(ctx,
			`UPDATE post_info SET comment_count = comment_count + 1 WHERE id = ?`, req.PostId); err != nil {
			return err
		}

		// 子回复时,父级 reply_count + 1(一级评论不维护 reply_count)
		if req.ParentId > 0 {
			if _, err := session.ExecCtx(ctx,
				`UPDATE comment SET reply_count = reply_count + 1 WHERE id = ?`, req.ParentId); err != nil {
				return err
			}
		}
		return nil
	})
	if txErr != nil {
		l.Errorf("create comment tx failed: post_id=%d, parent_id=%d, err=%v", req.PostId, req.ParentId, txErr)
		return nil, errorx.ErrInternal
	}

	// 7.5) 清掉 post_info 缓存, 否则 PostDetail 拉到的 comment_count 仍是缓存里的 0
	//      → fold-bar 误显示 "还没有人评论"。走 raw SQL 改的列, model.UPDATE 路径才会自动清缓存。
	//      key 走 internal/keys 加应用前缀。
	if _, err := l.svcCtx.RedisCli.DelCtx(l.ctx, keys.Key(fmt.Sprintf("cache:postInfo:id:%d", req.PostId))); err != nil {
		l.Errorf("del post_info cache failed: post_id=%d, err=%v", req.PostId, err)
	}

	// 8) 组响应(本地生成 created_at,免一次 DB roundtrip)
	item := &types.CommentItem{
		Id:        newID,
		PostId:    req.PostId,
		ParentId:  req.ParentId,
		RootId:    rootId,
		Content:   content,
		LikeCount: 0,
		Status:    1,
		Author: types.CommentAuthor{
			Id:     currentUID,
			Type:   role,
			Name:   name,
			Avatar: avatar,
			IsShop: role == 2,
		},
		CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
	}
	if replyTo != "" {
		item.ReplyToUser = &types.CommentAuthor{
			Id:   replyTo,
			Type: replyType,
		}
	}
	return &types.CommentCreateResponse{Item: item}, nil
}