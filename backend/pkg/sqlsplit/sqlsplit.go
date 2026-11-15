// Package sqlsplit 提供与具体数据库解析器无关的 SQL 语句拆分与类型识别能力。
//
// 背景：工单创建/预审链路原先统一使用 TiDB parser（MySQL 方言）解析 SQL，
// PostgreSQL 专属语法（GENERATED ALWAYS AS IDENTITY、timestamptz、COMMENT ON、
// dollar-quoted 字符串等）无法解析，导致 PG 工单在提交阶段即被拦截。
// 本包用语法感知的分号拆分状态机替代 parser 依赖，覆盖 PG 场景：
//   - 单引号字符串（含 ” 转义）
//   - 双引号标识符
//   - dollar-quoted 字符串（$tag$...$tag$，含函数体）
//   - 行注释 -- 与块注释 /* */（注释内的分号不参与拆分）
//   - 括号深度（括号内的分号不参与拆分）
//
// MySQL 执行端（SQLServer/Oracle 执行器）同样复用本实现。
package sqlsplit

import (
	"fmt"
	"strings"
	"unicode"
)

// Split 按分号拆分 SQL 文本为独立语句（保留原文，含尾部分号）。
// 空语句（仅注释/空白）不返回。
func Split(sqltext string) []string {
	s := sqltext
	var parts []string
	var depth int
	var inSingle, inDouble, inDollar, inBracket, inLineComment, inBlockComment bool
	var dollarTag string
	var cur strings.Builder
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		cur.WriteRune(c)

		if inLineComment {
			if c == '\n' {
				inLineComment = false
			}
			continue
		}
		if inBlockComment {
			if c == '*' && i+1 < len(runes) && runes[i+1] == '/' {
				cur.WriteRune('/')
				i++
				inBlockComment = false
			}
			continue
		}
		if inSingle {
			if c == '\'' {
				if i+1 < len(runes) && runes[i+1] == '\'' {
					cur.WriteRune('\'')
					i++
					continue
				}
				inSingle = false
			}
			continue
		}
		if inDouble {
			if c == '"' {
				inDouble = false
			}
			continue
		}
		if inBracket {
			if c == ']' {
				inBracket = false
			}
			continue
		}
		if inDollar {
			if c == '$' {
				rest := string(runes[i:])
				endTag := "$" + dollarTag + "$"
				if strings.HasPrefix(rest, endTag) {
					for j := 0; j < len(endTag)-1; j++ {
						if i+1+j >= len(runes) {
							break
						}
						cur.WriteRune(runes[i+1+j])
					}
					i += len(endTag) - 1
					inDollar = false
					dollarTag = ""
				}
			}
			continue
		}

		switch c {
		case '-':
			if i+1 < len(runes) && runes[i+1] == '-' {
				inLineComment = true
				continue
			}
		case '/':
			if i+1 < len(runes) && runes[i+1] == '*' {
				inBlockComment = true
				continue
			}
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '[':
			inBracket = true
		case '$':
			tagEnd := -1
			for j := i + 1; j < len(runes); j++ {
				if runes[j] == '$' {
					tagEnd = j
					break
				}
				if rl := runes[j]; !((rl >= 'a' && rl <= 'z') || (rl >= 'A' && rl <= 'Z') || (rl >= '0' && rl <= '9') || rl == '_') {
					break
				}
			}
			if tagEnd > i {
				inDollar = true
				dollarTag = string(runes[i+1 : tagEnd])
			}
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ';':
			if depth == 0 {
				stm := cur.String()
				if !isEmptyStatement(stm) {
					parts = append(parts, stm)
				}
				cur.Reset()
			}
		}
	}
	tail := cur.String()
	if !isEmptyStatement(tail) {
		parts = append(parts, tail)
	}
	return parts
}

// isEmptyStatement 判断一段文本是否为「空语句」：剥离前导注释与空白、分隔符后
// 没有任何实际内容。典型来源是 GO 批处理分隔符剥离后留下的连续分号（`;` / `;\n;`），
// 以及夹在语句间的纯注释段 —— 它们不应成为一条语句参与类型识别。
func isEmptyStatement(stm string) bool {
	s := stm
	// 迭代上限防御：正常最多 2 轮（剥注释→Trim 分号→再剥注释）即收敛
	for n := 0; n < 32; n++ {
		s = StripLeadingComments(s)
		s = strings.Trim(s, "; \t\r\n")
		if s == "" {
			return true
		}
		if !strings.HasPrefix(s, "--") && !strings.HasPrefix(s, "/*") {
			return false
		}
	}
	return false
}

