package checker

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/simajiong/jaysql/pkg/kv"
	"github.com/simajiong/jaysql/pkg/query"
	"github.com/simajiong/jaysql/pkg/utils"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/controllers"
	"github.com/simajiong/jaysql/internal/inspect/controllers/dao"
	"github.com/simajiong/jaysql/internal/inspect/controllers/parser"
	"github.com/simajiong/jaysql/internal/inspect/controllers/process"
	"github.com/simajiong/jaysql/internal/inspect/services"

	"github.com/gin-contrib/requestid"
	"github.com/gin-gonic/gin"
	"github.com/pingcap/tidb/pkg/parser/ast"
	_ "github.com/pingcap/tidb/pkg/types/parser_driver"
)

// 返回数据
type ReturnData struct {
	Summary      []controllers.SummaryItem `json:"summary"`       // 摘要
	AffectedRows int                       `json:"affected_rows"` // 影响行数
	Type         string                    `json:"type"`          // SQL类型
	FingerId     string                    `json:"finger_id"`     // 指纹
	Query        string                    `json:"query"`         // 原始SQL
	Line         int                       `json:"line"`          // SQL 所在行号（从 1 开始）
}

// SyntaxError SQL 语法解析失败。
//
// 与「接口报错」的本质区别：它携带可定位到具体字符的结构化信息（行/列/出错片段）。
// 预审链路据此把它渲染成一条「语法错误」问题项，而不是笼统的失败提示 ——
// 历史上语法错误以裸 error 上抛，前端只能显示「规范预审快照读取失败」，
// 把用户的 SQL 错误说成了系统故障。
type SyntaxError struct {
	Message string // parser 原始错误描述
	Line    int    // 出错行号（从 1 开始，相对提交的整段 SQL）
	Column  int    // 出错列偏移（从 0 开始）
	Near    string // 出错位置附近的原文片段
}

func (e *SyntaxError) Error() string { return e.Message }

// syntaxErrPosRe 从 TiDB parser 错误文本中提取位置：line 1 column 15 near "talbe"
var syntaxErrPosRe = regexp.MustCompile(`line\s+(\d+)\s+column\s+(\d+)(?:\s+near\s+"([^"]*)")?`)

// newSyntaxError 解析 parser 原始错误，提取可定位的位置信息。
// 解析不出位置时回退到「第 1 行」，绝不因为位置缺失而丢掉错误本身。
func newSyntaxError(err error) *SyntaxError {
	se := &SyntaxError{Message: err.Error(), Line: 1, Column: 0}
	m := syntaxErrPosRe.FindStringSubmatch(se.Message)
	if m == nil {
		return se
	}
	if v, e := strconv.Atoi(m[1]); e == nil && v > 0 {
		se.Line = v
	}
	if v, e := strconv.Atoi(m[2]); e == nil && v >= 0 {
		se.Column = v
	}
	if len(m) > 3 {
		se.Near = strings.TrimSpace(m[3])
	}
	return se
}

// truncateSyntaxNear 截断 parser 回传的出错片段，避免把整段 SQL 塞进提示文案
func truncateSyntaxNear(near string) string {
	near = strings.TrimSpace(near)
	runes := []rune(near)
	if len(runes) > 40 {
		return string(runes[:40]) + "..."
	}
	return near
}

// ToReturnData 把语法错误转成一条结构化审核结果。
//
// 语法错误意味着整段 SQL 无法解析，切不出「第几条语句」，因此按原始行号定位，
// 并把出错行原文一并带回，前端据此在 SQL 文本中高亮到具体位置。
func (e *SyntaxError) ToReturnData(sqlText string) []ReturnData {
	statement := ""
	if lines := strings.Split(strings.ReplaceAll(sqlText, "\r\n", "\n"), "\n"); e.Line >= 1 && e.Line <= len(lines) {
		statement = strings.TrimSpace(lines[e.Line-1])
	}
	position := fmt.Sprintf("第 %d 行第 %d 列", e.Line, e.Column+1)
	advice := "① 检查该位置的拼写与关键字；② 检查括号、引号是否成对闭合；③ 确认每条语句以分号结尾"
	if near := truncateSyntaxNear(e.Near); near != "" {
		advice = fmt.Sprintf("① 检查 `%s` 附近的拼写与关键字；② 检查括号、引号是否成对闭合；③ 确认每条语句以分号结尾", near)
	}
	return []ReturnData{
		{
			Type:  "SYNTAX_ERROR",
			Line:  e.Line,
			Query: statement,
			Summary: []controllers.SummaryItem{
				{
					Level:    LevelWarn,
					Category: controllers.CategorySyntaxError,
					Object:   position,
					Advice:   advice,
					Message:  fmt.Sprintf("%s 附近存在语法错误：%s", position, e.Message),
				},
			},
		},
	}
}

