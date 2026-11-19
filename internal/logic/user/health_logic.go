// 健康检查接口
//   探测三项:tencent_map key 是否可用 + db 连接 + redis 连接
//   - 全部 ok → status: "ok"
//   - 任一 fail → status: "degraded"(失败项写明 msg,运维一眼定位)
//
// 设计取舍:
//   - 不并行:串行足够快(<500ms),并行代码复杂度不值得
//   - 不带 cache:健康检查每次都真实探测,缓存就失去意义
//   - 不返回敏感信息:DB host / redis addr 不在响应里,避免泄露内网地址

package user

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"local-life-api/internal/keys"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

const (
	healthTencentMapKeyPlaceholder = "TENCENT_MAP_KEY_PLACEHOLDER"
	healthProbeTimeout             = 2 * time.Second
)

type HealthLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewHealthLogic(ctx context.Context, svcCtx *svc.ServiceContext) *HealthLogic {
	return &HealthLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// Health 探测三项 + 汇总状态
func (l *HealthLogic) Health(req *types.HealthReq) (resp *types.HealthResp, err error) {
	items := []types.HealthItem{
		l.checkTencentMap(),
		l.checkDB(),
		l.checkRedis(),
	}

	overall := "ok"
	for _, it := range items {
		if it.Status == "fail" {
			overall = "degraded"
			break
		}
	}

	return &types.HealthResp{
		Status: overall,
		Items:  items,
		Ts:     time.Now().Unix(),
	}, nil
}

// checkTencentMap 用一个轻量接口探测腾讯 key 是否有效
//   - district/list 返回 34 个省级,数据量适中(~10KB)
//   - 用 GET 一次判断 key 是否过期 / 欠费 / 配错
//   - status=0 → ok;其他 → fail(写入 message 方便定位)
func (l *HealthLogic) checkTencentMap() types.HealthItem {
	key := l.svcCtx.TencentMapKey
	if !isTencentMapKeyConfigured(key) {
		return types.HealthItem{
			Name: "tencent_map", Status: "fail",
			Msg: "key not configured (empty or placeholder)",
		}
	}

	client := &http.Client{Timeout: healthProbeTimeout}
	req, err := http.NewRequestWithContext(l.ctx, http.MethodGet,
		"https://apis.map.qq.com/ws/district/v1/list?key="+key+"&page_size=1", nil)
	if err != nil {
		return types.HealthItem{Name: "tencent_map", Status: "fail", Msg: err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		return types.HealthItem{Name: "tencent_map", Status: "fail", Msg: "network: " + err.Error()}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var parsed struct {
		Status  int    `json:"status"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return types.HealthItem{Name: "tencent_map", Status: "fail", Msg: "invalid response: " + string(body[:min(len(body), 80)])}
	}
	if parsed.Status != 0 {
		return types.HealthItem{Name: "tencent_map", Status: "fail", Msg: "tencent status=" + itoa(parsed.Status) + " msg=" + parsed.Message}
	}
	return types.HealthItem{Name: "tencent_map", Status: "ok"}
}

// checkDB 用 SELECT 1 探活(go-zero SqlConn 没有 PingContext,ExecCtx 等价)
//   注:QueryRowCtx 会读取结果到 v;我们只关心 err,所以用 ExecCtx 更轻
func (l *HealthLogic) checkDB() types.HealthItem {
	if l.svcCtx.DBConn == nil {
		return types.HealthItem{Name: "db", Status: "fail", Msg: "DBConn is nil"}
	}
	ctx, cancel := context.WithTimeout(l.ctx, healthProbeTimeout)
	defer cancel()
	if _, err := l.svcCtx.DBConn.ExecCtx(ctx, "SELECT 1"); err != nil {
		return types.HealthItem{Name: "db", Status: "fail", Msg: err.Error()}
	}
	return types.HealthItem{Name: "db", Status: "ok"}
}

// checkRedis 用 Ping 探测
func (l *HealthLogic) checkRedis() types.HealthItem {
	if l.svcCtx.RedisCli == nil {
		return types.HealthItem{Name: "redis", Status: "fail", Msg: "RedisCli is nil"}
	}
	// go-zero Redis client 没有 Ping,Setex 一对 dummy 探活
	// key 走 keys.Key —— 共享底座上还有 chatroom,不能共用命名空间
	if err := l.svcCtx.RedisCli.Setex(keys.Key("health:probe"), "1", 1); err != nil {
		return types.HealthItem{Name: "redis", Status: "fail", Msg: err.Error()}
	}
	return types.HealthItem{Name: "redis", Status: "ok"}
}

// itoa int → string(避免 import strconv)
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	buf := make([]byte, 0, 8)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if negative {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}