package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/datasec/forms"
	datasecModels "github.com/simajiong/jaysql/internal/datasec/models"
	dasDao "github.com/simajiong/jaysql/internal/das/dao"
	dasServices "github.com/simajiong/jaysql/internal/das/services"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/secret"
)

// 敏感列自动发现（按需触发，非后台全库扫描）：
//   1. information_schema 读列元数据（零数据访问）；
//   2. 对候选列抽样 SELECT ... LIMIT 500（只读一次，单表耗时可预期）；
//   3. 判定器给结论，写 insight_datasec_columns（status=suggested）待人工确认。
//
// 性能红线：单次只扫指定表、LIMIT 500、元数据能定论的不采样。
// 不支持自动发现的引擎（Oracle/SQLServer/ClickHouse）返回明确错误，而不是静默跳过。

const scanSampleLimit = 500

// 合法标识符：字母/下划线开头，字母数字下划线组成。防止拼 SQL 时被注入。
var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

func validIdentifier(name string) bool {
	return identifierPattern.MatchString(strings.TrimSpace(name))
}

type ScanTableService struct {
	*forms.ScanTableForm
	C *gin.Context
}

// Run 扫描指定实例指定库下的表（多个表名以英文逗号分隔）。
// SchemaName 口径与 SQL 查询一致：MySQL 系传库名；PG 系传 "库.schema" 或仅库名（默认 public）。
func (s *ScanTableService) Run() (interface{}, error) {
	cfg, err := dasServices.GetDBConfig(strings.TrimSpace(s.InstanceID))
	if err != nil {
		return nil, err
	}
	dbType := string(cfg.DbType)
	tables := splitTableNames(s.TableNames)
	if len(tables) == 0 {
		return nil, fmt.Errorf("请指定要扫描的表名（多个表用英文逗号分隔，仅支持字母数字下划线）")
	}
	switch {
	case dasServices.IsMySQLFamily(dbType):
		return s.scanMySQL(cfg, tables)
	case dasServices.IsPostgresFamily(dbType):
		return s.scanPostgres(cfg, tables)
	default:
		return nil, fmt.Errorf("自动发现当前支持 MySQL 系 / PG 系数据库，%s 暂不支持（可手工配置列）", dbType)
	}
}

func splitTableNames(raw string) []string {
	out := make([]string, 0, 8)
	for _, t := range strings.Split(raw, ",") {
		t = strings.TrimSpace(t)
		if t != "" && validIdentifier(t) {
			out = append(out, t)
		}
	}
	return out
}

type scanColumnMeta struct {
	Name    string
	Comment string
	IsText  bool // 数值/时间类列不做内容判定，不参与采样
}

// ---------- MySQL 系 ----------

func (s *ScanTableService) scanMySQL(cfg models.InsightInstances, tables []string) (interface{}, error) {
	plainPassword, err := secret.Decrypt(cfg.Password)
	if err != nil {
		return nil, err
	}
	schema := strings.TrimSpace(s.SchemaName)
	if schema == "" {
		return nil, fmt.Errorf("请指定要扫描的库名")
	}
	if !validIdentifier(schema) {
		return nil, fmt.Errorf("库名 %s 含非法字符", schema)
	}
	ctx, cancel := context.WithTimeout(s.C.Request.Context(), 120*time.Second)
	defer cancel()
	db := dasDao.DB{User: cfg.User, Password: plainPassword, Host: cfg.Hostname, Port: cfg.Port, Ctx: ctx}
	sqlDB, err := db.Open()
	if err != nil {
		return nil, fmt.Errorf("连接实例失败: %w", err)
	}
	defer sqlDB.Close()

	results := make([]scanTableResult, 0, len(tables))
	for _, table := range tables {
		metas, err := mysqlColumns(sqlDB, schema, table)
		if err != nil {
			return nil, fmt.Errorf("读取 %s.%s 列元数据失败: %w", schema, table, err)
		}
		results = append(results, s.scanOneTable(strings.TrimSpace(s.InstanceID), schema, table, metas,
			func(cols []string) (*[]string, *[]map[string]interface{}, error) {
				// 表名/列名已过 identifierPattern 白名单，反引号包裹后拼接是安全的
				return db.Query(fmt.Sprintf("SELECT %s FROM `%s`.`%s` LIMIT %d",
					quoteBacktickCols(cols), schema, table, scanSampleLimit))
			}))
	}
	return results, nil
}

