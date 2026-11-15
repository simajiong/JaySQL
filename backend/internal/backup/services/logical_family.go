package services

import "strings"

// KingbaseES 与 PostgreSQL 共用同一条逻辑备份/恢复通道：
// 协议一致，sys_dump / sys_restore / ksql 的参数与 pg_dump / pg_restore / psql 同源，
// 自定义格式（-Fc）的产物也同为 PGDMP 容器。
//
// 差异只有三处：工具名、默认安装目录、版本串格式（V009R001C010 而非 16.2）。
// 这些差异统一收敛在本文件，避免在二十多个分派点上散装写 dbType == "Kingbase"，
// 也避免以后再加同类国产库时继续复制粘贴。
//
// 注意：物理备份（pgBackRest / XtraBackup）不属于本通道 —— 金仓没有 pgBackRest 工具链，
// pg_physical.go、pgbackrest_precheck.go 里的 "PostgreSQL" 判断必须保持原样。

// 逻辑工具用途
const (
	logicalToolRoleDump    = "dump"
	logicalToolRoleRestore = "restore"
	logicalToolRoleQuery   = "query"
)

// isKingbaseDBType 归一到金仓的各种写法（大小写、有无空格/中划线）。
// 与 dbfamily 包的归一结果保持一致：Kingbase 属于 PostgreSQL 家族。
func isKingbaseDBType(dbType string) bool {
	switch strings.ToLower(strings.TrimSpace(dbType)) {
	case "kingbase", "kingbasees", "kingbase es", "kingbase-es", "kingbasev8", "kingbase v8":
		return true
	}
	return false
}

// isPostgresLogicalFamily 判断是否走 PG 逻辑备份/恢复通道（PostgreSQL + KingbaseES）。
func isPostgresLogicalFamily(dbType string) bool {
	return strings.EqualFold(strings.TrimSpace(dbType), "PostgreSQL") || isKingbaseDBType(dbType)
}

// logicalToolName 返回指定用途的客户端工具名。
//   - PostgreSQL：pg_dump / pg_restore / psql
//   - KingbaseES：sys_dump / sys_restore / ksql
func logicalToolName(dbType string, role string) string {
	switch role {
	case logicalToolRoleRestore:
		if isKingbaseDBType(dbType) {
			return "sys_restore"
		}
		return "pg_restore"
	case logicalToolRoleQuery:
		if isKingbaseDBType(dbType) {
			return "ksql"
		}
		return "psql"
	default:
		if isKingbaseDBType(dbType) {
			return "sys_dump"
		}
		return "pg_dump"
	}
}

// logicalToolNameCandidates 返回该用途下可接受的工具名（按优先级）。
// 金仓安装包里个别版本保留了 pg_* 兼容软链，因此把两种名字都作为候选，
// 命中哪个都算可用 —— 探测脚本里逐个尝试即可，不必要求运维先建软链。
func logicalToolNameCandidates(dbType string, role string) []string {
	primary := logicalToolName(dbType, role)
	if !isKingbaseDBType(dbType) {
		return []string{primary}
	}
	var fallback string
	switch role {
	case logicalToolRoleRestore:
		fallback = "pg_restore"
	case logicalToolRoleQuery:
		fallback = "psql"
	default:
		fallback = "pg_dump"
	}
	return []string{primary, fallback}
}

// logicalDefaultDatabase 返回未配置 extra.database 时的默认连接库。
// PG 惯例是 postgres；金仓 V8 自带 test / kingbase / security 三个库，test 是装机默认库。
func logicalDefaultDatabase(dbType string) string {
	if isKingbaseDBType(dbType) {
		return "test"
	}
	return "postgres"
}

// logicalDefaultOSUser 返回数据库进程的默认操作系统用户，用于切换执行身份。
func logicalDefaultOSUser(dbType string) string {
	if isKingbaseDBType(dbType) {
		return "kingbase"
	}
	return "postgres"
}

// logicalToolInstallDirs 返回该库类型常见的客户端安装目录，
// 供工具探测脚本与本地候选路径查找（金仓默认装在 /opt/Kingbase/ES/V8/Server/bin）。
func logicalToolInstallDirs(dbType string) []string {
	if !isKingbaseDBType(dbType) {
		return nil
	}
	return []string{
		"/opt/Kingbase/ES/V8/Server/bin",
		"/opt/Kingbase/ES/V8/bin",
		"/opt/Kingbase/Server/bin",
		"/home/kingbase/Kingbase/ES/V8/Server/bin",
		"/usr/local/Kingbase/ES/V8/Server/bin",
	}
}

// logicalServerVersionForTool 返回用于挑选客户端版本的版本串。
// 金仓版本形如 V009R001C010，既不是 PG 的 x.y，也无法按 major 对齐客户端；
// 返回空串让上层跳过「客户端 major 必须等于服务端 major」的强校验（该约束只对 PG 有意义）。
func logicalServerVersionForTool(dbType string, serverVersion string) string {
	if isKingbaseDBType(dbType) {
		return ""
	}
	return serverVersion
}

// requiresLogicalExactMajor 判断是否需要「客户端 major == 服务端 major」的强校验。
func requiresLogicalExactMajor(dbType string, toolName string) bool {
	if isKingbaseDBType(dbType) {
		return false
	}
	return requiresPostgresExactMajor(toolName)
}

// sameLogicalFamily 判断备份集与目标实例是否属于同一备份体系。
// 直接比字符串会把 Kingbase / KingbaseES 的写法差异误判成「类型不一致」，
// 从而在恢复向导第一步就拦住，所以同族内一律放行；跨族仍然拒绝。
func sameLogicalFamily(setDBType string, instanceDBType string) bool {
	if strings.EqualFold(strings.TrimSpace(setDBType), strings.TrimSpace(instanceDBType)) {
		return true
	}
	return isPostgresLogicalFamily(setDBType) && isPostgresLogicalFamily(instanceDBType)
}

// logicalDBLabel 用于日志与报错文案，避免把金仓写成 PostgreSQL 误导排查。
func logicalDBLabel(dbType string) string {
	if isKingbaseDBType(dbType) {
		return "KingbaseES"
	}
	return "PostgreSQL"
}
