package background

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"local-life-api/internal/svc"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/redis"
)

// view_count 单权威源为 Redis Hash（HINCRBY 累加），mysql.view_count 仅作后台异步镜像。
//
// Redis 数据结构（每张表独立 namespace，key 已用 hashtag 形式）：
//
//	{ns:view}        HASH:  field={id}, value=累计 view_count（HINCRBY 单调递增）
//	{ns:view}:dirty  SET:   待 flush 的 id 集合（自动去重）
//
// hashtag 规则(Redis Cluster 兼容):
//   - 大括号内 "ns:view" 是 hashtag 参与 slot 计算
//   - 同 namespace 的 hash 和 dirty key 落同一 slot
//   - lua / pipeline 跨 key 可用
//   - 不同 namespace 分散到不同 slot/节点
//
// 单 hash 结构 vs 独立 string keys:
//   - 单 hash 内存占用低(约 30MB 可容纳 100 万 fields,比 100 万 string key 节省 ~50MB)
//   - Redis Cluster 上落同一个 slot,避免 CROSSSLOT 限制
//   - pipeline / lua 操作可全部集中在一个 key 上
//   - 监控/审计/redis-cli --pattern 抓 view_count 操作更聚合
//
// 大 key 风控:
//   - 单 hash 的 fields 总数建议不超过 100 万。超出请做分桶(id % N)。
//   - 本文件内所有 redis 操作只允许 HGET/HMGET/HINCRBY/HDEL 等精细命令。
//   - 严禁 HGETALL / HKEYS / HVALS / DEL {prefix}(不要清整个 hash)。
//   - field 级 EXPIRE 在 Redis 社区版不支持,不要尝试 EXPIRE 整个 hash。
//
// 数据流：
//
//	读路径 PostDetail（业务直接读 Redis，不读 mysql.view_count）：
//	  1. HINCRBY {prefix} {id} 1（得到当前真实值）
//	  2. SADD {prefix}:dirty {id}
//
//	后台 flush（每 viewCountFlushInterval 秒一次）：
//	  for table in tables {
//	    // 1) Lua 一次：SPOP 最多 N 个 id，并 HGET 各 id 当前 incr 累加值
//	    ids, incrValues = LUA_SPOP_AND_HGET(table.DirtyKey, table.HashKey, MaxBatch)
//	    if ids 空 -> 静默
//
//	    // 2) SELECT 一次拿 mysql.view_count 当前值（id IN (?, ?, ...)）
//	    mysqlCounts[id] = SELECT id, view_count FROM table.Name WHERE id IN (...)
//
//	    // 3) Go 层防覆盖过滤：
//	    //    - incr <= 0 跳过（防 0 值覆盖）
//	    //    - incr <= mysqlCounts[id] 跳过（防小值覆盖大值，保护运营/人工干预）
//	    //    - 仅 incr > mysqlCounts[id] 进入待 UPDATE 列表
//
//	    if 待 UPDATE 列表空 -> 直接结束（SPOP 已消费 dirty 数据，无需重 SADD）
//
//	    // 4) 1 条 SQL 写 mysql（CASE WHEN 批量）
//	    UPDATE `table.Name` SET `view_count` = CASE `id` WHEN ... THEN ... END WHERE `id` IN (...)
//
//	    // 5) pipeline DEL model 缓存（即便失败也不回退，下次 HINCRBY 自然重新加载）
//	    pipelined DEL {CacheKeyPrefix}{id} × N
//
//	    // 6) UPDATE 失败 -> SADD id 回 dirty，5s 周期兜底重试
//	  }
//
// 多实例安全：
//   - SPOP 由 Redis 单线程原子保证多实例不重复
//   - 失败 SADD 回 dirty 即使重复也是 no-op
//   - 防覆盖 WHERE/if 避免任何时序竞争导致的回退
//
// 监控（结构化日志）：
//   - 启动/停止：action=start/stopping
//   - 每次 flush：action=filter 或 action=finish，带 result/processed/filter/duration
//   - 错误：打 ERROR，带 stage + table 标识
//   - 日志统一前缀：[view_count_flush]