func mysqlColumns(sqlDB *sql.DB, schema, table string) ([]scanColumnMeta, error) {
	rows, err := sqlDB.Query(
		"SELECT COLUMN_NAME, IFNULL(COLUMN_COMMENT,''), DATA_TYPE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=? AND TABLE_NAME=? ORDER BY ORDINAL_POSITION",
		schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanColumnMetas(rows)
}

func mysqlTypeIsText(dataType string) bool {
	switch dataType {
	case "char", "varchar", "text", "tinytext", "mediumtext", "longtext", "enum", "set":
		return true
	}
	return false
}

func quoteBacktickCols(cols []string) string {
	quoted := make([]string, 0, len(cols))
	for _, c := range cols {
		quoted = append(quoted, "`"+c+"`")
	}
	return strings.Join(quoted, ", ")
}

// ---------- PG 系（含 KingbaseES，协议与系统视图同源） ----------

func (s *ScanTableService) scanPostgres(cfg models.InsightInstances, tables []string) (interface{}, error) {
	plainPassword, err := secret.Decrypt(cfg.Password)
	if err != nil {
		return nil, err
	}
	// Schema 口径与 SQL 查询一致："库.schema" 或仅 "库"（schema 取 public）
	schemaPath := strings.TrimSpace(s.SchemaName)
	if schemaPath == "" {
		return nil, fmt.Errorf("请指定要扫描的库名（PG 系格式：库.schema 或仅库名）")
	}
	databaseName, schemaName := schemaPath, "public"
	if parts := strings.SplitN(schemaPath, ".", 2); len(parts) == 2 {
		databaseName, schemaName = parts[0], parts[1]
	}
	if !validIdentifier(databaseName) || !validIdentifier(schemaName) {
		return nil, fmt.Errorf("库/schema 名含非法字符")
	}
	extra := postgresExtra{}
	if len(cfg.ExtraConfig) > 0 {
		_ = json.Unmarshal(cfg.ExtraConfig, &extra)
	}
	db := dasDao.PostgresDB{
		User: cfg.User, Password: plainPassword, Host: cfg.Hostname, Port: cfg.Port,
		Database: databaseName, SSLMode: extra.SSLMode,
		Ctx: s.C.Request.Context(),
	}

	results := make([]scanTableResult, 0, len(tables))
	for _, table := range tables {
		metas, err := postgresColumns(db, schemaName, table)
		if err != nil {
			return nil, fmt.Errorf("读取 %s.%s 列元数据失败: %w", schemaName, table, err)
		}
		results = append(results, s.scanOneTable(strings.TrimSpace(s.InstanceID), schemaName, table, metas,
			func(cols []string) (*[]string, *[]map[string]interface{}, error) {
				// schema/table/列名均已过白名单校验，双引号包裹后拼接安全；LIMIT 为常量
				return db.Query(fmt.Sprintf("SELECT %s FROM %q.%q LIMIT %d",
					quoteDoubleCols(cols), schemaName, table, scanSampleLimit))
			}))
	}
	return results, nil
}

type postgresExtra struct {
	SSLMode string `json:"sslmode"`
}

func postgresColumns(db dasDao.PostgresDB, schema, table string) ([]scanColumnMeta, error) {
	// 注释通过 pg_description 关联（information_schema.columns 不含注释）；
	// schema/table 已过白名单校验，可安全内联。
	query := fmt.Sprintf(`SELECT c.column_name, COALESCE(d.description, ''), c.data_type
		FROM information_schema.columns c
		LEFT JOIN pg_catalog.pg_description d
		  ON d.objoid = ('%q.%q')::regclass AND d.objsubid = c.ordinal_position
		WHERE c.table_schema = '%s' AND c.table_name = '%s'
		ORDER BY c.ordinal_position`, schema, table, schema, table)
	columns, data, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	metas := make([]scanColumnMeta, 0, 16)
	if columns == nil || data == nil {
		return metas, nil
	}
	for _, row := range *data {
		m := scanColumnMeta{
			Name:    toStringValue(row["column_name"]),
			Comment: toStringValue(row["coalesce"]),
			IsText:  false,
		}
		dataType := strings.ToLower(toStringValue(row["data_type"]))
		m.IsText = strings.Contains(dataType, "char") || strings.Contains(dataType, "text")
		metas = append(metas, m)
	}
	return metas, nil
}

func quoteDoubleCols(cols []string) string {
	quoted := make([]string, 0, len(cols))
	for _, c := range cols {
		quoted = append(quoted, "\""+c+"\"")
	}
	return strings.Join(quoted, ", ")
}

// ---------- 公共扫描逻辑 ----------

type scanSuggestion struct {
	ColumnName string `json:"column_name"`
	DataType   string `json:"data_type"`
	Confidence int    `json:"confidence"`
	SampleHit  string `json:"sample_hit"`
	RuleID     uint64 `json:"rule_id"`
	RuleName   string `json:"rule_name"`
}

type scanTableResult struct {
	Schema      string           `json:"schema"`
	Table       string           `json:"table"`
	Suggestions []scanSuggestion `json:"suggestions"`
	Sampled     int              `json:"sampled"` // 本次实际采样的候选列数
	Elapsed     string           `json:"elapsed"`
}

// scanOneTable 单表扫描：文本列一次取样 → 判定 → 落建议。
// sampleFetcher 由各引擎注入（SQL 方言差异收敛在这一层）。
func (s *ScanTableService) scanOneTable(instanceID, schema, table string, metas []scanColumnMeta,
	sampleFetcher func(cols []string) (*[]string, *[]map[string]interface{}, error)) scanTableResult {
	start := time.Now()
	result := scanTableResult{Schema: schema, Table: table, Suggestions: []scanSuggestion{}}
	if len(metas) == 0 {
		result.Elapsed = fmt.Sprintf("%dms", time.Since(start).Milliseconds())
		return result
	}
	candidateCols := make([]string, 0, len(metas))
	commentByName := make(map[string]string, len(metas))
	for _, m := range metas {
		commentByName[m.Name] = m.Comment
		if m.IsText && validIdentifier(m.Name) {
			candidateCols = append(candidateCols, m.Name)
		}
	}
	if len(candidateCols) == 0 {
		result.Elapsed = fmt.Sprintf("%dms", time.Since(start).Milliseconds())
		return result
	}
	columns, data, err := sampleFetcher(candidateCols)
	if err != nil {
		// 抽样失败不中断整表结论：记日志，按零样本走纯元数据判定
		global.App.Log.Error(fmt.Sprintf("datasec: 抽样 %s.%s 失败: %v", schema, table, err))
		result.Elapsed = fmt.Sprintf("%dms", time.Since(start).Milliseconds())
		return result
	}
	result.Sampled = len(candidateCols)
	// 列名 -> 样本集合（DAO 对重名列会加后缀，按候选列前缀归一）
	samplesByName := make(map[string][]string, len(candidateCols))
	if columns != nil && data != nil {
		for _, row := range *data {
			for col, val := range row {
				base := baseColumnName(col, candidateCols)
				samplesByName[base] = append(samplesByName[base], toStringValue(val))
			}
		}
	}
	for _, col := range candidateCols {
		verdict := detectSensitiveType(col, commentByName[col], samplesByName[col])
		if verdict == nil {
			continue
		}
		rule, err := findRuleByDataType(verdict.DataType)
		if err != nil {
			continue // 单列缺规则不影响整表
		}
		if err := upsertSuggestion(instanceID, schema, table, col, verdict, rule); err != nil {
			global.App.Log.Error(fmt.Sprintf("datasec: 写入建议失败 %s.%s.%s: %v", schema, table, col, err))
			continue
		}
		result.Suggestions = append(result.Suggestions, scanSuggestion{
			ColumnName: col, DataType: verdict.DataType, Confidence: verdict.Confidence,
			SampleHit: verdict.SampleHit, RuleID: rule.ID, RuleName: rule.Name,
		})
	}
	result.Elapsed = fmt.Sprintf("%dms", time.Since(start).Milliseconds())
	return result
}

// baseColumnName 把 DAO 生成的「col_<uuid>[别名]」式重列名归一回原始列名。
func baseColumnName(resultCol string, candidates []string) string {
	lower := strings.ToLower(resultCol)
	best := resultCol
	bestLen := -1
	for _, c := range candidates {
		if strings.HasPrefix(lower, strings.ToLower(c)) && len(c) > bestLen {
			best = c
			bestLen = len(c)
		}
	}
	return best
}

// findRuleByDataType 取该类型的内置规则（EnsureBuiltinRules 保证存在）；
// 内置规则被停用时回退到该类型下任意启用规则。
func findRuleByDataType(dataType string) (*datasecModels.InsightDataSecRules, error) {
	var rule datasecModels.InsightDataSecRules
	err := global.App.DB.Where("data_type=? and enabled=1 and built_in=1", dataType).First(&rule).Error
	if err == nil {
		return &rule, nil
	}
	err = global.App.DB.Where("data_type=? and enabled=1", dataType).Order("id ASC").First(&rule).Error
	if err != nil {
		return nil, fmt.Errorf("类型 %s 没有可用的启用规则，请先在「脱敏规则」中启用", dataType)
	}
	return &rule, nil
}

// upsertSuggestion 写入/刷新扫描建议：已生效配置不覆盖（用户拍板过的机器不改），
// 建议态记录原地刷新置信度与依据。
func upsertSuggestion(instanceID, schema, table, column string, verdict *scanVerdict, rule *datasecModels.InsightDataSecRules) error {
	var existing datasecModels.InsightDataSecColumns
	err := global.App.DB.Where("instance_id=? and schema_name=? and table_name=? and column_name=?",
		instanceID, schema, table, column).First(&existing).Error
	if err == nil {
		if existing.Status == columnStatusEnabled {
			return nil
		}
		existing.RuleID = rule.ID
		existing.Confidence = verdict.Confidence
		existing.SampleHit = verdict.SampleHit
		existing.Source = "scan"
		return global.App.DB.Save(&existing).Error
	}
	suggestion := datasecModels.InsightDataSecColumns{
		InstanceID: instanceID, SchemaName: schema, TblName: table, ColumnName: column,
		RuleID: rule.ID, Status: columnStatusSuggested, Source: "scan",
		Confidence: verdict.Confidence, SampleHit: verdict.SampleHit,
	}
	return global.App.DB.Create(&suggestion).Error
}

func scanColumnMetas(rows *sql.Rows) ([]scanColumnMeta, error) {
	defer rows.Close()
	metas := make([]scanColumnMeta, 0, 16)
	for rows.Next() {
		var m scanColumnMeta
		var dataType string
		if err := rows.Scan(&m.Name, &m.Comment, &dataType); err != nil {
			return nil, err
		}
		m.IsText = mysqlTypeIsText(dataType)
		metas = append(metas, m)
	}
	return metas, rows.Err()
}
