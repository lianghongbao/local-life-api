package user

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"local-life-api/common/jwtx"
	"local-life-api/internal/errorx"
	"local-life-api/internal/model"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type MyPostsLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewMyPostsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *MyPostsLogic {
	return &MyPostsLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// postRow 数据库查询行结构，字段类型与 post_info / user 表严格对齐
type postRow struct {
	Id         uint64         `db:"id"`
	UserId     uint64         `db:"user_id"`
	Nickname   string         `db:"nickname"`
	Avatar     string         `db:"avatar"`
	Type       int64          `db:"type"`
	Title      string         `db:"title"`
	Images     sql.NullString `db:"images"`
	Content    sql.NullString `db:"content"`
	Area       string         `db:"area"`
	Latitude   float64        `db:"latitude"`
	Longitude  float64        `db:"longitude"`
	Contact    string         `db:"contact"`
	Status     int64          `db:"status"`
	ViewCount  uint64         `db:"view_count"`
	CreatedAt  string         `db:"created_at"`
	AreaAdcode string         `db:"area_adcode"`
	AreaName   string         `db:"area_name"`
	StreetName string         `db:"street_name"`
	CityAdcode string         `db:"city_adcode"`
}

// myPostsCacheTTL "我的发布" 列表缓存时间
//   - 短 TTL 让 DML（insert/update/delete）后自然失效，无需主动删缓存
//   - 10s 内同一用户重复翻页直接命中 Redis
const myPostsCacheTTL = 10 * time.Second

// myPostsCacheKey 缓存 key 模板：my:posts:{userId}:p{page}:s{size}
func myPostsCacheKey(userId int64, page, pageSize int) string {
	return fmt.Sprintf("my:posts:%d:p%d:s%d", userId, page, pageSize)
}

// MyPosts 查询当前用户发布的列表（含所有状态，按时间倒序）
//
// 缓存策略：
//   - Redis key：my:posts:{userId}:p{page}:s{size}，TTL 10s
//   - 命中：直接返回 JSON 反序列化的结果
//   - 未命中：走 DB 查询，结果写入 Redis（SETEX，每次覆盖写）
//   - DML 后无需主动失效：下次查询会用新结果覆盖旧缓存，
//     最坏延迟 10s（TTL 到期后必走 DB 重建）
//
// 标识体系：JWT 取 openid → FindOneByOpenid 反查 uid → 用 uid 查 post_info
func (l *MyPostsLogic) MyPosts(req *types.PageRequest) (resp *types.PostListResponse, err error) {
	// 1. 从 JWT 取 openid(业务标识符)
	openid, err := jwtx.GetOpenid(l.ctx)
	if err != nil {
		l.Errorf("JWT 解析失败: %v", err)
		return nil, errorx.ErrUnauthorized
	}

	// 2. 反查 user.id 用于缓存 key + 查 post_info.user_id 外键
	user, err := l.svcCtx.UserModel.FindOneByOpenid(l.ctx, openid)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil, errorx.ErrUnauthorized
		}
		l.Errorf("查询用户失败: openid=%s, err=%v", openid, err)
		return nil, errorx.ErrInternal
	}
	uid := int64(user.Id)

	// 2. 分页参数兜底（PageRequest 上有 validate tag，这里双保险）
	page := req.Page
	pageSize := req.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	offset := (page - 1) * pageSize

	// 3. 查缓存（svcCtx.RedisCli 由 NewServiceContext 保证非 nil）
	cacheKey := myPostsCacheKey(uid, page, pageSize)
	cached, cacheErr := l.svcCtx.RedisCli.Get(cacheKey)
	if cacheErr == nil && cached != "" {
		var hit types.PostListResponse
		if jsonUnmarshalErr := json.Unmarshal([]byte(cached), &hit); jsonUnmarshalErr == nil {
			return &hit, nil
		} else {
			// 反序列化失败不视为致命错误，降级走 DB 并打日志
			l.Errorf("我的发布缓存反序列化失败: key=%s, err=%v", cacheKey, jsonUnmarshalErr)
		}
	}

	// 4. 查总数
	var total int64
	countSQL := "SELECT COUNT(*) FROM `post_info` WHERE `user_id` = ?"
	if err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &total, countSQL, uint64(uid)); err != nil {
		l.Errorf("查询用户发布总数失败: userId=%d, err=%v", uid, err)
		return nil, errorx.ErrInternal
	}

	// 5. 空结果直接返回（避免无意义的 JOIN 查询，同时缓存空结果防止缓存穿透）
	if total == 0 {
		resp = &types.PostListResponse{
			List:  []types.PostItem{},
			Total: 0,
		}
		l.cacheMyPosts(cacheKey, resp)
		return resp, nil
	}

	// 6. 分页查询（JOIN user 表拿 nickname + avatar，全部用 ? 占位符）
	listSQL := `
		SELECT
			p.id, p.user_id,
			u.nickname, u.avatar,
			p.type, p.title, p.images, p.content,
			p.area, p.latitude, p.longitude, p.contact,
			p.status,
			p.view_count,
			DATE_FORMAT(p.created_at, '%Y-%m-%d %H:%i:%s') AS created_at,
			p.area_adcode, p.area_name, p.street_name, p.city_adcode
		FROM post_info p
		LEFT JOIN user u ON u.id = p.user_id
		WHERE p.user_id = ?
		ORDER BY p.created_at DESC
		LIMIT ? OFFSET ?
	`

	var rows []postRow
	if err := l.svcCtx.DBConn.QueryRowsCtx(l.ctx, &rows, listSQL, uint64(uid), pageSize, offset); err != nil {
		l.Errorf("查询用户发布列表失败: userId=%d, err=%v", uid, err)
		return nil, errorx.ErrInternal
	}

	// 7. Avatar: file_key → URL(永久有效)
	// 8. 映射到 PostItem（行数已知，直接索引赋值避免 append grow）
	items := make([]types.PostItem, len(rows))
	for i, r := range rows {
		items[i] = types.PostItem{
			Id:         int64(r.Id),
			UserId:     int64(r.UserId),
			Nickname:   r.Nickname,
			Avatar:     l.svcCtx.OSS.Client.URL(r.Avatar),
			Type:       int(r.Type),
			Title:      r.Title,
			Images:     BuildURLsInJSON(l.ctx, r.Images.String, l.svcCtx.OSS.Client),
			Content:    r.Content.String,
			Area:       r.Area,
			Latitude:   r.Latitude,
			Longitude:  r.Longitude,
			Contact:    r.Contact,
			Status:     int(r.Status),
			ViewCount:  int(r.ViewCount),
			CreatedAt:  r.CreatedAt,
			AreaAdcode: r.AreaAdcode,
			AreaName:   r.AreaName,
			StreetName: r.StreetName,
			CityAdcode: r.CityAdcode,
		}
	}

	resp = &types.PostListResponse{
		List:  items,
		Total: total,
	}

	// 8. 写缓存（失败仅记录日志，不影响返回）
	l.cacheMyPosts(cacheKey, resp)

	return resp, nil
}

// cacheMyPosts 写入 Redis 缓存，TTL 10s
func (l *MyPostsLogic) cacheMyPosts(key string, resp *types.PostListResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		l.Errorf("我的发布缓存序列化失败: key=%s, err=%v", key, err)
		return
	}

	if setErr := l.svcCtx.RedisCli.Setex(key, string(data), int(myPostsCacheTTL.Seconds())); setErr != nil {
		l.Errorf("我的发布缓存写入失败: key=%s, err=%v", key, setErr)
	}
}
