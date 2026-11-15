package checker

// PG 规则全量实测（真实 PostgreSQL 16 实例 + 真实规范参数）
//
// 目的：对「postgres 段 34 条 DDL 规则 + 27 条 DML 规则」逐条构造可验证用例，
// 在 postgres 库 public schema 下建好 fixture，执行审核并记录「预期 vs 实际」。
//
// 与既有单测的区别：
//   - 参数不是手写注入，而是 BuildInspectParamsForInstance 按实例绑定的规范生成，
//     映射链（规范 → InspectParams）一起被验证；
//   - 引擎带真实库连接（s.DB 非空），依赖元信息的规则（表/列/索引存在性、影响行数、
//     NOT NULL 列、主键/唯一键）走真实查询，而不是静默跳过。
//
// 运行：
//
//	LIVE_PG_MATRIX=1 \
//	LIVE_PG_HOST=192.168.1.19 LIVE_PG_PORT=5432 LIVE_PG_USER=postgres \
//	LIVE_PG_PASS=*** LIVE_PG_DB=postgres LIVE_PG_SCHEMA=public \
//	LIVE_PG_INSTANCE=ds-d4p3p3x1tten LIVE_META_DSN='root:root1234@tcp(192.168.1.19:3306)/jaysql?charset=utf8mb4&parseTime=True' \
//	go test ./internal/inspect/checker -run TestPGRuleMatrixLive -v

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/global"
	inspectServices "github.com/simajiong/jaysql/internal/inspect/services"
	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/controllers/dao"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// liveCase 单条规则用例
type liveCase struct {
	Key      string   // 引擎输出的 rule key（规范 key 去掉 db 前缀）
	Category string   // structure / query_update
	Name     string   // 规则名
	SQL      string   // 待审核 SQL
	Expect   bool     // true=期望命中；false=期望不命中（规则关闭 / 合规语句）
	ExpectNote string // 期望说明
	Note     string   // 备注（为什么这样构造）
}

// liveResult 单条用例结果
type liveResult struct {
	Key      string   `json:"key"`
	Category string   `json:"category"`
	Name     string   `json:"name"`
	SQL      string   `json:"sql"`
	Expected string   `json:"expected"`
	Actual   []string `json:"actual"`
	Pass     bool     `json:"pass"`
	Status   string   `json:"status"` // passed / failed / not_triggered
	Err      string   `json:"err,omitempty"`
	Note     string   `json:"note,omitempty"`
}

func liveEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func liveManagerDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("LIVE_META_DSN")
	if dsn == "" {
		t.Skip("未设置 LIVE_META_DSN，跳过实测")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("连接元数据库失败: %v", err)
	}
	return db
}

func livePGDB() *dao.DB {
	return &dao.DB{
		User:     liveEnv("LIVE_PG_USER", "postgres"),
		Password: os.Getenv("LIVE_PG_PASS"),
		Host:     liveEnv("LIVE_PG_HOST", "192.168.1.19"),
		Port:     atoiSafe(liveEnv("LIVE_PG_PORT", "5432")),
		Database: liveEnv("LIVE_PG_DB", "postgres"),
		Schema:   liveEnv("LIVE_PG_SCHEMA", "public"),
		DbType:   "PostgreSQL",
	}
}

// liveFixtures 建 fixture（幂等：先 drop 再建），全部落在工单 schema（默认 public）
func liveFixtures(t *testing.T, db *dao.DB) {
	t.Helper()
	stmts := []string{
		`DROP TABLE IF EXISTS t_pg_rule_multi CASCADE`,
		`DROP TABLE IF EXISTS t_pg_rule_dept CASCADE`,
		`DROP TABLE IF EXISTS t_pg_rule_user CASCADE`,
		`CREATE TABLE t_pg_rule_user (
			id bigint NOT NULL,
			name varchar(20) NOT NULL DEFAULT '',
			email varchar(50) DEFAULT '',
			status integer NOT NULL DEFAULT 0,
			uq_code varchar(20) DEFAULT '',
			create_time timestamp NOT NULL DEFAULT now(),
			update_time timestamp,
			CONSTRAINT pk_pg_rule_user PRIMARY KEY (id),
			CONSTRAINT uk_pg_rule_user_code UNIQUE (uq_code)
		)`,
		`COMMENT ON TABLE t_pg_rule_user IS '审核规则实测用表'`,
		`CREATE TABLE t_pg_rule_dept (
			id bigint NOT NULL,
			dept_name varchar(20) DEFAULT '',
			create_time timestamp NOT NULL DEFAULT now(),
			CONSTRAINT pk_pg_rule_dept PRIMARY KEY (id)
		)`,
		`CREATE TABLE t_pg_rule_multi (
			id bigint NOT NULL,
			a integer NOT NULL DEFAULT 0,
			b integer NOT NULL DEFAULT 0,
			c integer NOT NULL DEFAULT 0,
			create_time timestamp NOT NULL DEFAULT now(),
			update_time timestamp,
			CONSTRAINT pk_pg_rule_multi PRIMARY KEY (id)
		)`,
		// 三条索引：索引数量/冗余索引/索引改名冲突都靠它
		`CREATE INDEX idx_pg_multi_a ON t_pg_rule_multi (a)`,
		`CREATE INDEX idx_pg_multi_ab ON t_pg_rule_multi (a, b)`,
		`CREATE UNIQUE INDEX uk_pg_multi_b ON t_pg_rule_multi (b)`,
		// 100 行：影响行数上限 10 的用例靠它稳定触发
		`INSERT INTO t_pg_rule_user (id, name, email, status, uq_code, create_time)
		 SELECT g, 'n' || g, 'e' || g || '@x.com', 0, 'c' || g, now()
		 FROM generate_series(1, 100) AS g`,
		`INSERT INTO t_pg_rule_dept (id, dept_name) SELECT g, 'd' || g FROM generate_series(1, 100) AS g`,
		`INSERT INTO t_pg_rule_multi (id, a, b, c) SELECT g, g, g, g FROM generate_series(1, 100) AS g`,
		`ANALYZE t_pg_rule_user`,
		`ANALYZE t_pg_rule_multi`,
	}
	for _, s := range stmts {
		if err := db.Execute(s); err != nil {
			t.Fatalf("准备 fixture 失败: %v\nSQL: %s", err, s)
		}
	}
}

