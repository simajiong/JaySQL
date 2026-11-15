package services

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gorm.io/datatypes"
)

var simpleIdentRe = regexp.MustCompile(`^[A-Za-z0-9_\-]+$`)
var simplePatternRe = regexp.MustCompile(`^[A-Za-z0-9_\-\*\%_\.]+$`)

func validateIdent(name string, label string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%s不能为空", label)
	}
	if len(name) > 255 {
		return fmt.Errorf("%s过长", label)
	}
	if !simpleIdentRe.MatchString(name) {
		return fmt.Errorf("%s仅支持字母、数字、下划线(_)与短横线(-)", label)
	}
	return nil
}

func validatePattern(p string, label string) error {
	v := strings.TrimSpace(p)
	if v == "" {
		return fmt.Errorf("%s不能为空", label)
	}
	if len(v) > 255 {
		return fmt.Errorf("%s过长", label)
	}
	if !simplePatternRe.MatchString(v) {
		return fmt.Errorf("%s仅支持字母、数字、下划线(_)、短横线(-)、点(.)与通配符(*,%%)", label)
	}
	return nil
}

type mysqlBackupSpec struct {
	Databases []mysqlBackupDatabase `json:"databases"`
}

type mysqlBackupDatabase struct {
	Name                 string   `json:"name"`
	Tables               []string `json:"tables"`
	ExcludeTables        []string `json:"exclude_tables"`
	ExcludeTablePatterns []string `json:"exclude_table_patterns"`
}

type pgBackupSpec struct {
	Databases []pgBackupDatabase `json:"databases"`
}

type pgBackupDatabase struct {
	Name                 string       `json:"name"`
	Schemas              []string     `json:"schemas"`
	Tables               []pgTableRef `json:"tables"`
	ExcludeTables        []pgTableRef `json:"exclude_tables"`
	ExcludeTablePatterns []string     `json:"exclude_table_patterns"`
}

type pgTableRef struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
}

type dumpJob struct {
	DBName               string
	FileName             string
	BackupType           string
	MySQLAll             bool
	MySQLDB              bool
	MySQLTabs            []string
	MySQLExcludeTabs     []string
	MySQLExcludePatterns []string

	PgSchemas         []string
	PgTables          []string
	PgExcludeTables   []string
	PgExcludePatterns []string
}

type normalizedObjectSpec struct {
	DriverType    string
	BackupType    string
	ObjectMode    string
	ObjectSpec    datatypes.JSON
	ObjectSummary string
	LegacyDBName  string
	Jobs          []dumpJob
}

// 产物流式压缩方式
const (
	// ArtifactCompressionNone 不压缩，字节流原样直写
	ArtifactCompressionNone = "none"
	// ArtifactCompressionZip 以 zip 容器打包（多条目场景，Deflate 压缩）
	ArtifactCompressionZip = "zip"
	// ArtifactCompressionGzip 以 gzip 单文件流压缩（单一大产物场景）
	ArtifactCompressionGzip = "gzip"
)

type BackupArtifactPlan struct {
	ArtifactName      string
	StreamCompression string
	// CompressionLevel 仅 gzip 模式有意义：FAST（吞吐优先）/ HIGH（压缩比优先）
	CompressionLevel string
}

// gzipArtifactName 为单文件产物补上 .gz 后缀，已带后缀时保持不变。
func gzipArtifactName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		trimmed = "all_databases.sql"
	}
	if strings.HasSuffix(strings.ToLower(trimmed), ".gz") {
		return trimmed
	}
	return trimmed + ".gz"
}