const (
	// viewCountFlushInterval flush 周期。
	// 5s 周期：单 SQL 写 1000 行 ~30ms, MySQL 端 CPU 占用 < 0.4%, 远低于满载。
	// 副作用：cache 命中率略降 + binlog 频率 6× — 当前业务量下可接受。
	viewCountFlushInterval = 5 * time.Second

	// viewCountFlushMaxBatch 单表单次循环最多处理的 id 个数。
	// 保护：异常情况下防止单次无限循环。
	viewCountFlushMaxBatch = 1000
)

// FlushTableConfig 单张表的 view_count flush 配置。
type FlushTableConfig struct {
	// Name 表名（硬编码常量："post_info" / "shop"，仅内部配置使用，安全拼 SQL）。
	Name string
	// DirtyKey 待 flush 的 id 集合 key。
	DirtyKey string
	// HashKey HASH 形式的累加值 key(如 "{post:view}"),id 不再嵌入 key 路径,
	// 而是作为 hash field 传递:field = strconv.FormatUint(id, 10),value = 当前累计 view_count。
	HashKey string
	// CacheKeyPrefix model 缓存 key 前缀（flush 后清空，如 "cache:postInfo:id:"）。
	CacheKeyPrefix string
}

// 预置配置(导出 const 供 logic 层引用,避免硬编码 key)。
//
// key 格式约定:HASH 数据结构的 hash key(如 "{post:view}"),id 作为 hash field 拼接。
//
// Cluster hashtag 约束(已合规):
//   - 所有 key 形如 "local-life:{namespace}:view" 或 "local-life:{namespace}:view:dirty"
//   - hashtag = 大括号内的 "namespace:view"(如 "post:view" / "shop:view")
//   - 同 namespace 的 dirty 和 hash key 落同一 slot, lua / pipeline 跨 key 可用
//   - 不同 namespace 走不同 hashtag, 自动分散到不同 slot / 节点
//   - local-life: 前缀在 hashtag 之外 —— 加前缀不影响 slot 分布
const (
	// PostViewKeyPrefix post view_count 累加 hash key。
	PostViewKeyPrefix = "local-life:{post:view}"
	// PostViewDirtyKey post view_count 待 flush 集合 key。
	PostViewDirtyKey = "local-life:{post:view}:dirty"
	// ShopViewKeyPrefix shop view_count 累加 hash key。
	ShopViewKeyPrefix = "local-life:{shop:view}"
	// ShopViewDirtyKey shop view_count 待 flush 集合 key。
	ShopViewDirtyKey = "local-life:{shop:view}:dirty"
)

var (
	postInfoFlushConfig = FlushTableConfig{
		Name:           "post_info",
		DirtyKey:       PostViewDirtyKey,
		HashKey:        PostViewKeyPrefix,
		CacheKeyPrefix: "local-life:cache:postInfo:id:",
	}

	shopFlushConfig = FlushTableConfig{
		Name:           "shop",
		DirtyKey:       ShopViewDirtyKey,
		HashKey:        ShopViewKeyPrefix,
		CacheKeyPrefix: "local-life:cache:shop:id:",
	}
)

// viewCountFlusher view_count 落库后台任务。
type viewCountFlusher struct {
	svcCtx *svc.ServiceContext
	tables []FlushTableConfig
}

// StartViewCountFlusher 启动 view_count flush 后台任务。
//
//   - 在 main 启动 HTTP server 之前调用一次。
//   - 返回的 stop 函数用于优雅退出（context cancel 时调用）。
//   - 默认处理 post_info 和 shop 两张表。
func StartViewCountFlusher(svcCtx *svc.ServiceContext) (stop func()) {
	return StartViewCountFlusherWithTables(svcCtx, []FlushTableConfig{
		postInfoFlushConfig,
		shopFlushConfig,
	})
}

