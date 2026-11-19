-- ============================================================
-- 共享底座：chatroom 库建表
-- ============================================================
-- 内容来自 chatroom 各服务的 migrations/*.sql（表结构权威是 ent schema，
-- 服务启动时会自动增量对齐；这里只是让空库也能直接用，不依赖 Go 运行时）。
--
-- 四个服务各管各的表，都放在 chatroom 这一个 schema 里：
--   users                ← user
--   friends              ← friend
--   conversations / conversation_members ← conversation
--   messages             ← message
--
-- 只在 mysql 数据目录为空（首次创建）时执行。底座跑过之后本文件
-- 不会自动重跑，需要手动补执行（见 README.md）。

USE `chatroom`;

-- ------------------------------------------------------------
-- chatroom-user
-- ------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `users` (
    `id`                   CHAR(36) NOT NULL COMMENT '用户 uid, UUIDv7',
    `username`             VARCHAR(255) COLLATE utf8mb4_bin NOT NULL COMMENT '登录用户名, 唯一',
    `nickname`             VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT '展示昵称',
    `avatar`               VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT '头像 URL',
    `password_hash`        VARCHAR(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'bcrypt 哈希, 60 字节',
    `created_at`           TIMESTAMP NOT NULL COMMENT '注册时间',
    `email`                VARCHAR(255) COLLATE utf8mb4_bin DEFAULT NULL COMMENT '邮箱, 可选, 不唯一',
    `phone`                VARCHAR(255) COLLATE utf8mb4_bin DEFAULT NULL COMMENT '手机号, 可选, 全局唯一',
    `is_visible_to_stranger` TINYINT(1) NOT NULL DEFAULT '0' COMMENT '是否对陌生人可见',
    `status`               TINYINT UNSIGNED NOT NULL DEFAULT '1' COMMENT '账号状态 1=正常 0=禁用',
    PRIMARY KEY (`id`),
    UNIQUE KEY `user_username` (`username`),
    UNIQUE KEY `user_phone` (`phone`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '用户表';

-- ------------------------------------------------------------
-- chatroom-friend
-- ------------------------------------------------------------
-- 一条记录代表 user_id 对 friend_id 提过申请 / 已是好友，user_id 是发起方。
CREATE TABLE IF NOT EXISTS `friends` (
    `id`         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '主键, 自增',
    `user_id`    CHAR(36) NOT NULL COMMENT '发起方 uid',
    `friend_id`  CHAR(36) NOT NULL COMMENT '目标方 uid',
    `status`     TINYINT UNSIGNED NOT NULL DEFAULT '0' COMMENT '状态 0=PENDING 1=ACCEPTED 2=REJECTED',
    `created_at` TIMESTAMP NOT NULL COMMENT '申请发起时间',
    `updated_at` TIMESTAMP NOT NULL COMMENT '状态变更时间',
    `message`    VARCHAR(200) COLLATE utf8mb4_bin DEFAULT NULL COMMENT '申请附言, 可选, ≤ 200 字符',
    PRIMARY KEY (`id`),
    UNIQUE KEY `friend_user_id_friend_id` (`user_id`, `friend_id`),
    KEY `friend_friend_id` (`friend_id`),
    KEY `friend_user_id_id` (`user_id`, `id`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '好友申请表';

-- ------------------------------------------------------------
-- chatroom-conversation
-- ------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `conversations` (
    `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '主键, 自增',
    `type`        TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '会话类型 1=单聊 2=群聊',
    `name`        VARCHAR(128) NOT NULL DEFAULT '' COMMENT '群名, 单聊为空, ≤ 128 字符',
    `owner_id`    CHAR(36) NOT NULL DEFAULT '' COMMENT '群主 uid, 单聊为空',
    -- 单聊的成员对键 "uid1,uid2"（两个 uid 升序）。群聊为 NULL。
    -- 唯一索引是「单聊唯一」的数据库级约束，并发建单聊时后到的一方被索引挡下。
    -- MySQL 唯一索引不约束 NULL，所以群聊互不冲突。
    `pair_key`    VARCHAR(80) DEFAULT NULL COMMENT '单聊成员对键 ''uid1,uid2'', 群聊为 NULL',
    `created_at`  TIMESTAMP NOT NULL COMMENT '创建时间',
    -- datetime(6) 微秒精度：秒精度下同一秒建的会话排序分不出先后。
    `updated_at`  DATETIME(6) NOT NULL COMMENT '最后活动时间',
    PRIMARY KEY (`id`),
    UNIQUE KEY `conversation_pair_key` (`pair_key`),
    KEY `conversation_updated_at` (`updated_at`),
    KEY `conversation_owner_id` (`owner_id`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '会话表';

CREATE TABLE IF NOT EXISTS `conversation_members` (
    `id`              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '主键, 自增',
    `user_id`         CHAR(36) NOT NULL COMMENT '成员 uid',
    `unread_count`    INT NOT NULL DEFAULT 0 COMMENT '未读消息数',
    `joined_at`       TIMESTAMP NOT NULL COMMENT '加入时间',
    `conversation_id` BIGINT UNSIGNED NOT NULL COMMENT '会话 id',
    PRIMARY KEY (`id`),
    UNIQUE KEY `conversationmember_conversation_id_user_id` (`conversation_id`, `user_id`),
    KEY `conversationmember_user_id_conversation_id` (`user_id`, `conversation_id`)
    -- 故意不建外键。引用完整性靠应用层保证：成员行由 repo 在同一事务里
    -- 跟着会话一起写 / 删。
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '会话成员表';

-- ------------------------------------------------------------
-- chatroom-message
-- ------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `messages` (
    `id`              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '主键, 自增',
    `conversation_id` BIGINT UNSIGNED NOT NULL COMMENT '会话 id',
    `sender_id`       CHAR(36) NOT NULL COMMENT '发送者 uid',
    `type`            TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '消息类型 1=文本 2=图片',
    `content`         TEXT NOT NULL COMMENT '消息内容/图片 URL',
    `created_at`      TIMESTAMP NOT NULL COMMENT '发送时间',
    PRIMARY KEY (`id`),
    -- 历史消息的主查询路径：某会话的消息按 id 倒序。
    -- id 是主键，InnoDB 会把它挂在二级索引末尾，所以这个索引天然覆盖
    -- conversation_id + id 的排序，游标分页不用回表。
    KEY `message_conversation_id_id` (`conversation_id`, `id`),
    KEY `message_sender_id` (`sender_id`)
    -- 故意不建外键。conversation_id 指向的会话可能已被解散，消息保留着
    -- （历史可查）。外键会挡住这件事。
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '消息表';