func PlanBackupArtifact(dbType string, driverType string, objectMode string, legacyDBName string, backupType string, rawSpec datatypes.JSON, rawDriverConfig datatypes.JSON) (*BackupArtifactPlan, error) {
	var spec map[string]any
	if len(rawSpec) > 0 {
		if err := json.Unmarshal(rawSpec, &spec); err != nil {
			return nil, err
		}
	}
	normalized, err := normalizeObjectSpec(dbType, driverType, objectMode, legacyDBName, backupType, spec)
	if err != nil {
		return nil, err
	}
	// 任务级压缩选项：字段缺失时按默认值生效（启用压缩 + 快）
	compressOpt := resolveArtifactCompression(rawDriverConfig)
	if isPostgresPhysicalDriverType(normalized.DriverType) {
		return &BackupArtifactPlan{
			ArtifactName:      "pgbackrest-backup.json",
			StreamCompression: ArtifactCompressionNone,
		}, nil
	}
	if isMySQLPhysicalDriverType(normalized.DriverType) {
		return &BackupArtifactPlan{
			ArtifactName:      "backup.xbstream",
			StreamCompression: ArtifactCompressionNone,
		}, nil
	}
	if isPostgresLogicalFamily(dbType) {
		return &BackupArtifactPlan{
			ArtifactName:      "backup.zip",
			StreamCompression: ArtifactCompressionZip,
		}, nil
	}
	if len(normalized.Jobs) == 1 {
		fileName := normalized.Jobs[0].FileName
		// 单文件逻辑产物：是否 gzip 由任务级「产物压缩」开关决定（默认启用压缩 + 快）。
		// 恢复侧已按 .sql.gz 后缀分流（logical_restore_workflow.go），无需额外适配。
		if strings.EqualFold(strings.TrimSpace(dbType), "MySQL") && compressOpt.Enabled {
			return &BackupArtifactPlan{
				ArtifactName:      gzipArtifactName(fileName),
				StreamCompression: ArtifactCompressionGzip,
				CompressionLevel:  compressOpt.Level,
			}, nil
		}
		return &BackupArtifactPlan{
			ArtifactName:      fileName,
			StreamCompression: ArtifactCompressionNone,
		}, nil
	}
	return &BackupArtifactPlan{
		ArtifactName:      "backup.zip",
		StreamCompression: ArtifactCompressionZip,
	}, nil
}

func normalizeObjectSpec(dbType string, driverType string, objectMode string, legacyDBName string, backupType string, rawSpec map[string]any) (*normalizedObjectSpec, error) {
	drvType := strings.ToUpper(strings.TrimSpace(driverType))
	if drvType == "" {
		drvType = "LOGICAL"
	}
        if drvType == "PHYSICAL" || drvType == "PG_BACKREST" || drvType == "XTRABACKUP" {
		return normalizePhysicalObjectSpec(dbType, drvType, backupType)
	}
	mode := strings.ToUpper(strings.TrimSpace(objectMode))
	if mode == "" {
		mode = "CUSTOM"
	}
	if mode != "ALL" && mode != "CUSTOM" {
		return nil, fmt.Errorf("object_mode 非法")
	}
	bType := strings.ToUpper(strings.TrimSpace(backupType))
	if bType == "" {
		bType = "FULL"
	}
	switch bType {
	case "FULL", "SCHEMA", "DATA":
	default:
		return nil, fmt.Errorf("backup_type 非法")
	}

	if legacyDBName != "" && mode == "CUSTOM" && rawSpec == nil {
		if err := validateIdent(legacyDBName, "库名"); err != nil {
			return nil, err
		}
		switch {
		case strings.EqualFold(strings.TrimSpace(dbType), "MySQL"):
			rawSpec = map[string]any{
				"databases": []map[string]any{
					{"name": legacyDBName, "tables": []string{}},
				},
			}
		case isPostgresLogicalFamily(dbType):
			rawSpec = map[string]any{
				"databases": []map[string]any{
					{"name": legacyDBName, "schemas": []string{}, "tables": []map[string]any{}},
				},
			}
		}
	}

	// PostgreSQL 与 KingbaseES 的对象模型一致（库 → schema → 表），复用同一套解析。
	switch {
	case strings.EqualFold(strings.TrimSpace(dbType), "MySQL"):
		return normalizeMySQLObjectSpec(mode, bType, rawSpec)
	case isPostgresLogicalFamily(dbType):
		return normalizePostgresObjectSpec(mode, bType, rawSpec)
	default:
		return nil, fmt.Errorf("仅支持 MySQL/PostgreSQL/Kingbase")
	}
}

