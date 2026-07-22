-- ============================================================
-- 小城便利贴 - 数据库初始化脚本
-- 数据库: local_life
-- 字符集: utf8mb4 / utf8mb4_unicode_ci
--
-- 用途:
--   - dev / 新环境首次部署时,跑这一份就能拿到完整的最新 schema
--   - 包含全部业务表 + 索引 + 默认管理员账号
--
-- 维护规则:
--   - 本文件即"当前最新完整结构",每次有 schema 变化直接改这里
--   - 需要给线上实例追增量时,写到 migrations/YYYY-MM-DD-<desc>.sql
--     并把同样变更同步回本文件,保持 dev / 生产结构一致
-- ============================================================

CREATE DATABASE IF NOT EXISTS `local_life`
    DEFAULT CHARACTER SET utf8mb4
    COLLATE utf8mb4_unicode_ci;

USE `local_life`;

-- ============================================================
-- 1. 用户表 (user)
-- ============================================================
DROP TABLE IF EXISTS `user`;
CREATE TABLE `user` (
    `id`          BIGINT       UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '用户ID(DB主键)',
    `openid`      VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '微信OpenID(业务标识符,JWT主载荷)',
    `unionid`     VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '微信UnionID(跨端合并预留,dev 阶段未填值)',
    `nickname`    VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '昵称',
    `avatar`      VARCHAR(512) NOT NULL DEFAULT '' COMMENT '头像URL(阿里云OSS)',
    `phone`       VARCHAR(20)  NOT NULL DEFAULT '' COMMENT '手机号',
    `status`      TINYINT      NOT NULL DEFAULT 1  COMMENT '状态: 1正常 2封禁',
    `created_at`  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    `updated_at`  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_openid` (`openid`),
    KEY `idx_unionid` (`unionid`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户表';

-- ============================================================
-- 2. 信息发布表 (post_info)
-- ============================================================
DROP TABLE IF EXISTS `post_info`;
CREATE TABLE `post_info` (
    `id`            BIGINT       UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '信息ID',
    `user_id`       BIGINT       UNSIGNED NOT NULL DEFAULT 0  COMMENT '发布用户ID',
    `type`          TINYINT      NOT NULL DEFAULT 1  COMMENT '类型: 1二手闲置 2房屋租售 3求职招聘 4便民求助',
    `title`         VARCHAR(128) NOT NULL DEFAULT '' COMMENT '标题',
    `images`        TEXT         COMMENT '图片组(JSON数组, 阿里云OSS URL)',
    `content`       TEXT         COMMENT '详情描述',
    `area`          VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '所在区域',
    `contact`       VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '联系方式',
    `latitude`      DOUBLE       NOT NULL DEFAULT 0  COMMENT '纬度',
    `longitude`     DOUBLE       NOT NULL DEFAULT 0  COMMENT '经度',
    `area_adcode`   CHAR(6)      NOT NULL DEFAULT '' COMMENT '行政区划编码(国标 6 位,区/县级)',
    `area_name`     VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '区/县名,如 都江堰市',
    `street_name`   VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '镇/街道名,文本冗余',
    `city_adcode`   CHAR(6)      NOT NULL DEFAULT '' COMMENT '市级 adcode,用于全市降级',
    `city_name`     VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '市级名称,如 珠海市,与 city_adcode 配对',
    `status`        TINYINT      NOT NULL DEFAULT 1  COMMENT '状态: 1待审核 2已通过 3已下架 4审核拒绝',
    `reject_reason` VARCHAR(255) NOT NULL DEFAULT '' COMMENT '拒绝原因',
    `view_count`    INT          UNSIGNED NOT NULL DEFAULT 0 COMMENT '浏览数',
    `comment_count` INT          UNSIGNED NOT NULL DEFAULT 0 COMMENT '评论数冗余',
    `created_at`    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    `updated_at`    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
    PRIMARY KEY (`id`),
    KEY `idx_user_id` (`user_id`),
    KEY `idx_type_status` (`type`, `status`),
    KEY `idx_created_at` (`created_at`),
    KEY `idx_post_area_status_created` (`area_adcode`, `status`, `created_at`),
    KEY `idx_post_city_status_created` (`city_adcode`, `status`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='信息发布表';

-- ============================================================
-- 3. 商家黄页表 (shop)
-- ============================================================
DROP TABLE IF EXISTS `shop`;
CREATE TABLE `shop` (
    `id`          BIGINT       UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '商家ID',
    `user_id`     BIGINT       UNSIGNED NOT NULL DEFAULT 0  COMMENT '入驻用户ID',
    `name`        VARCHAR(128) NOT NULL DEFAULT '' COMMENT '店铺名称',
    `category`    TINYINT      NOT NULL DEFAULT 9  COMMENT '行业分类: 1家政 2维修 3开锁 4搬家 5跑腿 6美业 7教育 8美食 9其他',
    `address`     VARCHAR(256) NOT NULL DEFAULT '' COMMENT '地址',
    `latitude`    DOUBLE       NOT NULL DEFAULT 0  COMMENT '纬度',
    `longitude`   DOUBLE       NOT NULL DEFAULT 0  COMMENT '经度',
    `area_adcode` CHAR(6)      NOT NULL DEFAULT '' COMMENT '行政区划编码(国标 6 位,区/县级)',
    `area_name`   VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '区/县名,如 都江堰市',
    `street_name` VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '镇/街道名,文本冗余',
    `city_adcode` CHAR(6)      NOT NULL DEFAULT '' COMMENT '市级 adcode,用于全市降级',
    `city_name`   VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '市级名称,如 珠海市,与 city_adcode 配对',
    `phone`       VARCHAR(20)  NOT NULL DEFAULT '' COMMENT '联系电话',
    `description` TEXT         COMMENT '店铺介绍',
    `images`      TEXT         COMMENT '图片组(JSON数组, 阿里云OSS URL)',
    `status`        TINYINT      NOT NULL DEFAULT 1  COMMENT '状态: 1待审核 2已通过 3已下架 4审核拒绝',
    `reject_reason` VARCHAR(255) NOT NULL DEFAULT '' COMMENT '拒绝原因',
    `view_count`    INT          UNSIGNED NOT NULL DEFAULT 0 COMMENT '浏览数',
    `created_at`    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    `updated_at`    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
    PRIMARY KEY (`id`),
    KEY `idx_user_id` (`user_id`),
    KEY `idx_category_status` (`category`, `status`),
    KEY `idx_shop_area_status_created` (`area_adcode`, `status`, `created_at`),
    KEY `idx_shop_city_status_created` (`city_adcode`, `status`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='商家黄页表';

-- ============================================================
-- 4. 公告表 (notice)
-- ============================================================
DROP TABLE IF EXISTS `notice`;
CREATE TABLE `notice` (
    `id`         BIGINT       UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '公告ID',
    `title`      VARCHAR(128) NOT NULL DEFAULT '' COMMENT '标题',
    `content`    TEXT         COMMENT '内容',
    `is_top`     TINYINT      NOT NULL DEFAULT 0  COMMENT '是否置顶: 0否 1是',
    `status`     TINYINT      NOT NULL DEFAULT 1  COMMENT '状态: 1正常 2下架',
    `created_at` DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    `updated_at` DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
    PRIMARY KEY (`id`),
    KEY `idx_is_top_status` (`is_top`, `status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='公告表';

-- ============================================================
-- 5. 轮播图表 (banner)
-- ============================================================
DROP TABLE IF EXISTS `banner`;
CREATE TABLE `banner` (
    `id`          BIGINT       UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '轮播图ID',
    `image`       VARCHAR(512) NOT NULL DEFAULT '' COMMENT '图片URL(阿里云OSS)',
    `jump_type`   TINYINT      NOT NULL DEFAULT 0  COMMENT '跳转类型: 0无跳转 1公告 2商家 3信息 4外链',
    `jump_target` VARCHAR(512) NOT NULL DEFAULT '' COMMENT '跳转目标(公告ID/商家ID/信息ID/外链URL)',
    `sort_order`  INT          NOT NULL DEFAULT 0  COMMENT '排序(数值越小越靠前)',
    `title`       VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '标题(用于alt)',
    `status`      TINYINT      NOT NULL DEFAULT 1  COMMENT '状态: 1正常 2下架',
    `created_at`  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    `updated_at`  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
    PRIMARY KEY (`id`),
    KEY `idx_sort_status` (`sort_order`, `status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='轮播图表';

-- ============================================================
-- 6. 管理员表 (admin)
-- ============================================================
DROP TABLE IF EXISTS `admin`;
CREATE TABLE `admin` (
    `id`         BIGINT       UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '管理员ID',
    `username`   VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '用户名',
    `password`   VARCHAR(128) NOT NULL DEFAULT '' COMMENT '密码(bcrypt)',
    `role`       TINYINT      NOT NULL DEFAULT 1  COMMENT '角色: 1超级管理员',
    `status`     TINYINT      NOT NULL DEFAULT 1  COMMENT '状态: 1正常 2禁用',
    `created_at` DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    `updated_at` DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_username` (`username`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='管理员表';

-- ============================================================
-- 初始化默认管理员 (用户名: admin, 密码: admin123, bcrypt加密)
-- bcrypt 哈希用 golang.org/x/crypto/bcrypt 工具生成
-- 上线后请立即通过管理后台修改密码！
-- ============================================================
INSERT INTO `admin` (`username`, `password`, `role`, `status`) VALUES
('admin', '$2a$10$6UZcbMYJ.M0HhL1eKsuSxOXixI0klXwvLU5flucZ8DZMYPCbjFNNC', 1, 1);

-- ============================================================
-- 7. 帖子评论表 (comment)
-- ============================================================
DROP TABLE IF EXISTS `comment`;
CREATE TABLE `comment` (
    `id`                     BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `post_id`                BIGINT UNSIGNED NOT NULL,
    `parent_id`              BIGINT UNSIGNED NOT NULL DEFAULT '0' COMMENT '0=一级评论;>0=子回复 direct parent',
    `reply_to_uid`           VARCHAR(64)     NOT NULL DEFAULT '',
    `reply_to_type`          TINYINT         NOT NULL DEFAULT '0' COMMENT '0=无 1=user 2=shop',
    `root_id`                BIGINT UNSIGNED NOT NULL DEFAULT '0' COMMENT '一级评论=自己id;子回复=顶层一级评论id (aggregate query key)',
    `author_id`              VARCHAR(64)     NOT NULL,
    `author_type`            TINYINT         NOT NULL COMMENT '1=user 2=shop',
    `author_name_snapshot`   VARCHAR(64)     NOT NULL COMMENT '快照:发评时一次性写入,改名不消失',
    `author_avatar_snapshot` VARCHAR(255)    NOT NULL DEFAULT '' COMMENT '快照:发评时一次性写入,改名不消失',
    `content`                VARCHAR(1000)   NOT NULL,
    `image_urls`             JSON            NULL,
    `like_count`             INT UNSIGNED    NOT NULL DEFAULT '0',
    `reply_count`            INT UNSIGNED    NOT NULL DEFAULT '0',
    `report_count`           INT UNSIGNED    NOT NULL DEFAULT '0',
    `status`                 TINYINT         NOT NULL DEFAULT '1' COMMENT '1=正常 2=已删除 3=审核中',
    `deleted_by`             VARCHAR(64)     NOT NULL DEFAULT '' COMMENT '影子删除执行人 (user uid 或 admin),便于审计',
    `created_at`             DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
    `updated_at`             DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (`id`),
    KEY `idx_post_root_created` (`post_id`, `root_id`, `status`, `created_at`),
    KEY `idx_post_parent_created` (`post_id`, `parent_id`, `status`, `created_at`),
    KEY `idx_author_created` (`author_type`, `author_id`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='帖子评论';

-- ============================================================
-- 8. 评论点赞表 (comment_like)
-- ============================================================
DROP TABLE IF EXISTS `comment_like`;
CREATE TABLE `comment_like` (
    `id`         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `comment_id` BIGINT UNSIGNED NOT NULL,
    `user_id`    VARCHAR(64)     NOT NULL,
    `created_at` DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_comment_user` (`comment_id`, `user_id`),
    KEY `idx_user_created` (`user_id`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='评论点赞(仅 user)';

-- ============================================================
-- 9. 评论举报表 (comment_report)
-- ============================================================
DROP TABLE IF EXISTS `comment_report`;
CREATE TABLE `comment_report` (
    `id`            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `comment_id`    BIGINT UNSIGNED NOT NULL,
    `reporter_uid`  VARCHAR(64)     NOT NULL,
    `reporter_type` TINYINT         NOT NULL COMMENT '1=user 2=shop',
    `reason`        VARCHAR(255)    NOT NULL,
    `description`   VARCHAR(500)    NOT NULL DEFAULT '',
    `status`        TINYINT         NOT NULL DEFAULT '1' COMMENT '1=待处理 2=已采纳 3=已驳回',
    `created_at`    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
    `processed_at`  DATETIME        NULL,
    `processed_by`  VARCHAR(64)     NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_comment_reporter` (`comment_id`, `reporter_uid`),
    KEY `idx_status_created` (`status`, `created_at`),
    KEY `idx_comment` (`comment_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='评论举报';

-- ============================================================
-- 10. admin 操作审计表 (admin_operation_log)
-- ============================================================
DROP TABLE IF EXISTS `admin_operation_log`;
CREATE TABLE `admin_operation_log` (
    `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `admin_id`    BIGINT UNSIGNED NOT NULL                COMMENT '操作人(admin.id)',
    `action`      VARCHAR(64)     NOT NULL                COMMENT '操作类型,如 comment_audit_keep/hide/delete',
    `target_type` VARCHAR(32)     NOT NULL                COMMENT '目标类型,如 comment',
    `target_id`   BIGINT UNSIGNED NOT NULL                COMMENT '目标 ID, 如 comment_id',
    `reason`      VARCHAR(500)    NOT NULL DEFAULT ''     COMMENT '操作理由/admin 输入',
    `created_at`  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (`id`),
    KEY `idx_admin_action_created` (`admin_id`, `action`, `created_at`),
    KEY `idx_target` (`target_type`, `target_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='admin 操作审计日志';