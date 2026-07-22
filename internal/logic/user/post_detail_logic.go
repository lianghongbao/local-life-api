package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"local-life-api/internal/background"
	"local-life-api/internal/errorx"
	"local-life-api/internal/model"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/sqlc"
)

type PostDetailLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewPostDetailLogic(ctx context.Context, svcCtx *svc.ServiceContext) *PostDetailLogic {
	return &PostDetailLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// postVisibleStatus 对 C 端可见的 post 状态：2=已通过
const postVisibleStatus = 2

// PostDetail 帖子详情,作者可见自己 status≠2 的帖子(已下架/审核中/已驳回)
//   - status=2 → 全员可见
//   - status≠2 → 仅作者本人;其余情况 404(防分享链接外泄)
//   - 走 raw SQL 不走 PostInfoModel 缓存:
//     post_info 加字段后老 cache 反序列化字段错位,会让 post.UserId 读到错误值导致误判"非作者"
func (l *PostDetailLogic) PostDetail(req *types.IdRequest) (resp *types.PostItem, err error) {
	post, err := l.findPostRaw(uint64(req.Id))
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil, errorx.ErrPostNotFound
		}
		l.Errorf("查询 post 失败: id=%d, err=%v", req.Id, err)
		return nil, errorx.ErrInternal
	}

	// 权限闸门:非可见 status 仅作者本人可看(作者匹配通过 JWT openid → user.id 反查)
	if post.Status != postVisibleStatus {
		viewerUid, _ := currentUserFromCtx(l.ctx)
		var viewerUser *model.User
		if viewerUid != "" {
			if u, errUID := l.svcCtx.UserModel.FindOneByOpenid(l.ctx, viewerUid); errUID == nil {
				viewerUser = u
			}
		}
		// 旁观者未登录/未匹配时,记录 post 与 viewer 信息(便于排查异常访问路径)
		if viewerUser == nil {
			l.Infof("[PostDetail] status≠2, viewer 未登录或 user 未找到: post_id=%d, post.user_id=%d, viewerUid=%q",
				req.Id, post.UserId, viewerUid)
		} else {
			l.Infof("[PostDetail] status≠2, viewer 找到: post_id=%d, post.user_id=%d, viewerUser.Id=%d, openid=%q",
				req.Id, post.UserId, viewerUser.Id, viewerUid)
		}
		if viewerUser == nil || int64(viewerUser.Id) != int64(post.UserId) {
			return nil, errorx.ErrPostNotFound
		}
	}

	// 浏览计数:走 Redis HINCRBY,与 mysql.view_count 无关
	viewCount := int(background.IncrViewCount(
		l.svcCtx.RedisCli,
		background.PostViewKeyPrefix,
		background.PostViewDirtyKey,
		uint64(req.Id),
	))

	// 作者信息(失败降级,前端展示缺昵称/缺头像)
	var nickname, avatar string
	user, userErr := l.svcCtx.UserModel.FindOne(l.ctx, post.UserId)
	if userErr == nil {
		nickname = user.Nickname
		avatar = user.Avatar
	}
	if avatar != "" {
		avatar = l.svcCtx.OSS.Client.URL(avatar)
	}

	resp = &types.PostItem{
		Id:           int64(post.Id),
		UserId:       int64(post.UserId),
		Nickname:     nickname,
		Avatar:       avatar,
		Type:         int(post.Type),
		Title:        post.Title,
		Images:       BuildURLsInJSON(l.ctx, post.Images.String, l.svcCtx.OSS.Client),
		Content:      post.Content.String,
		Area:         post.Area,
		Latitude:     post.Latitude,
		Longitude:    post.Longitude,
		Contact:      post.Contact,
		Status:       int(post.Status),
		RejectReason: post.RejectReason.String,
		ViewCount:    viewCount,
		CommentCount: int(post.CommentCount),
		CreatedAt:    post.CreatedAt.Format("2006-01-02 15:04:05"),
		AreaAdcode:   post.AreaAdcode,
		AreaName:     post.AreaName,
		StreetName:   post.StreetName,
		CityAdcode:   post.CityAdcode,
	}
	return resp, nil
}

// postDetailRow raw SQL 扫描的目标 struct;字段顺序/类型与 SELECT 严格对应
type postDetailRow struct {
	Id           uint64         `db:"id"`
	UserId       uint64         `db:"user_id"`
	Type         int64          `db:"type"`
	Title        string         `db:"title"`
	Images       sql.NullString `db:"images"`
	Content      sql.NullString `db:"content"`
	Area         string         `db:"area"`
	Contact      string         `db:"contact"`
	Latitude     float64        `db:"latitude"`
	Longitude    float64        `db:"longitude"`
	AreaAdcode   string         `db:"area_adcode"`
	AreaName     string         `db:"area_name"`
	StreetName   string         `db:"street_name"`
	CityAdcode   string         `db:"city_adcode"`
	CityName     sql.NullString `db:"city_name"`
	Status       int64          `db:"status"`
	RejectReason sql.NullString `db:"reject_reason"`
	ViewCount    uint64         `db:"view_count"`
	CommentCount uint64         `db:"comment_count"`
	CreatedAt    time.Time      `db:"created_at"`
	UpdatedAt    time.Time      `db:"updated_at"`
}

// findPostRaw 绕过 PostInfoModel 缓存层,直接走 DB 查 post_info
func (l *PostDetailLogic) findPostRaw(id uint64) (*postDetailRow, error) {
	var r postDetailRow
	err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &r,
		`SELECT id, user_id, type, title, images, content, area, contact,
		        latitude, longitude,
		        area_adcode, area_name, street_name, city_adcode, city_name,
		        status, reject_reason, view_count, comment_count,
		        created_at, updated_at
		 FROM post_info WHERE id = ? LIMIT 1`, id,
	)
	if err != nil {
		if errors.Is(err, sqlc.ErrNotFound) || errors.Is(err, model.ErrNotFound) {
			return nil, model.ErrNotFound
		}
		return nil, fmt.Errorf("query post_info %d: %w", id, err)
	}
	return &r, nil
}