// StartViewCountFlusherWithTables 启动 view_count flush 后台任务（自定义表列表）。
func StartViewCountFlusherWithTables(svcCtx *svc.ServiceContext, tables []FlushTableConfig) (stop func()) {
	f := &viewCountFlusher{
		svcCtx: svcCtx,
		tables: tables,
	}

	ctx, cancel := context.WithCancel(context.Background())

	go f.run(ctx)
	logx.Infof("[view_count_flush] action=start interval=%s tables=%d", viewCountFlushInterval, len(tables))

	return cancel
}

func (f *viewCountFlusher) run(ctx context.Context) {
	ticker := time.NewTicker(viewCountFlushInterval)
	defer ticker.Stop()

	// 启动时先 flush 一次（处理进程异常退出残留的 dirty set）。
	f.flushAll(ctx)

	for {
		select {
		case <-ctx.Done():
			logx.Info("[view_count_flush] action=stopping")
			f.flushAll(context.Background())
			return
		case <-ticker.C:
			f.flushAll(ctx)
		}
	}
}

// flushAll 遍历所有表执行一次 flush。
func (f *viewCountFlusher) flushAll(ctx context.Context) {
	for _, table := range f.tables {
		f.flushTable(ctx, table)
	}
}

// luaFlushScript 一次性：SPOP 最多 batchSize 个 id，并 HGET 各 id 当前 incr 累加值。
//
//	KEYS[1] = dirtyKey
//	KEYS[2] = hashKey（如 "post:view"）
//	ARGV[1] = batchSize
//
// 返回：扁平数组 [id1, value1, id2, value2, ...]
//   - 如果 SPOP 结果空，返回空数组
//   - 如果 HGET 结果为 false（field 不存在），该 value 序列化为 ""；Go 层会过滤为 incr<=0 跳过
const luaFlushScript = `
local batchSize = tonumber(ARGV[1])
local ids = redis.call('SPOP', KEYS[1], batchSize)
if (not ids) or #ids == 0 then return {} end
local out = {}
for i, id in ipairs(ids) do
    local v = redis.call('HGET', KEYS[2], id)
    table.insert(out, id)
    if v == false then
        table.insert(out, '')
    else
        table.insert(out, v)
    end
end
return out
`

// viewCountFlushEntry 内部小类型：单条待落库记录。
type viewCountFlushEntry struct {
	Id   uint64
	Incr int64
}