// runLiveCase 执行一条用例：新服务实例（避免上下文串味），只做审核、不执行 SQL
func runLiveCase(t *testing.T, params config.InspectParams, db *dao.DB, c liveCase) liveResult {
	t.Helper()
	svc := &SyntaxInspectService{
		DbType:        "PostgreSQL",
		DBSchema:      db.Database,
		InspectSchema: db.Schema,
		DB:            db,
		SqlText:       c.SQL,
		InspectParams: params,
	}
	res := liveResult{
		Key:      c.Key,
		Category: c.Category,
		Name:     c.Name,
		SQL:      strings.TrimSpace(c.SQL),
		Note:     c.Note,
	}
	datas, err := svc.runPGInspect()
	if err != nil {
		res.Err = err.Error()
		res.Expected = c.ExpectNote
		res.Pass = false
		res.Status = "failed"
		return res
	}
	keySet := map[string]bool{}
	for _, d := range datas {
		for _, s := range d.Summary {
			if s.RuleKey != "" {
				keySet[s.RuleKey] = true
				res.Actual = append(res.Actual, s.RuleKey)
			}
		}
	}
	sort.Strings(res.Actual)
	res.Actual = dedupStrings(res.Actual)
	hit := keySet[c.Key]
	res.Expected = c.ExpectNote
	if hit == c.Expect {
		res.Pass = true
		if hit {
			res.Status = "passed"
		} else {
			res.Status = "not_triggered"
		}
	} else {
		res.Pass = false
		res.Status = "failed"
	}
	return res
}

func dedupStrings(in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := in[:0]
	var last string
	for i, s := range in {
		if i == 0 || s != last {
			out = append(out, s)
		}
		last = s
	}
	return out
}

func TestPGRuleMatrixLive(t *testing.T) {
	if os.Getenv("LIVE_PG_MATRIX") != "1" {
		t.Skip("未设置 LIVE_PG_MATRIX=1，跳过真实 PG 规则实测")
	}
	db := livePGDB()
	// 规范参数：与生产同一函数（实例 → 绑定规范 → InspectParams）
	global.App.DB = liveManagerDB(t)
	params, err := inspectServices.BuildInspectParamsForInstance(liveEnv("LIVE_PG_INSTANCE", "ds-d4p3p3x1tten"))
	if err != nil {
		t.Fatalf("构建审核参数失败: %v", err)
	}
	liveFixtures(t, db)

	var results []liveResult
	for _, c := range pgLiveCases() {
		results = append(results, runLiveCase(t, params, db, c))
	}

	// 落盘，供报告脚本汇总
	if b, e := json.MarshalIndent(results, "", "  "); e == nil {
		_ = os.WriteFile("/tmp/pg_rule_matrix.json", b, 0o644)
	}

	var passed, failed, notTriggered int
	for _, r := range results {
		switch r.Status {
		case "passed":
			passed++
		case "not_triggered":
			notTriggered++
		default:
			failed++
			t.Errorf("[FAIL] %s (%s) 预期=%s 实际=%v err=%s sql=%s",
				r.Key, r.Name, r.Expected, r.Actual, r.Err, r.SQL)
		}
	}
	t.Logf("总计 %d 条：通过 %d，未触发 %d，失败 %d", len(results), passed, notTriggered, failed)
	if len(results) != 61 {
		t.Errorf("用例数应为 61（34 DDL + 27 DML），实际 %d", len(results))
	}
}
