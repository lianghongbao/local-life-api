-- ============================================================
-- 共享底座：建库
-- ============================================================
-- 同一 MySQL 实例上给各应用一个独立 schema：
--   local_life  ← local-life-api
--   chatroom    ← chatroom 五个服务
--
-- 字符集统一 utf8mb4。各库的默认排序规则跟各自应用原本的建库脚本保持一致：
--   local_life → utf8mb4_unicode_ci（沿用 docs/sql/init.sql 的约定）
--   chatroom   → utf8mb4_bin（chatroom 的 users.username / users.phone
--                 唯一索引依赖大小写敏感的二进制比较）
--
-- 注意：建库时的 COLLATE 只是「默认值」。表级显式声明的 COLLATE 优先；
-- 没声明的表会继承库默认值。MySQL 8.0 的服务器默认是 utf8mb4_0900_ai_ci，
-- 所以凡是希望大小写敏感的表，都必须在表级显式写 COLLATE。
--
-- 只在 mysql 数据目录为空（首次创建）时执行。底座跑过之后本文件
-- 不会自动重跑，需要手动补执行（见 README.md）。

CREATE DATABASE IF NOT EXISTS `local_life`
    DEFAULT CHARACTER SET utf8mb4
    COLLATE utf8mb4_unicode_ci;

CREATE DATABASE IF NOT EXISTS `chatroom`
    DEFAULT CHARACTER SET utf8mb4
    COLLATE utf8mb4_bin;
