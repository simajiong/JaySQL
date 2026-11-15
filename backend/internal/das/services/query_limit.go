package services

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/simajiong/jaysql/internal/das/limitpolicy"
)

var trailingBlockCommentRe = regexp.MustCompile(`(?s)\s*/\*.*\*/\s*$`)
var limitCommaRe = regexp.MustCompile(`(?i)\blimit\s+(\d+)\s*,\s*(\d+)`)
var limitSimpleRe = regexp.MustCompile(`(?i)\blimit\s+(\d+)\b`)
var sqlserverTopRe = regexp.MustCompile(`(?i)\btop\s*\(?\s*(\d+)\s*\)?`)
var sqlserverFetchNextRe = regexp.MustCompile(`(?i)\bfetch\s+next\s+(\d+)\s+rows?\s+only\b`)
var oracleFetchFirstRe = regexp.MustCompile(`(?i)\bfetch\s+first\s+(\d+)\s+rows?\s+only\b`)
var oracleRownumRe = regexp.MustCompile(`(?i)\brownum\s*(<=|<)\s*(\d+)\b`)

func splitTrailingBlockComment(sql string) (body string, tail string) {
	loc := trailingBlockCommentRe.FindStringIndex(sql)
	if loc == nil {
		return sql, ""
	}
	return strings.TrimRight(sql[:loc[0]], " \t\r\n"), sql[loc[0]:]
}

func applyQueryLimit(dbType string, sqlText string) (string, error) {
	body, tail := splitTrailingBlockComment(sqlText)
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return sqlText, nil
	}
	defaultLimit := int(limitpolicy.DefaultReturnRows)
	maxLimit := int(limitpolicy.MaxReturnRows)

	switch strings.ToLower(dbType) {
	// "kingbase" 与 postgresql 同为 LIMIT 语法，走同一条注入/封顶路径；
	// 漏掉它会落到 default 分支（原样放行），大表查询会一次性返回全部行。
	case "mysql", "tidb", "doris", "postgresql", "kingbase", "kingbasees", "clickhouse":
		if strings.EqualFold(dbType, "mysql") || strings.EqualFold(dbType, "tidb") || strings.EqualFold(dbType, "doris") || strings.EqualFold(dbType, "clickhouse") {
			return strings.TrimRight(body, " \t\r\n") + tail, nil
		}
		capped, hasLimit := capLimitClause(body, maxLimit)
		if hasLimit {
			return strings.TrimRight(capped, " \t\r\n") + tail, nil
		}
		return strings.TrimRight(body, " \t\r\n") + fmt.Sprintf(" LIMIT %d", defaultLimit) + tail, nil
	case "sqlserver":
		capped, hasRowLimit := capSQLServerRowLimit(body, maxLimit)
		if hasRowLimit {
			return strings.TrimRight(capped, " \t\r\n") + tail, nil
		}
		injected := injectSQLServerTop(body, defaultLimit)
		return strings.TrimRight(injected, " \t\r\n") + tail, nil
	case "oracle":
		capped, hasOracleLimit := capOracleRowLimit(body, maxLimit)
		if hasOracleLimit {
			return strings.TrimRight(capped, " \t\r\n") + tail, nil
		}
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "fetch first") || strings.Contains(lower, "rownum") {
			return strings.TrimRight(body, " \t\r\n") + tail, nil
		}
		wrapped := fmt.Sprintf("SELECT * FROM (%s) WHERE ROWNUM <= %d", strings.TrimRight(body, " \t\r\n"), defaultLimit)
		return wrapped + tail, nil
	default:
		return strings.TrimRight(body, " \t\r\n") + tail, nil
	}
}

