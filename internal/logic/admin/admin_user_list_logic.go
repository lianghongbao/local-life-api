package admin

import (
	"context"

	"local-life-api/internal/errorx"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type AdminUserListLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAdminUserListLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AdminUserListLogic {
	return &AdminUserListLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// AdminUserList 后台用户列表
//   - 分页查询 user 表
//   - 按 created_at DESC
//   - 不缓存（后台数据需实时性）
func (l *AdminUserListLogic) AdminUserList(req *types.PageRequest) (resp *types.AdminUserListResponse, err error) {
	// 1. 分页兜底
	page := req.Page
	pageSize := req.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	offset := (page - 1) * pageSize

	// 2. COUNT
	var total int64
	if err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &total, "SELECT COUNT(*) FROM `user`"); err != nil {
		l.Errorf("查询用户总数失败: err=%v", err)
		return nil, errorx.ErrInternal
	}

	if total == 0 {
		return &types.AdminUserListResponse{
			List:  []types.AdminUserItem{},
			Total: 0,
		}, nil
	}

	// 3. 分页查询（不缓存，后台需要实时）
	listSQL := `
		SELECT
			id, nickname, avatar, phone, status,
			DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') AS created_at
		FROM user
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`

	type userRow struct {
		Id        int64  `db:"id"`
		Nickname  string `db:"nickname"`
		Avatar    string `db:"avatar"`
		Phone     string `db:"phone"`
		Status    int64  `db:"status"`
		CreatedAt string `db:"created_at"`
	}

	var rows []userRow
	if err := l.svcCtx.DBConn.QueryRowsCtx(l.ctx, &rows, listSQL, pageSize, offset); err != nil {
		l.Errorf("查询用户列表失败: err=%v", err)
		return nil, errorx.ErrInternal
	}

	// 拼接 完整 URL(file_key → https://static.lamborai.com/...)
	avatarKeys := make([]string, len(rows))
	for i, r := range rows {
		avatarKeys[i] = r.Avatar
	}
	avatars := l.svcCtx.OSS.Client.URLs(avatarKeys)

	items := make([]types.AdminUserItem, len(rows))
	for i, r := range rows {
		items[i] = types.AdminUserItem{
			Id:        r.Id,
			Nickname:  r.Nickname,
			Avatar:    avatars[i],
			Phone:     r.Phone,
			Status:    int(r.Status),
			CreatedAt: r.CreatedAt,
		}
	}

	return &types.AdminUserListResponse{
		List:  items,
		Total: total,
	}, nil
}