func normalizeMySQLObjectSpec(mode string, backupType string, rawSpec map[string]any) (*normalizedObjectSpec, error) {
	if mode == "ALL" {
		return &normalizedObjectSpec{
			DriverType:    "LOGICAL",
			BackupType:    backupType,
			ObjectMode:    "ALL",
			ObjectSpec:    nil,
			ObjectSummary: "全部实例",
			Jobs: []dumpJob{
				{BackupType: backupType, MySQLAll: true, FileName: "all_databases.sql"},
			},
		}, nil
	}
	if rawSpec == nil {
		return nil, fmt.Errorf("请选择备份对象")
	}
	buf, err := json.Marshal(rawSpec)
	if err != nil {
		return nil, err
	}
	var spec mysqlBackupSpec
	if err := json.Unmarshal(buf, &spec); err != nil {
		return nil, err
	}
	if len(spec.Databases) == 0 {
		return nil, fmt.Errorf("请选择备份对象")
	}
	dbMap := map[string]*mysqlBackupDatabase{}
	for _, item := range spec.Databases {
		if err := validateIdent(item.Name, "库名"); err != nil {
			return nil, err
		}
		db := item
		db.Name = strings.TrimSpace(db.Name)
		if existing, ok := dbMap[db.Name]; ok {
			existing.Tables = append(existing.Tables, db.Tables...)
			continue
		}
		dbMap[db.Name] = &db
	}
	names := make([]string, 0, len(dbMap))
	for k := range dbMap {
		names = append(names, k)
	}
	sort.Strings(names)

	jobs := make([]dumpJob, 0, len(names))
	summaryParts := make([]string, 0, len(names))
	legacyDB := ""
	for _, dbName := range names {
		db := dbMap[dbName]
		tables := uniqueStrings(db.Tables)
		excludeTables := uniqueStrings(db.ExcludeTables)
		excludePatterns := uniqueStrings(db.ExcludeTablePatterns)
		for _, t := range tables {
			if err := validateIdent(t, "表名"); err != nil {
				return nil, err
			}
		}
		for _, t := range excludeTables {
			if err := validateIdent(t, "排除表名"); err != nil {
				return nil, err
			}
		}
		for _, p := range excludePatterns {
			if err := validatePattern(p, "排除表模式"); err != nil {
				return nil, err
			}
		}
		fileName := fmt.Sprintf("%s.sql", dbName)
		job := dumpJob{
			BackupType:           backupType,
			DBName:               dbName,
			FileName:             fileName,
			MySQLDB:              true,
			MySQLTabs:            tables,
			MySQLExcludeTabs:     excludeTables,
			MySQLExcludePatterns: excludePatterns,
		}
		jobs = append(jobs, job)
		if len(tables) == 0 {
			meta := []string{}
			if len(excludeTables)+len(excludePatterns) > 0 {
				meta = append(meta, fmt.Sprintf("排除%d", len(excludeTables)+len(excludePatterns)))
			}
			if len(meta) == 0 {
				summaryParts = append(summaryParts, dbName)
			} else {
				summaryParts = append(summaryParts, fmt.Sprintf("%s(%s)", dbName, strings.Join(meta, ",")))
			}
		} else {
			meta := []string{fmt.Sprintf("%d表", len(tables))}
			if len(excludeTables)+len(excludePatterns) > 0 {
				meta = append(meta, fmt.Sprintf("排除%d", len(excludeTables)+len(excludePatterns)))
			}
			summaryParts = append(summaryParts, fmt.Sprintf("%s(%s)", dbName, strings.Join(meta, ",")))
		}
		if legacyDB == "" && len(names) == 1 {
			legacyDB = dbName
		}
	}

	summary := "自定义: " + strings.Join(summaryParts, ", ")
	return &normalizedObjectSpec{
		DriverType:    "LOGICAL",
		BackupType:    backupType,
		ObjectMode:    "CUSTOM",
		ObjectSpec:    datatypes.JSON(buf),
		ObjectSummary: summary,
		LegacyDBName:  legacyDB,
		Jobs:          jobs,
	}, nil
}

