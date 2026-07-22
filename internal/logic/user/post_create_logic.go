package user

import (
	"context"
	"database/sql"
	"errors"
	"regexp"

	"local-life-api/common/jwtx"
	"local-life-api/internal/errorx"
	"local-life-api/internal/model"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

// phoneRE 中国大陆手机号正则：1 开头 + 3-9 + 9 位数字，共 11 位
var phoneRE = regexp.MustCompile(`^1[3-9]\d{9}$`)

// postImgExts 帖子图片后缀白名单
//   - 改这里请同步 upload_policy.go 里的 FileTypePolicies["image"]
var postImgExts = []string{"jpg", "jpeg", "png", "webp", "gif"}

type PostCreateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewPostCreateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *PostCreateLogic {
	return &PostCreateLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// postCreateStatusInitial 新发布的 post 状态：1=待审核
const postCreateStatusInitial = 1

// PostCreate C 端发布信息
//   - JWT 取 openid（业务标识符）→ 反查 user.id 写 user_id 外键
//   - 不信任客户端传的 user_id
//   - 校验用户未被封禁（user.status != 1 → ErrUserBanned）
//   - images 字段：JSON 数组字符串，空字符串或合法 JSON 数组
//   - status 默认 1（待审核），后续由管理员审核
//   - 通过 PostInfoModel.Insert 写库（自带缓存层）
func (l *PostCreateLogic) PostCreate(req *types.PostCreateRequest) (resp *types.EmptyResponse, err error) {
	// 1. 从 JWT 取 openid(业务标识符)
	openid, err := jwtx.GetOpenid(l.ctx)
	if err != nil {
		l.Errorf("JWT 解析失败: %v", err)
		return nil, errorx.ErrUnauthorized
	}

	// 2. 用 openid 反查 user(拿 user.id 写外键 + 校验封禁)
	user, err := l.svcCtx.UserModel.FindOneByOpenid(l.ctx, openid)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil, errorx.ErrUnauthorized
		}
		l.Errorf("查询用户失败: openid=%s, err=%v", openid, err)
		return nil, errorx.ErrInternal
	}
	if user.Status != 1 {
		l.Errorf("用户已被封禁: openid=%s, status=%d", openid, user.Status)
		return nil, errorx.ErrUserBanned
	}
	uid := int64(user.Id)

	// 3. 校验并规范化 images 字段(走公共校验: host + path prefix + 后缀白名单)
	images, err := ValidateAndCleanImages(req.Images, BizPost, uid, postImgExts, l.svcCtx.OSS.HostCfg)
	if err != nil {
		l.Errorf("images 字段非法: openid=%s, raw=%q, err=%v", openid, req.Images, err)
		return nil, errorx.ErrInvalidParam
	}

	// 4. 校验联系方式手机号格式
	if !phoneRE.MatchString(req.Contact) {
		l.Errorf("联系方式手机号格式非法: openid=%s, contact=%q", openid, req.Contact)
		return nil, errorx.ErrInvalidParam
	}

	// 5. reverse geocoding(失败不阻塞主链路,5 字段留空字符串)
	cityAdcode, cityName, areaAdcode, areaName, streetName := ResolveGeoFields(l.ctx, l.svcCtx, req.Latitude, req.Longitude)
	if areaAdcode == "" || areaName == "" || streetName == "" || cityAdcode == "" || cityName == "" {
		l.Infof("[post_create] WARN geo fields partial: openid=%s, lat=%f, lng=%f, "+
			"city_adcode=%q city_name=%q area_adcode=%q area_name=%q street_name=%q",
			openid, req.Latitude, req.Longitude, cityAdcode, cityName, areaAdcode, areaName, streetName)
	}

	// 6. 构造 PostInfo（sql.NullString 处理可空字段）
	post := &model.PostInfo{
		UserId:       uint64(uid),
		Type:         int64(req.Type),
		Title:        req.Title,
		Images:       sql.NullString{String: images, Valid: images != ""},
		Content:      sql.NullString{String: req.Content, Valid: req.Content != ""},
		Area:         req.Area,
		Contact:      req.Contact,
		Latitude:     req.Latitude,
		Longitude:    req.Longitude,
		AreaAdcode:   areaAdcode,
		AreaName:     areaName,
		StreetName:   streetName,
		CityAdcode:   cityAdcode,
		CityName:     cityName,
		Status:       postCreateStatusInitial,
		RejectReason: "",
		ViewCount:    0,
		CommentCount: 0,
	}

	// 7. INSERT（model 自带缓存：cache:postInfo:id:{id}）
	if _, err := l.svcCtx.PostInfoModel.Insert(l.ctx, post); err != nil {
		l.Errorf("发布 post 失败: openid=%s, err=%v", openid, err)
		return nil, errorx.ErrInternal
	}

	l.Infof("发布 post 成功: openid=%s, type=%d", openid, req.Type)
	return &types.EmptyResponse{}, nil
}