// 语法check
type SyntaxInspectService struct {
	C             *gin.Context
	InstanceID    string
	DbUser        string
	DbPassword    string
	DbHost        string
	DbPort        int
	DbType        string // MySQL / PostgreSQL / Oracle；决定 dao 层用哪种协议连接目标实例
	DBSchema      string // 连接使用的 database：MySQL=库名；PG=目标 database；Oracle=服务名兜底
	InspectSchema string // PG/Oracle 专用：目标 schema（Oracle 为 CURRENT_SCHEMA 语义）
	ServiceName   string // Oracle 专用：extra_config.service_name，元信息探测连接用
	SqlText       string
	Username      string
	Charset       string
	Collation     string
	DB            *dao.DB
	Audit         *parser.Audit
	InspectParams config.InspectParams

	// pgMeta PG 引擎的元信息探针，仅供单测注入桩实现；生产为 nil（走真实库查询）。
	// 见 pg_object_rules.go: pgMetaResolver。
	pgMeta pgMetaResolver

	// oracleMeta Oracle 引擎的元信息探针，仅供单测注入桩实现；生产为 nil（走真实库查询）。
	// 见 oracle_object_rules.go: oracleMetaResolver。
	oracleMeta oracleMetaResolver

	// sqlserverMeta SQL Server 引擎的元信息探针，仅供单测注入桩实现；生产为 nil（走真实库查询）。
	// 见 sqlserver_object_rules.go: sqlServerMetaResolver。
	sqlserverMeta sqlServerMetaResolver
}

// 初始化DB
func (s *SyntaxInspectService) initDB() {
	s.DB = &dao.DB{
		User:     s.DbUser,
		Password: s.DbPassword,
		Host:     s.DbHost,
		Port:     s.DbPort,
		Database: s.DBSchema,
		Schema:   s.InspectSchema,
		DbType:   s.DbType,
	}
}

// 计算 SQL 语句在原始文本中的起始行号（从 1 开始）
func (s *SyntaxInspectService) computeLine(query string) int {
	if query == "" || s.SqlText == "" {
		return 1
	}
	// 优先匹配原始文本；部分解析器会复写大小写或空白，失败时做容错匹配
	queries := []string{query, strings.TrimSpace(query), strings.TrimSuffix(query, ";")}
	seen := make(map[string]struct{})
	for _, q := range queries {
		if q == "" {
			continue
		}
		if _, ok := seen[q]; ok {
			continue
		}
		seen[q] = struct{}{}
		if idx := strings.Index(s.SqlText, q); idx >= 0 {
			return strings.Count(s.SqlText[:idx], "\n") + 1
		}
	}
	return 1
}

// 初始化实例的审核参数（唯一来源：实例绑定的 SQL 开发规范）
func (s *SyntaxInspectService) initInspectParams() error {
	params, err := services.BuildInspectParamsForInstance(s.InstanceID)
	if err != nil {
		return fmt.Errorf("获取 SQL 规范参数失败: %v", err)
	}
	s.InspectParams = params
	return nil
}

func (s *SyntaxInspectService) parser() error {
	// 解析SQL
	var warns []error
	var err error
	// 解析
	s.Audit, warns, err = parser.NewParse(s.SqlText, s.Charset, s.Collation)
	if len(warns) > 0 {
		return fmt.Errorf("Parse Warning: %s", utils.ErrsJoin("; ", warns))
	}
	if err != nil {
		// 语法错误携带位置信息上抛：调用方可按 *SyntaxError 取出行/列/出错片段，
		// 用 errors.As 判定；不识别的调用方仍按普通 error 处理，行为不变。
		return newSyntaxError(err)
	}
	return nil
}

// 判断多条alter语句是否需要合并
func (s *SyntaxInspectService) mergeAlters(kv *kv.KVCache, mergeAlters []string) ReturnData {
	data := ReturnData{FingerId: utils.GenerateSimpleRandomString(16)}
	dbVersionIns := process.DbVersion{Version: kv.Get("dbVersion").(string)}
	if s.InspectParams.ENABLE_MERGE_ALTER_TABLE && !dbVersionIns.IsTiDB() {
		if ok, val := utils.IsRepeat(mergeAlters); ok {
			for _, v := range val {
				data.Summary = append(data.Summary, controllers.SummaryItem{
					Level:   LevelWarn,
					RuleKey: "merge_multiple_alter_stmt_same_table",
					Message: fmt.Sprintf("[MySQL数据库]表`%s`的多条ALTER操作，请合并为一条ALTER语句", v),
				})
			}
		}
	}
	return data
}