func normalizePostgresObjectSpec(mode string, backupType string, rawSpec map[string]any) (*normalizedObjectSpec, error) {
	if mode == "ALL" {
		return &normalizedObjectSpec{
			DriverType:    "LOGICAL",
			BackupType:    backupType,
			ObjectMode:    "ALL",
			ObjectSpec:    nil,
			ObjectSummary: "全部实例",
			Jobs:          nil,
		}, nil
	}
	if rawSpec == nil {
		return nil, fmt.Errorf("请选择备份对象")
	}
	buf, err := json.Marshal(rawSpec)
	if err != nil {
		return nil, err
	}
	var spec pgBackupSpec
	if err := json.Unmarshal(buf, &spec); err != nil {
		return nil, err
	}
	if len(spec.Databases) == 0 {
		return nil, fmt.Errorf("请选择备份对象")
	}

	dbMap := map[string]*pgBackupDatabase{}
	for _, item := range spec.Databases {
		if err := validateIdent(item.Name, "数据库名"); err != nil {
			return nil, err
		}
		db := item
		db.Name = strings.TrimSpace(db.Name)
		if existing, ok := dbMap[db.Name]; ok {
			existing.Schemas = append(existing.Schemas, db.Schemas...)
			existing.Tables = append(existing.Tables, db.Tables...)
			continue
		}
		dbMap[db.Name] = &db
	}

	names := make([]string, 0, len(dbMap))
	for k := range dbMap {
		names = append(names, k)
	}
	sort.Strings(names)

	jobs := make([]dumpJob, 0, len(names))
	summaryParts := make([]string, 0, len(names))
	legacyDB := ""
	for _, dbName := range names {
		db := dbMap[dbName]
		schemas := uniqueStrings(db.Schemas)
		excludePatterns := uniqueStrings(db.ExcludeTablePatterns)
		for _, s := range schemas {
			if err := validateIdent(s, "Schema"); err != nil {
				return nil, err
			}
		}
		tables := make([]string, 0, len(db.Tables))
		for _, t := range db.Tables {
			if err := validateIdent(t.Schema, "Schema"); err != nil {
				return nil, err
			}
			if err := validateIdent(t.Name, "表名"); err != nil {
				return nil, err
			}
			tables = append(tables, fmt.Sprintf("%s.%s", t.Schema, t.Name))
		}
		tables = uniqueStrings(tables)

		excludeTables := make([]string, 0, len(db.ExcludeTables))
		for _, t := range db.ExcludeTables {
			if err := validateIdent(t.Schema, "Schema"); err != nil {
				return nil, err
			}
			if err := validateIdent(t.Name, "排除表名"); err != nil {
				return nil, err
			}
			excludeTables = append(excludeTables, fmt.Sprintf("%s.%s", t.Schema, t.Name))
		}
		excludeTables = uniqueStrings(excludeTables)
		for _, p := range excludePatterns {
			if err := validatePattern(p, "排除表模式"); err != nil {
				return nil, err
			}
		}

		fileName := fmt.Sprintf("%s.dump", dbName)
		jobs = append(jobs, dumpJob{
			BackupType:        backupType,
			DBName:            dbName,
			FileName:          fileName,
			PgSchemas:         schemas,
			PgTables:          tables,
			PgExcludeTables:   excludeTables,
			PgExcludePatterns: excludePatterns,
		})

		if len(schemas) == 0 && len(tables) == 0 {
			meta := []string{}
			if len(excludeTables)+len(excludePatterns) > 0 {
				meta = append(meta, fmt.Sprintf("排除%d", len(excludeTables)+len(excludePatterns)))
			}
			if len(meta) == 0 {
				summaryParts = append(summaryParts, dbName)
			} else {
				summaryParts = append(summaryParts, fmt.Sprintf("%s(%s)", dbName, strings.Join(meta, ",")))
			}
		} else {
			meta := []string{}
			if len(schemas) > 0 {
				meta = append(meta, fmt.Sprintf("%d schema", len(schemas)))
			}
			if len(tables) > 0 {
				meta = append(meta, fmt.Sprintf("%d表", len(tables)))
			}
			if len(excludeTables)+len(excludePatterns) > 0 {
				meta = append(meta, fmt.Sprintf("排除%d", len(excludeTables)+len(excludePatterns)))
			}
			summaryParts = append(summaryParts, fmt.Sprintf("%s(%s)", dbName, strings.Join(meta, ",")))
		}

		if legacyDB == "" && len(names) == 1 {
			legacyDB = dbName
		}
	}

	summary := "自定义: " + strings.Join(summaryParts, ", ")
	return &normalizedObjectSpec{
		DriverType:    "LOGICAL",
		BackupType:    backupType,
		ObjectMode:    "CUSTOM",
		ObjectSpec:    datatypes.JSON(buf),
		ObjectSummary: summary,
		LegacyDBName:  legacyDB,
		Jobs:          jobs,
	}, nil
}

