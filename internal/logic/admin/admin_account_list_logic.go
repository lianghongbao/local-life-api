package admin

import (
	"context"

	"local-life-api/internal/errorx"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type AdminAccountListLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAdminAccountListLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AdminAccountListLogic {
	return &AdminAccountListLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// AdminAccountList 后台账号列表
//   - 全量查询 admin 表,按 id ASC
//   - 不缓存(后台账号变更需实时)
//   - password 字段绝不返回(虽然 SELECT 没列出,这里再注释一次)
func (l *AdminAccountListLogic) AdminAccountList(req *types.PageRequest) (resp *types.AdminAccountListResponse, err error) {
	page := req.Page
	pageSize := req.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	offset := (page - 1) * pageSize

	// COUNT
	var total int64
	if err := l.svcCtx.DBConn.QueryRowCtx(l.ctx, &total, "SELECT COUNT(*) FROM `admin`"); err != nil {
		l.Errorf("查询 admin 账号总数失败: err=%v", err)
		return nil, errorx.ErrInternal
	}

	if total == 0 {
		return &types.AdminAccountListResponse{
			List:  []types.AdminAccountItem{},
			Total: 0,
		}, nil
	}

	// 分页查询(显式列出字段,不含 password)
	listSQL := `
		SELECT
			id, username, role, status,
			DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') AS created_at,
			DATE_FORMAT(updated_at, '%Y-%m-%d %H:%i:%s') AS updated_at
		FROM admin
		ORDER BY id ASC
		LIMIT ? OFFSET ?
	`

	type adminRow struct {
		Id        int64  `db:"id"`
		Username  string `db:"username"`
		Role      int64  `db:"role"`
		Status    int64  `db:"status"`
		CreatedAt string `db:"created_at"`
		UpdatedAt string `db:"updated_at"`
	}

	var rows []adminRow
	if err := l.svcCtx.DBConn.QueryRowsCtx(l.ctx, &rows, listSQL, pageSize, offset); err != nil {
		l.Errorf("查询 admin 账号列表失败: err=%v", err)
		return nil, errorx.ErrInternal
	}

	items := make([]types.AdminAccountItem, len(rows))
	for i, r := range rows {
		items[i] = types.AdminAccountItem{
			Id:        r.Id,
			Username:  r.Username,
			Role:      int(r.Role),
			Status:    int(r.Status),
			CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt,
		}
	}

	return &types.AdminAccountListResponse{
		List:  items,
		Total: total,
	}, nil
}
