-- ============================================================
-- 2026-11-18 搜索功能:加 FULLTEXT 全文索引(ngram 解析器)
-- ============================================================
--
-- 用途:给帖子(title/content)和商家(name/address/description)加中文全文检索索引。
--
-- 为什么用 ngram:
--   MySQL 默认的 default 解析器按空格切词,中文整句是一个 token,
--   AGAINST('自行车') 搜不到「二手自行车转让」。ngram 按 2-gram 切分,
--   中文检索才能正常工作。
--
-- 为什么不用 Elasticsearch:
--   当前帖子量级极小(个位数~十万级),ES 引入部署/监控/双写一致性成本不划算。
--   MySQL 原生 FULLTEXT 够用,后续真要上 ES 再抽 SearchPost 接口切换。
--
-- 注意:
--   1. FULLTEXT 索引在 MySQL 上是 append-only 的,加索引期间会锁表(小表很快)
--   2. 索引是增量构建,存量数据会被回填,无需手动 rebuild
--   3. 新增/更新帖子后 MySQL 会自动维护索引,无需额外同步

-- ---------- post_info:帖子标题 + 正文 ----------
ALTER TABLE `post_info`
    ADD FULLTEXT KEY `ft_post_search` (`title`, `content`) WITH PARSER ngram;

-- ---------- shop:商家名称 + 地址 + 介绍 ----------
ALTER TABLE `shop`
    ADD FULLTEXT KEY `ft_shop_search` (`name`, `address`, `description`) WITH PARSER ngram;