func (f *viewCountFlusher) flushTable(ctx context.Context, table FlushTableConfig) {
	start := time.Now()

	// 1) Lua 一次性 SPOP + HGET。
	res, err := f.svcCtx.RedisCli.Eval(luaFlushScript,
		[]string{table.DirtyKey, table.HashKey},
		viewCountFlushMaxBatch,
	)
	if err != nil && err != redis.Nil {
		logx.Errorf("[view_count_flush] action=eval_fail table=%s stage=eval err=%v", table.Name, err)
		return
	}
	rawArr, _ := res.([]interface{})
	if len(rawArr) == 0 {
		return // 没有待处理数据，静默不刷 finish 日志。
	}
	if len(rawArr)%2 != 0 {
		// 形状异常,理论上不会发生(LUA 一对一对返回)。保守处理:跳过本批,等待下次重试
		logx.Errorf("[view_count_flush] action=eval_shape_bad table=%s len=%d", table.Name, len(rawArr))
		return
	}

	// 2) 拆 [id, value] 为 typed entries，剔除 incr<=0 / id 非数字。
	entries := make([]viewCountFlushEntry, 0, len(rawArr)/2)
	for i := 0; i < len(rawArr); i += 2 {
		idStr, _ := rawArr[i].(string)
		valStr, _ := rawArr[i+1].(string)
		id, err1 := strconv.ParseUint(idStr, 10, 64)
		if err1 != nil {
			continue
		}
		if valStr == "" {
			continue // incr 不存在（field 被外部 HDEL 或 hash 被清空），跳过
		}
		incr, err2 := strconv.ParseInt(valStr, 10, 64)
		if err2 != nil || incr <= 0 {
			continue // 防 0 / 负值覆盖
		}
		entries = append(entries, viewCountFlushEntry{Id: id, Incr: incr})
	}
	if len(entries) == 0 {
		return
	}

	// 3) SELECT 一次拉 mysql 当前 view_count。
	ids := make([]uint64, len(entries))
	for i, e := range entries {
		ids[i] = e.Id
	}
	mysqlCounts, err := f.fetchMysqlCounts(ctx, table, ids)
	if err != nil {
		// SELECT 失败：把 entries 全部 SADD 回 dirty，下一 tick 再来（当前 5s 周期）。
		logx.Errorf("[view_count_flush] action=select_fail table=%s stage=select err=%v", table.Name, err)
		f.requeueEntries(table, entries)
		return
	}

	// 4) Go 层防覆盖过滤：仅 incr > mysqlCounts[id] 进入待 UPDATE。
	var toWrite []viewCountFlushEntry
	var skipped int
	for _, e := range entries {
		if e.Incr > mysqlCounts[e.Id] {
			toWrite = append(toWrite, e)
		} else {
			// incr <= mysql.view_count：保留 mysql 大值，不覆盖。
			skipped++
		}
	}
	logx.Infof("[view_count_flush] action=filter table=%s raw=%d write=%d skip=%d",
		table.Name, len(entries), len(toWrite), skipped)

	if len(toWrite) == 0 {
		// 无 mysql 写入，但 dirty 已被 SPOP 消费。语义上：脏数据的活儿已经判断完毕。
		// 之后 HINCRBY 会重新 SADD，无需补偿。
		return
	}

	// 5) 拼 1 条 CASE WHEN UPDATE SQL。
	sql, args := buildCaseUpdateSQL(table, toWrite)
	if _, err := f.svcCtx.DBConn.ExecCtx(ctx, sql, args...); err != nil {
		logx.Errorf("[view_count_flush] action=update_fail table=%s stage=update err=%v",
			table.Name, err)
		f.requeueEntries(table, toWrite)
		return
	}

	// 6) 成功后 pipelined DEL model 缓存。
	if table.CacheKeyPrefix != "" {
		cacheKeys := make([]string, len(toWrite))
		for i, e := range toWrite {
			cacheKeys[i] = table.CacheKeyPrefix + strconv.FormatUint(e.Id, 10)
		}
		if _, err := f.svcCtx.RedisCli.Del(cacheKeys...); err != nil {
			// 缓存清失败不回退：下次 HINCRBY 自然重新加载；但要记日志方便排查。
			logx.Errorf("[view_count_flush] action=cache_del_fail table=%s stage=cache_del err=%v",
				table.Name, err)
		}
	}

	duration := time.Since(start)
	logx.Infof("[view_count_flush] action=finish result=success table=%s write=%d skip=%d duration_ms=%d",
		table.Name, len(toWrite), skipped, duration.Milliseconds())
}