func normalizePhysicalObjectSpec(dbType string, driverType string, backupType string) (*normalizedObjectSpec, error) {
        if strings.TrimSpace(dbType) == "MySQL" {
                drvType := strings.ToUpper(strings.TrimSpace(driverType))
                if drvType == "" {
                        drvType = "XTRABACKUP"
                }
                normalizedBackupType := normalizeMySQLPhysicalBackupType(backupType)
                if normalizedBackupType == "" {
                        return nil, fmt.Errorf("MySQL 物理备份类型非法")
                }
                return &normalizedObjectSpec{
                        DriverType:    drvType,
                        BackupType:    normalizedBackupType,
                        ObjectMode:    "ALL",
                        ObjectSpec:    nil,
                        ObjectSummary: fmt.Sprintf("整实例物理备份(XtraBackup/%s)", normalizedBackupType),
                        LegacyDBName:  "",
                        Jobs:          nil,
                }, nil
        }
	if strings.TrimSpace(dbType) != "PostgreSQL" {
                return nil, fmt.Errorf("物理备份当前仅支持 MySQL/PostgreSQL")
	}
	drvType := strings.ToUpper(strings.TrimSpace(driverType))
	if drvType == "" {
		drvType = "PG_BACKREST"
	}
	normalizedBackupType := strings.ToUpper(strings.TrimSpace(backupType))
	if normalizedBackupType == "" {
		normalizedBackupType = "FULL"
	}
	switch normalizedBackupType {
	case "FULL", "DIFF", "INCR":
	default:
		return nil, fmt.Errorf("PostgreSQL 物理备份类型非法")
	}
	summary := "整实例物理备份"
	if drvType == "PG_BACKREST" {
		summary = fmt.Sprintf("整实例物理备份(pgBackRest/%s)", normalizedBackupType)
	}
	return &normalizedObjectSpec{
		DriverType:    drvType,
		BackupType:    normalizedBackupType,
		ObjectMode:    "ALL",
		ObjectSpec:    nil,
		ObjectSummary: summary,
		LegacyDBName:  "",
		Jobs:          nil,
	}, nil
}

func uniqueStrings(input []string) []string {
	if len(input) == 0 {
		return nil
	}
	m := map[string]struct{}{}
	out := make([]string, 0, len(input))
	for _, raw := range input {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if _, ok := m[v]; ok {
			continue
		}
		m[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