func (s *SyntaxInspectService) Run() (returnData []ReturnData, err error) {
	// 初始化实例绑定的 SQL 规范参数（MySQL/PG 共用的唯一判定来源）
	err = s.initInspectParams()
	if err != nil {
		return nil, err
	}
	// 初始化DB
	s.initDB()
	// PG 类数据库走 PG 专用引擎：判定参数与 MySQL 完全一致（来自实例绑定的规范），
	// 仅实现方式不同（TiDB parser 无法解析 IDENTITY / COMMENT ON 等 PG 语法）。
	// 金仓 KingbaseES 兼容 PG 方言，同样走 PG 引擎（规则目录也归一在 postgres 段）。
	if strings.EqualFold(s.DbType, "PostgreSQL") ||
		strings.EqualFold(s.DbType, "Kingbase") ||
		strings.EqualFold(s.DbType, "KingbaseES") {
		return s.runPGInspect()
	}
	// Oracle 同样走专用引擎（文本解析；TiDB parser 无法解析 NUMBER/VARCHAR2/IDENTITY 等语法）
	if strings.EqualFold(s.DbType, "Oracle") {
		return s.runOracleInspect()
	}
	// SQL Server 同样走专用引擎（文本解析；T-SQL 与 MySQL 方言完全不兼容：
	// [方括号标识符]、IDENTITY、NVARCHAR、TOP、sp_addextendedproperty 等）
	if strings.EqualFold(s.DbType, "SQLServer") || strings.EqualFold(s.DbType, "MSSQL") {
		return s.runSQLServerInspect()
	}
	// RequestID
	requestID := requestid.Get(s.C)
	// 存放alter语句中的表名
	var mergeAlters []string
	// 每次请求基于RequestID初始化kv cache
	kv := kv.NewKVCache(requestID)
	defer kv.Clear(requestID)
	// 获取目标数据库变量
	dbVars, err := dao.GetDBVars(s.DB)
	if err != nil {
		return returnData, fmt.Errorf("获取DB变量失败：%s", err.Error())
	}
	for k, v := range dbVars {
		kv.Put(k, v)
	}
	s.Charset = dbVars["dbCharset"]
	// 解析SQL
	err = s.parser()
	if err != nil {
		return returnData, err
	}
	// 迭代stmt
	st := Stmt{s}
	for _, stmt := range s.Audit.TiStmt {
		// 移除SQL尾部的分号
		sqlTrim := strings.TrimSuffix(stmt.Text(), ";")
		// 生成指纹ID
		fingerId := query.Id(query.Fingerprint(sqlTrim))
		// 存储指纹ID
		kv.Put(fingerId, true)
		line := s.computeLine(stmt.Text())
		switch stmt.(type) {
		case *ast.SelectStmt:
			// select语句不允许审核
			var data ReturnData = ReturnData{FingerId: fingerId, Query: stmt.Text(), Type: "DML", Line: line}
			data.Summary = append(data.Summary, controllers.SummaryItem{Level: LevelWarn, Message: "发现SELECT语句，请删除SELECT语句后重新审核"})
			returnData = append(returnData, data)
		case *ast.CreateTableStmt:
			data := st.CreateTableStmt(stmt, kv, fingerId)
			data.Line = line
			returnData = append(returnData, data)
		case *ast.CreateViewStmt:
			data := st.CreateViewStmt(stmt, kv, fingerId)
			data.Line = line
			returnData = append(returnData, data)
		case *ast.AlterTableStmt:
			data, mergeAlter := st.AlterTableStmt(stmt, kv, fingerId)
			data.Line = line
			mergeAlters = append(mergeAlters, mergeAlter)
			returnData = append(returnData, data)
		case *ast.DropTableStmt, *ast.TruncateTableStmt:
			data := st.DropTableStmt(stmt, kv, fingerId)
			data.Line = line
			returnData = append(returnData, data)
		case *ast.DeleteStmt, *ast.InsertStmt, *ast.UpdateStmt:
			data := st.DMLStmt(stmt, kv, fingerId)
			data.Line = line
			returnData = append(returnData, data)
		case *ast.RenameTableStmt:
			data := st.RenameTableStmt(stmt, kv, fingerId)
			data.Line = line
			returnData = append(returnData, data)
		case *ast.AnalyzeTableStmt:
			data := st.AnalyzeTableStmt(stmt, kv, fingerId)
			data.Line = line
			returnData = append(returnData, data)
		case *ast.CreateDatabaseStmt:
			data := st.CreateDatabaseStmt(stmt, kv, fingerId)
			data.Line = line
			returnData = append(returnData, data)
		case *ast.AlterDatabaseStmt:
			data := st.AlterDatabaseStmt(stmt, kv, fingerId)
			data.Line = line
			returnData = append(returnData, data)
		default:
			// 不允许的其他语句，有需求可以扩展
			var data ReturnData = ReturnData{FingerId: fingerId, Query: stmt.Text(), Type: "", Line: line}
			data.Summary = append(data.Summary, controllers.SummaryItem{Level: LevelWarn, Message: "未识别或禁止的审核语句，请联系数据库管理员"})
			returnData = append(returnData, data)
		}
	}
	// 判断多条alter语句是否需要合并
	if len(mergeAlters) > 1 {
		mergeData := s.mergeAlters(kv, mergeAlters)
		if len(mergeData.Summary) > 0 {
			returnData = append(returnData, mergeData)
		}
	}
	// 比如只传递了注释,如:#
	if len(s.Audit.TiStmt) == 0 {
		return
	}
	return
}