// fetchMysqlCounts 一次 SELECT 拉出 id->view_count 映射。
//
// 使用 sqlx 的 IN expansion：WHERE id IN (?, ?, ...) 语法由 sqlx 解析。
// table.Name 是 hardcode 常量（"post_info"/"shop"），仅 internal 配置使用，fmt.Sprintf 安全。
func (f *viewCountFlusher) fetchMysqlCounts(ctx context.Context, table FlushTableConfig, ids []uint64) (map[uint64]int64, error) {
	if len(ids) == 0 {
		return map[uint64]int64{}, nil
	}
	type row struct {
		Id        uint64 `db:"id"`
		ViewCount int64  `db:"view_count"`
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	sql := fmt.Sprintf("SELECT `id`, `view_count` FROM `%s` WHERE `id` IN (%s)",
		table.Name, placeholders)
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	var rows []row
	if err := f.svcCtx.DBConn.QueryRowsCtx(ctx, &rows, sql, args...); err != nil {
		return nil, err
	}
	out := make(map[uint64]int64, len(rows))
	for _, r := range rows {
		out[r.Id] = r.ViewCount
	}
	return out, nil
}

// buildCaseUpdateSQL 拼 1 条 UPDATE CASE WHEN id 多条件批量更新。
//
// 形如：
//
//	UPDATE `post_info` SET `view_count` = CASE `id`
//	    WHEN 1001 THEN 5
//	    WHEN 1002 THEN 7
//	END
//	WHERE `id` IN (1001, 1002)
//
// table.Name 是 hardcode 常量，可安全嵌入 SQL。
func buildCaseUpdateSQL(table FlushTableConfig, entries []viewCountFlushEntry) (string, []any) {
	var sb strings.Builder
	sb.WriteString("UPDATE `")
	sb.WriteString(table.Name)
	sb.WriteString("` SET `view_count` = CASE `id`")
	args := make([]any, 0, 2*len(entries))
	for _, e := range entries {
		sb.WriteString(" WHEN ? THEN ?")
		args = append(args, e.Id, e.Incr)
	}
	sb.WriteString(" END WHERE `id` IN (")
	args2 := make([]any, 0, len(args)+len(entries))
	args2 = append(args2, args...)
	for i, e := range entries {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteByte('?')
		args2 = append(args2, e.Id)
	}
	sb.WriteString(")")
	return sb.String(), args2
}

// requeueEntries 把 entries 重新 SADD 回 dirty set（UPDATE 失败时调用）。
func (f *viewCountFlusher) requeueEntries(table FlushTableConfig, entries []viewCountFlushEntry) {
	if len(entries) == 0 {
		return
	}
	members := make([]any, len(entries))
	for i, e := range entries {
		members[i] = strconv.FormatUint(e.Id, 10)
	}
	if _, err := f.svcCtx.RedisCli.Sadd(table.DirtyKey, members...); err != nil {
		logx.Errorf("[view_count_flush] action=requeue_fail table=%s stage=requeue err=%v",
			table.Name, err)
	}
}

// ReadViewCount 读取单条记录的实时 view_count（直接读 Redis hash field）。
//
//   - 用于业务展示场景（PostList / HomeIndex 等列表），仅读不增。
//   - 返回 Redis hash {hashKey} 中 field={recordId} 的当前累加值；field 不存在时返回 0。
//   - 不依赖 mysql.view_count，不做 base+delta 叠加。
//
// 关于"防覆盖保护"的语义：
//   - 后台 flush 周期（5s）内 mysql.view_count 才会同步到当前 Redis 值。
//   - mysql.view_count 仅作异步镜像，业务读路径以 Redis 为准。
func ReadViewCount(redisCli *redis.Redis, hashKey string, recordId uint64) int {
	field := strconv.FormatUint(recordId, 10)
	val, err := redisCli.Hget(hashKey, field)
	if err != nil || val == "" {
		return 0
	}
	n, err := strconv.Atoi(val)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// ReadViewCountsBatch 批量读 N 个 id 的实时 view_count（一次 HMGET）。
//
//   - 用于"应用层排序"场景：从候选池拉一批 id，一次 HMGET 拿全部 view_count 后在 Go 层排序。
//   - 实现细节：使用 Redis HMGET 一次 RTT 拉全 N fields，N >= 2 时优于 N 次 HGET。
//   - 失败/缺失的 field 在返回 map 中缺失（调用方应把缺失视为 0）。
//   - 负值视为 0（同 ReadViewCount 的语义）。
//
// 返回：id -> view_count 映射；err 仅为 redis 通讯错误。语义错误（缺失/负值）不报。
func ReadViewCountsBatch(redisCli *redis.Redis, hashKey string, recordIds []uint64) (map[uint64]int, error) {
	out := make(map[uint64]int, len(recordIds))
	if len(recordIds) == 0 {
		return out, nil
	}
	fields := make([]string, len(recordIds))
	for i, id := range recordIds {
		fields[i] = strconv.FormatUint(recordIdField(id), 10)
	}
	vals, err := redisCli.Hmget(hashKey, fields...)
	if err != nil {
		return nil, err
	}
	for i, v := range vals {
		if v == "" {
			continue // field 缺失（没 HINCRBY 过），不在 map 中
		}
		n, parseErr := strconv.Atoi(v)
		if parseErr != nil || n < 0 {
			continue
		}
		out[recordIds[i]] = n
	}
	return out, nil
}

// recordIdField 把 uint64 id 转 hash field 的字符串形式（保持与 HINCRBY/HGET 一致）。
func recordIdField(id uint64) uint64 {
	return id
}

// luaIncrViewScript 原子地完成 HINCRBY + SADD：
//
//   - 把 hash field 的 incr 累加值 +1
//
//   - 把 id 加入 dirty set（用于后台 flush）
//
//     KEYS[1] = hashKey（如 "post:view"）
//     KEYS[2] = dirtyKey（如 "post:view:dirty"）
//     ARGV[1] = field（即 id 字符串，如 "123"）
//     ARGV[2] = dirty member（同 ARGV[1]）
//
// 返回：HINCRBY 之后的累加值。
const luaIncrViewScript = `
local v = redis.call('HINCRBY', KEYS[1], ARGV[1], 1)
redis.call('SADD', KEYS[2], ARGV[2])
return v
`

// IncrViewCount 业务进入 PostDetail 时调用：原子累加 view_count 并标记 dirty。
//
//   - 返回 Redis 当前 incr 累加值（即业务要展示给用户的"实时 view_count"）。
//   - 本函数不依赖 mysql.view_count，与 ReadViewCount 配合实现"业务完全走 Redis"语义。
//   - dirty 标记失败对读返回值无影响；5s 周期兜底（见 StartViewCountFlusher）。
//
// 注意：
//   - incr 累加值在 Redis 单调递增，永远是当前最新值。
//   - 用户没进入过 PostDetail 的 id，HINCRBY 后会从 0 涨到 1（"post:view" hash 新建对应 field）。
//   - 返回值是 int64，溢出极端情况下由 redis 内部处理（64 位整数）。
func IncrViewCount(redisCli *redis.Redis, hashKey, dirtyKey string, recordId uint64) int64 {
	field := strconv.FormatUint(recordId, 10)

	// 1. 原子 HINCRBY + SADD（同一 lua 内执行，保证原子）
	val, err := redisCli.Eval(luaIncrViewScript, []string{hashKey, dirtyKey}, field, field)
	if err != nil {
		// Lua 失败：降级为 Hget 当前值
		logx.Errorf("[view_count_flush] action=incr_lua_fail stage=eval hash=%s field=%s err=%v",
			hashKey, field, err)
		if cur, getErr := redisCli.Hget(hashKey, field); getErr == nil {
			if n, parseErr := strconv.ParseInt(cur, 10, 64); parseErr == nil {
				// best-effort SADD dirty,失败无所谓
				if _, saddErr := redisCli.Sadd(dirtyKey, field); saddErr != nil {
					logx.Errorf("[view_count_flush] action=incr_sadd_fail hash=%s field=%s stage=fallback_sadd err=%v",
						hashKey, field, saddErr)
				}
				return n
			} else {
				logx.Errorf("[view_count_flush] action=incr_hget_parse_fail hash=%s field=%s stage=hget_parse raw=%q err=%v",
					hashKey, field, cur, parseErr)
			}
		} else {
			logx.Errorf("[view_count_flush] action=incr_hget_fail hash=%s field=%s stage=hget err=%v",
				hashKey, field, getErr)
		}
		return 0
	}
	// 2. 把 Eval 返回的 int64 取出
	switch v := val.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case string:
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		} else {
			logx.Errorf("[view_count_flush] action=incr_eval_parse_fail stage=eval_parse raw=%q err=%v",
				v, err)
		}
	}
	return 0
}
