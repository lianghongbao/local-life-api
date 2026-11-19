package user

import (
	"context"
	"encoding/json"
	"fmt"

	"local-life-api/internal/errorx"
	"local-life-api/internal/keys"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type NoticeListLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewNoticeListLogic(ctx context.Context, svcCtx *svc.ServiceContext) *NoticeListLogic {
	return &NoticeListLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// 公告列表缓存 TTL（秒）
//   - 有数据：60s（公告由后台编辑，变更频率低）
//   - 空结果：5s（防穿透 + 快速失效，避免发公告后等 1 分钟才看到）
const (
	noticeListCacheTTLSeconds   = 60
	noticeListEmptyCacheTTLSecs = 5
)

// noticeListCacheKey 缓存 key：local-life:notices:p{page}:s{size}
func noticeListCacheKey(page, pageSize int) string {
	return keys.Key(fmt.Sprintf("notices:p%d:s%d", page, pageSize))
}

// NoticeList C 端公告列表
//   - 公开接口，仅展示 status=1（正常）
//   - 置顶优先，再按 created_at DESC
//   - Redis 缓存 60s（公告变更不频繁，靠 TTL 自然过期）
func (l *NoticeListLogic) NoticeList(req *types.PageRequest) (resp *types.NoticeListResponse, err error) {
	// 1. 分页参数兜底
	page := req.Page
	pageSize := req.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	offset := (page - 1) * pageSize

	// 2. 查缓存
	cacheKey := noticeListCacheKey(page, pageSize)
	cached, cacheErr := l.svcCtx.RedisCli.Get(cacheKey)
	if cacheErr == nil && cached != "" {
		var hit types.NoticeListResponse
		if jsonErr := json.Unmarshal([]byte(cached), &hit); jsonErr == nil {
			return &hit, nil
		} else {
			l.Errorf("NoticeList 缓存反序列化失败: key=%s, err=%v", cacheKey, jsonErr)
		}
	}

	// 3. 查总数
	var total int64
	countSQL := "SELECT COUNT(*) FROM `notice` WHERE `status` = 1"
	if err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &total, countSQL); err != nil {
		l.Errorf("查询公告总数失败: err=%v", err)
		return nil, errorx.ErrInternal
	}

	// 4. 空结果短路（缓存短 TTL 防穿透，避免发公告后等满 TTL 才看到）
	if total == 0 {
		resp = &types.NoticeListResponse{
			List:  []types.NoticeItem{},
			Total: 0,
		}
		l.cacheNoticeList(cacheKey, resp, noticeListEmptyCacheTTLSecs)
		return resp, nil
	}

	// 5. 分页查询（置顶优先 + 时间倒序）
	listSQL := `
		SELECT
			id, title, content, is_top, status,
			DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') AS created_at,
			DATE_FORMAT(updated_at, '%Y-%m-%d %H:%i:%s') AS updated_at
		FROM notice
		WHERE status = 1
		ORDER BY is_top DESC, created_at DESC
		LIMIT ? OFFSET ?
	`

	type noticeRow struct {
		Id        int64  `db:"id"`
		Title     string `db:"title"`
		Content   string `db:"content"`
		IsTop     int64  `db:"is_top"`
		Status    int64  `db:"status"`
		CreatedAt string `db:"created_at"`
		UpdatedAt string `db:"updated_at"`
	}

	var rows []noticeRow
	if err := l.svcCtx.DBConn.QueryRowsCtx(l.ctx, &rows, listSQL, pageSize, offset); err != nil {
		l.Errorf("查询公告列表失败: err=%v", err)
		return nil, errorx.ErrInternal
	}

	// 6. 映射到 NoticeItem
	items := make([]types.NoticeItem, len(rows))
	for i, r := range rows {
		items[i] = types.NoticeItem{
			Id:        r.Id,
			Title:     r.Title,
			Content:   r.Content,
			IsTop:     int(r.IsTop),
			Status:    int(r.Status),
			CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt,
		}
	}

	resp = &types.NoticeListResponse{
		List:  items,
		Total: total,
	}

	// 7. 写缓存
	l.cacheNoticeList(cacheKey, resp, noticeListCacheTTLSeconds)

	return resp, nil
}

// cacheNoticeList 写入公告列表缓存
func (l *NoticeListLogic) cacheNoticeList(key string, resp *types.NoticeListResponse, ttl int) {
	data, err := json.Marshal(resp)
	if err != nil {
		l.Errorf("NoticeList 缓存序列化失败: key=%s, err=%v", key, err)
		return
	}
	if setErr := l.svcCtx.RedisCli.Setex(key, string(data), ttl); setErr != nil {
		l.Errorf("NoticeList 缓存写入失败: key=%s, err=%v", key, setErr)
	}
}