func capLimitClause(sqlText string, limit int) (string, bool) {
	body := sqlText
	if matches := limitCommaRe.FindStringSubmatchIndex(body); matches != nil {
		countText := body[matches[4]:matches[5]]
		count, _ := strconv.Atoi(countText)
		if count > limit {
			return body[:matches[4]] + strconv.Itoa(limit) + body[matches[5]:], true
		}
		return body, true
	}
	if matches := limitSimpleRe.FindStringSubmatchIndex(body); matches != nil {
		countText := body[matches[2]:matches[3]]
		count, _ := strconv.Atoi(countText)
		if count > limit {
			return body[:matches[2]] + strconv.Itoa(limit) + body[matches[3]:], true
		}
		return body, true
	}
	return body, false
}

func capSQLServerRowLimit(sqlText string, limit int) (string, bool) {
	body := sqlText
	if matches := sqlserverTopRe.FindStringSubmatchIndex(body); matches != nil {
		numText := body[matches[2]:matches[3]]
		num, _ := strconv.Atoi(numText)
		if num > limit {
			return body[:matches[2]] + strconv.Itoa(limit) + body[matches[3]:], true
		}
		return body, true
	}
	if matches := sqlserverFetchNextRe.FindStringSubmatchIndex(body); matches != nil {
		numText := body[matches[2]:matches[3]]
		num, _ := strconv.Atoi(numText)
		if num > limit {
			return body[:matches[2]] + strconv.Itoa(limit) + body[matches[3]:], true
		}
		return body, true
	}
	return body, false
}

func capOracleRowLimit(sqlText string, limit int) (string, bool) {
	body := sqlText
	if matches := oracleFetchFirstRe.FindStringSubmatchIndex(body); matches != nil {
		numText := body[matches[2]:matches[3]]
		num, _ := strconv.Atoi(numText)
		if num > limit {
			return body[:matches[2]] + strconv.Itoa(limit) + body[matches[3]:], true
		}
		return body, true
	}
	if matches := oracleRownumRe.FindStringSubmatchIndex(body); matches != nil {
		numText := body[matches[4]:matches[5]]
		num, _ := strconv.Atoi(numText)
		if matches[2] >= 0 && matches[3] >= 0 && body[matches[2]:matches[3]] == "<" {
			num--
		}
		if num > limit {
			return body[:matches[4]] + strconv.Itoa(limit) + body[matches[5]:], true
		}
		return body, true
	}
	return body, false
}

func injectSQLServerTop(sqlText string, limit int) string {
	body := strings.TrimSpace(sqlText)
	lower := strings.ToLower(body)
	if strings.Contains(lower, " top ") || strings.HasPrefix(lower, "top ") || strings.Contains(lower, "fetch next") {
		return body
	}

	pos := findOuterSelectPos(body)
	if pos < 0 {
		return fmt.Sprintf("SELECT TOP %d * FROM (%s) AS t", limit, body)
	}

	after := pos + len("select")
	for after < len(body) {
		ch := body[after]
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			after++
			continue
		}
		break
	}

	next := strings.ToLower(body[after:])
	if strings.HasPrefix(next, "distinct") {
		after = after + len("distinct")
	}

	prefix := strings.TrimRight(body[:after], " \t\r\n")
	rest := strings.TrimLeft(body[after:], " \t\r\n")
	return prefix + fmt.Sprintf(" TOP %d ", limit) + rest
}

func findOuterSelectPos(sqlText string) int {
	s := sqlText
	depth := 0
	inSingle := false
	inDouble := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inSingle {
			if ch == '\'' {
				inSingle = false
			}
			continue
		}
		if inDouble {
			if ch == '"' {
				inDouble = false
			}
			continue
		}
		if ch == '\'' {
			inSingle = true
			continue
		}
		if ch == '"' {
			inDouble = true
			continue
		}
		if ch == '(' {
			depth++
			continue
		}
		if ch == ')' {
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth != 0 {
			continue
		}
		if (ch == 's' || ch == 'S') && i+6 <= len(s) {
			if strings.EqualFold(s[i:i+6], "select") {
				if (i == 0 || !isIdentChar(s[i-1])) && (i+6 == len(s) || !isIdentChar(s[i+6])) {
					return i
				}
			}
		}
	}
	return -1
}

func isIdentChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '$'
}