// StripGoBatches 把独立的 GO 批处理行替换为分号。
//
// GO 是 SSMS/sqlcmd 等客户端的批处理分隔符，不是 T-SQL 语句，但用户提交的
// 脚本里常带。不剥离的话 GO 会单独（或与后续语句粘）成一条，导致拆分/定型失败。
// 兼容 `GO n`（重复执行次数）写法；字符串/注释内的 GO 不受影响（按行整行匹配）。
func StripGoBatches(sqlText string) string {
	lines := strings.Split(strings.ReplaceAll(sqlText, "\r\n", "\n"), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if lower == "go" || strings.HasPrefix(lower, "go ") && isAllDigits(strings.TrimSpace(lower[3:])) {
			lines[i] = ";"
		}
	}
	return strings.Join(lines, "\n")
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// StripLeadingComments 去除 SQL 文本开头连续的空白与注释（-- 与 /* */），返回剩余文本。
func StripLeadingComments(s string) string {
	for {
		s = strings.TrimLeftFunc(s, unicode.IsSpace)
		switch {
		case strings.HasPrefix(s, "--"):
			if idx := strings.IndexByte(s, '\n'); idx >= 0 {
				s = s[idx+1:]
			} else {
				s = ""
			}
		case strings.HasPrefix(s, "/*"):
			if idx := strings.Index(s, "*/"); idx >= 0 {
				s = s[idx+2:]
			} else {
				s = ""
			}
		default:
			return strings.TrimSpace(s)
		}
	}
}

// NormalizeLeading 返回去除前导注释/空白并转为小写的语句头，用于前缀判断。
//
// 空白会被折叠成单个空格：`create  table t`、`create\ttable t`、`create\ntable t`
// 与 `create table t` 等价。这一步是必须的——文本解析型引擎（PG/SQLServer）靠
// `strings.HasPrefix(head, "create table")` 分发语句，多空白写法匹配不上就会落进
// default 分支，整条语句零结论（规范里开了也永远不报）。
func NormalizeLeading(stmt string) string {
	return strings.Join(strings.Fields(strings.ToLower(StripLeadingComments(stmt))), " ")
}

// 语句类型前缀表（与工单系统 DDL/DML/EXPORT 口径一致）
var (
	ddlPrefixes = []string{
		"create ", "alter ", "drop ", "truncate ", "rename ",
		"comment ", "grant ", "revoke ",
		"do ", // PG 匿名代码块（DO $$...$$）
	}
	dmlPrefixes = []string{
		"insert ", "update ", "delete ", "replace ", "merge ",
	}
	exportPrefixes = []string{
		"select ", "with ", "show ", "explain ", "desc ", "describe ",
	}
)

// StatementType 按语句开头关键字识别类型：DDL / DML / EXPORT；无法识别返回空串。
func StatementType(stmt string) string {
	normalized := NormalizeLeading(stmt)
	if hasAnyPrefix(normalized, ddlPrefixes) {
		return "DDL"
	}
	if hasAnyPrefix(normalized, dmlPrefixes) {
		return "DML"
	}
	if hasAnyPrefix(normalized, exportPrefixes) {
		return "EXPORT"
	}
	return ""
}

// SplitTyped 拆分并识别每条语句的类型；任一语句无法识别时报错（附带序号与前缀预览）。
func SplitTyped(sqltext string) (stmts []string, types []string, err error) {
	return SplitTypedExtra(sqltext, nil)
}

// SplitTypedExtra 与 SplitTyped 相同，额外把 extraDDLPrefixes 视为 DDL 语句前缀。
//
// 用途：SQL Server 的扩展属性/改名等对象运维动作写成 `EXEC sp_addextendedproperty ...` /
// `EXEC sp_rename ...`，它们既不是标准 DDL 也不是 DML，但语义上属于结构变更。
// 通过参数注入而不是直接改 ddlPrefixes，是因为后者会改变 MySQL/PG/Oracle/TiDB
// 四条既有链路的语句识别结果（原本被拒的语句会变成放行），属对存量数据库的行为变更。
func SplitTypedExtra(sqltext string, extraDDLPrefixes []string) (stmts []string, types []string, err error) {
	stmts = Split(sqltext)
	if len(stmts) == 0 {
		return nil, nil, fmt.Errorf("SQL内容不能为空")
	}
	for i, stmt := range stmts {
		st := statementTypeWith(stmt, extraDDLPrefixes)
		if st == "" {
			head := NormalizeLeading(stmt)
			if len(head) > 40 {
				head = head[:40]
			}
			return nil, nil, fmt.Errorf("第 %d 条语句无法识别类型（%s...），请提交标准 DDL、DML 或 SELECT 语句", i+1, head)
		}
		types = append(types, st)
	}
	return stmts, types, nil
}

// TSQLExtraDDLPrefixes T-SQL 的 EXEC 系列前缀：sp_addextendedproperty / sp_rename
// 等对象运维动作既不是标准 DDL 也不是 DML，但语义上属于结构变更，按 DDL 归类。
// 与 SQLServer 工单提交链路（order_plan）和预审链路（inspect/checker）共用同口径。
var TSQLExtraDDLPrefixes = []string{"exec ", "execute "}

// statementTypeWith 在 StatementType 的基础上追加调用方指定的 DDL 前缀。
func statementTypeWith(stmt string, extraDDLPrefixes []string) string {
	normalized := NormalizeLeading(stmt)
	if len(extraDDLPrefixes) > 0 && hasAnyPrefix(normalized, extraDDLPrefixes) {
		return "DDL"
	}
	return StatementType(stmt)
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
