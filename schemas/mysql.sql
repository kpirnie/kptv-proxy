SET SQL_MODE = "NO_AUTO_VALUE_ON_ZERO";
SET time_zone = "+00:00";

/*!40101 SET @OLD_CHARACTER_SET_CLIENT=@@CHARACTER_SET_CLIENT */;
/*!40101 SET @OLD_CHARACTER_SET_RESULTS=@@CHARACTER_SET_RESULTS */;
/*!40101 SET @OLD_COLLATION_CONNECTION=@@COLLATION_CONNECTION */;
/*!40101 SET NAMES utf8mb4 */;


DROP TABLE IF EXISTS `kp_channels`;
CREATE TABLE `kp_channels` (
  `id` bigint(20) NOT NULL,
  `name` varchar(256) NOT NULL,
  `m3u_attributes` longtext NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

DROP TABLE IF EXISTS `kp_epgs`;
CREATE TABLE `kp_epgs` (
  `id` bigint(20) NOT NULL,
  `name` varchar(128) NOT NULL,
  `url` varchar(2048) NOT NULL,
  `sort_order` tinyint(3) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

DROP TABLE IF EXISTS `kp_sd_accounts`;
CREATE TABLE `kp_sd_accounts` (
  `id` bigint(20) NOT NULL,
  `name` varchar(128) NOT NULL,
  `uname` varchar(128) NOT NULL,
  `pword` varchar(128) NOT NULL,
  `enabled` tinyint(1) NOT NULL,
  `days_to_fetch` int(11) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

DROP TABLE IF EXISTS `kp_sd_lineups`;
CREATE TABLE `kp_sd_lineups` (
  `id` bigint(20) NOT NULL,
  `sd_account_id` bigint(20) NOT NULL,
  `lineup_id` varchar(1024) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

DROP TABLE IF EXISTS `kp_settings`;
CREATE TABLE `kp_settings` (
  `id` bigint(20) NOT NULL,
  `the_key` varchar(128) NOT NULL,
  `the_value` longtext NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

DROP TABLE IF EXISTS `kp_sources`;
CREATE TABLE `kp_sources` (
  `id` bigint(20) NOT NULL,
  `name` varchar(128) NOT NULL,
  `uri` text NOT NULL,
  `uname` varchar(128) NOT NULL,
  `pword` varchar(128) NOT NULL,
  `sort_order` tinyint(3) NOT NULL,
  `max_cnx` tinyint(3) NOT NULL,
  `max_stream_to` char(32) NOT NULL,
  `retry_delay` char(32) NOT NULL,
  `max_retries` tinyint(2) NOT NULL,
  `max_failures` tinyint(2) NOT NULL,
  `min_data_size` int(6) NOT NULL,
  `user_agent` varchar(1024) NOT NULL,
  `req_origin` varchar(1024) NOT NULL,
  `req_referer` varchar(1024) NOT NULL,
  `live_inc_regex` text NOT NULL,
  `live_exc_regex` text NOT NULL,
  `series_inc_regex` text NOT NULL,
  `series_exc_regex` text NOT NULL,
  `vod_inc_regex` text NOT NULL,
  `vod_exc_regex` text NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

DROP TABLE IF EXISTS `kp_streams`;
CREATE TABLE `kp_streams` (
  `id` bigint(20) NOT NULL,
  `channel_id` bigint(20) NOT NULL,
  `source_id` bigint(20) NOT NULL,
  `s_order` tinyint(3) NOT NULL,
  `s_status` tinyint(2) NOT NULL,
  `s_hash` varchar(2048) NOT NULL,
  `s_url` varchar(2048) NOT NULL,
  `dead_reason` varchar(1024) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

DROP TABLE IF EXISTS `kp_xc_accounts`;
CREATE TABLE `kp_xc_accounts` (
  `id` bigint(20) NOT NULL,
  `name` varchar(128) NOT NULL,
  `uname` varchar(128) NOT NULL,
  `pword` varchar(128) NOT NULL,
  `max_cnx` tinyint(3) NOT NULL,
  `enable_live` tinyint(1) NOT NULL,
  `enable_series` tinyint(1) NOT NULL,
  `enable_vod` tinyint(1) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;


ALTER TABLE `kp_channels`
  ADD PRIMARY KEY (`id`),
  ADD UNIQUE KEY `name` (`name`);

ALTER TABLE `kp_epgs`
  ADD PRIMARY KEY (`id`);

ALTER TABLE `kp_sd_accounts`
  ADD PRIMARY KEY (`id`);

ALTER TABLE `kp_sd_lineups`
  ADD PRIMARY KEY (`id`);

ALTER TABLE `kp_settings`
  ADD PRIMARY KEY (`id`),
  ADD UNIQUE KEY `the_key` (`the_key`);

ALTER TABLE `kp_sources`
  ADD PRIMARY KEY (`id`);

ALTER TABLE `kp_streams`
  ADD PRIMARY KEY (`id`);

ALTER TABLE `kp_xc_accounts`
  ADD PRIMARY KEY (`id`);


ALTER TABLE `kp_channels`
  MODIFY `id` bigint(20) NOT NULL AUTO_INCREMENT;

ALTER TABLE `kp_epgs`
  MODIFY `id` bigint(20) NOT NULL AUTO_INCREMENT;

ALTER TABLE `kp_sd_accounts`
  MODIFY `id` bigint(20) NOT NULL AUTO_INCREMENT;

ALTER TABLE `kp_sd_lineups`
  MODIFY `id` bigint(20) NOT NULL AUTO_INCREMENT;

ALTER TABLE `kp_settings`
  MODIFY `id` bigint(20) NOT NULL AUTO_INCREMENT;

ALTER TABLE `kp_sources`
  MODIFY `id` bigint(20) NOT NULL AUTO_INCREMENT;

ALTER TABLE `kp_streams`
  MODIFY `id` bigint(20) NOT NULL AUTO_INCREMENT;

ALTER TABLE `kp_xc_accounts`
  MODIFY `id` bigint(20) NOT NULL AUTO_INCREMENT;

/*!40101 SET CHARACTER_SET_CLIENT=@OLD_CHARACTER_SET_CLIENT */;
/*!40101 SET CHARACTER_SET_RESULTS=@OLD_CHARACTER_SET_RESULTS */;
/*!40101 SET COLLATION_CONNECTION=@OLD_COLLATION_CONNECTION */;
