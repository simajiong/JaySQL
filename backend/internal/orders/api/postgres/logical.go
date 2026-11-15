// Package postgres 提供 PostgreSQL（含 Kingbase，协议兼容）工单的 DML 执行与
// 精确回滚 SQL 生成能力。
//
// 回滚方案采用「逻辑解码」（对应 MySQL 侧的 Binlog 行事件解析）：
// 在工单 DML 执行前创建临时逻辑复制槽（test_decoding 插件），执行后用
// pg_logical_slot_peek_changes 解码槽内事件、按事务 xid 过滤出本工单的行变更，
// 生成逆向 SQL，最后立即删除复制槽。全程对实例的日常使用无感知：
//   - 主库只多一段被临时槽钉住的 WAL，工单结束后立即释放；
//   - 解码动作发生在工单执行链内（同步、短窗口），不常驻进程；
//   - wal_level 不是 logical 时仅记录轻量提醒并跳过，绝不阻塞工单执行。
//
// 前置条件与降级行为（均不阻塞执行）：
//   - wal_level != logical      → 跳过回滚生成，执行日志提示如何开启；
//   - 无建槽权限 / 插件缺失    → 跳过回滚生成，执行日志提示原因；
//   - 影响行数超过配置阈值      → 跳过回滚生成（阈值见 config: order.pg_rollback_max_rows）；
//   - 表的 REPLICA IDENTITY 非 FULL → 该表的 UPDATE/DELETE 无法取到旧值，
//     对应变更跳过生成并在日志中提示（INSERT 不受影响）。
package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// logicalSlot 是一个临时逻辑复制槽的生命周期句柄。
// 创建即解码起点（槽的 consistent point），drop 必须由调用方保证执行，
// 否则槽会一直钉住 WAL，最终撑爆实例磁盘。
type logicalSlot struct {
	name string
	conn *pgx.Conn
}

// createLogicalSlot 在目标库上创建临时逻辑槽。
// 槽名规则：jaysql_rb_ 前缀 + taskID 净化（复制槽名上限 63 字节，仅允许小写、数字、下划线）。
// 失败返回原始错误，由调用方决定降级文案——常见原因是执行账号缺少复制槽权限
// （需要 pg_create_replication_slot 预定义角色 / REPLICATION 属性）或 test_decoding
// 插件未安装到实例的 lib 目录。
func createLogicalSlot(ctx context.Context, conn *pgx.Conn, taskID string) (*logicalSlot, error) {
	name := slotNameForTask(taskID)
	// pg_create_logical_replication_slot 第二个参数是插件名；test_decoding 是
	// PostgreSQL contrib 自带的输出插件，覆盖面最广（pgoutput 的 SQL 侧解码输出
	// 是二进制流，无法用本函数消费；wal2json 是第三方插件，装机率低于 test_decoding）。
	var slot, lsn string
	err := conn.QueryRow(ctx,
		"SELECT lsn::text, slot_name FROM pg_create_logical_replication_slot($1, 'test_decoding')", name,
	).Scan(&lsn, &slot)
	if err != nil {
		return nil, err
	}
	return &logicalSlot{name: slot, conn: conn}, nil
}

// drop 删除复制槽。幂等：槽不存在时视为成功（已被清理过）。
// 删除失败只返回错误，由调用方记日志——绝不能因为清理失败掩盖执行结果。
func (s *logicalSlot) drop(ctx context.Context) error {
	_, err := s.conn.Exec(ctx, "SELECT pg_drop_replication_slot($1)", s.name)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "does not exist") {
		return nil
	}
	return err
}

func slotNameForTask(taskID string) string {
	var b strings.Builder
	b.WriteString("jaysql_rb_")
	for _, r := range strings.ToLower(strings.TrimSpace(taskID)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			// 非法字符（含 '-'、空格等）统一折叠为 '_'，保持任务号可辨识
			b.WriteByte('_')
		}
		// 复制槽名上限 63 字节；前缀已占 10 字节，留 50 给任务号足够（tk-xxxxxxxxxxxx）
		if b.Len() >= 56 {
			break
		}
	}
	return b.String()
}

// queryWALLevel 读取实例 wal_level。普通账号即可查询，无需降级路径。
func queryWALLevel(ctx context.Context, conn *pgx.Conn) (string, error) {
	var level string
	if err := conn.QueryRow(ctx, "SHOW wal_level").Scan(&level); err != nil {
		return "", err
	}
	return strings.TrimSpace(level), nil
}

// queryCurrentWALLSN 返回当前 WAL 写入位点（如 "0/3A1F8C60"）。
func queryCurrentWALLSN(ctx context.Context, conn *pgx.Conn) (string, error) {
	var lsn string
	if err := conn.QueryRow(ctx, "SELECT pg_current_wal_lsn()::text").Scan(&lsn); err != nil {
		return "", err
	}
	return strings.TrimSpace(lsn), nil
}

// peekMaxChanges 是 pg_logical_slot_peek_changes 的 upto_nchanges 兜底上限。
// 工单自身的行数已受阈值约束，这个上限只为防住「槽创建到执行结束的窗口内
// 其他会话写入」被无限制解码导致内存膨胀。
const peekMaxChanges = 100000

// peekDecodedChanges 从槽中解码 [槽创建点, uptoLSN] 区间内的行变更，
// 只保留 xid 命中本工单事务集合的变更，按 WAL 顺序返回。
//
// 匹配口径：peek 结果集的 xid 列是 32 位 xid，工单侧用 txid_current() 的
// 低 32 位比对（工单执行是秒级窗口，不存在 xid 回绕歧义）。
func peekDecodedChanges(ctx context.Context, conn *pgx.Conn, slot string, uptoLSN string, xids []uint32) ([]decodedChange, error) {
	rows, err := conn.Query(ctx,
		"SELECT lsn::text, xid::text, data FROM pg_logical_slot_peek_changes($1, $2::pg_lsn, $3)",
		slot, uptoLSN, peekMaxChanges)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	xidSet := make(map[uint32]struct{}, len(xids))
	for _, x := range xids {
		xidSet[x] = struct{}{}
	}

	var changes []decodedChange
	for rows.Next() {
		var lsn, xidText, data string
		if err := rows.Scan(&lsn, &xidText, &data); err != nil {
			return nil, err
		}
		var xid uint32
		if _, err := fmt.Sscanf(xidText, "%d", &xid); err != nil {
			return nil, fmt.Errorf("解析解码事件 xid(%s) 失败: %w", xidText, err)
		}
		if _, ok := xidSet[xid]; !ok {
			continue // 其他会话的并发写入，不属于本工单
		}
		change, ok := parseTestDecodingLine(data)
		if !ok {
			continue // BEGIN/COMMIT/message 等非行事件
		}
		change.LSN = lsn
		change.XID = xid
		changes = append(changes, *change)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return changes, nil
}

// ---------------------------------------------------------------------------
// test_decoding 输出解析
//
// 输出行格式（依据 PostgreSQL 源码 contrib/test_decoding 的回归期望输出，
// 见 contrib/test_decoding/expected/*.out；该格式非官方稳定承诺，PG 10~17 未变过）：
//
//	BEGIN 687
//	table public.t_order: INSERT: id[integer]:1 remark[text]:'hello'
//	table public.t_order: UPDATE: old-key: id[integer]:2 : new-tuple: id[integer]:2 remark[text]:'x'
//	table public.t_order: DELETE: id[integer]:2
//	table public.t: DELETE: (no-tuple-data)
//	COMMIT 687 (at 2026-11-12 10:00:00.123+08)
//
// 列值规则：字符串类带单引号（内部 '' 转义，为合法 PG 字面量，可原样回填）；
// 数值类裸值；NULL 输出 null；bool 输出 true/false。
// old-key 的完整性由表的 REPLICA IDENTITY 决定：FULL 时为整行，DEFAULT 时仅主键。
// ---------------------------------------------------------------------------

type decodedColumn struct {
	Name   string
	Type   string
	Value  string // 原样 token（含引号形式）；"null" 表示 NULL（IsNull=true）
	IsNull bool
}

type decodedChange struct {
	LSN    string
	XID    uint32
	Op     string // INSERT / UPDATE / DELETE
	Schema string
	Table  string
	// Columns 是新元组（INSERT/UPDATE 的 new-tuple，DELETE 无）。
	Columns []decodedColumn
	// OldKey 是旧元组（UPDATE 的 old-key / DELETE 的旧行）。
	OldKey []decodedColumn
}

// parseTestDecodingLine 解析单条 data 行。返回 ok=false 表示非行事件（BEGIN/COMMIT 等）。
func parseTestDecodingLine(line string) (*decodedChange, bool) {
	trimmed := strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(trimmed, "table ") {
		return nil, false // BEGIN / COMMIT / message / streaming 标记等
	}
	rest := strings.TrimPrefix(trimmed, "table ")
	// "schema.table: OP: payload" —— schema.table 段内不会出现冒号（test_decoding 原样拼接限定名）
	sep := strings.Index(rest, ": ")
	if sep < 0 {
		return nil, false
	}
	qualified := rest[:sep]
	dot := strings.LastIndex(qualified, ".")
	if dot < 0 {
		return nil, false
	}
	change := &decodedChange{Schema: qualified[:dot], Table: qualified[dot+1:]}

	payload := rest[sep+2:]
	switch {
	case strings.HasPrefix(payload, "INSERT: "):
		change.Op = "INSERT"
		change.Columns = parseColumnTokens(payload[len("INSERT: "):])
	case strings.HasPrefix(payload, "UPDATE: "):
		change.Op = "UPDATE"
		body := payload[len("UPDATE: "):]
		// 分割 old-key / new-tuple 两段。源码里的分隔是 " : new-tuple:"，
		// 宽容兼容无前导冒号的形态（不同小版本空格差异）。
		newIdx := indexOfWord(body, "new-tuple:")
		if newIdx < 0 {
			// 异常形态：按纯 new-tuple 处理，生成阶段会因缺旧值跳过并提示
			change.Columns = parseColumnTokens(body)
			return change, true
		}
		oldPart := body[:newIdx]
		newPart := body[newIdx+len("new-tuple:"):]
		oldPart = strings.TrimSpace(oldPart)
		oldPart = strings.TrimPrefix(oldPart, "old-key:")
		oldPart = strings.TrimSpace(oldPart)
		oldPart = strings.TrimSuffix(oldPart, ":")
		oldPart = strings.TrimSpace(oldPart)
		change.OldKey = parseColumnTokens(oldPart)
		change.Columns = parseColumnTokens(strings.TrimSpace(newPart))
	case strings.HasPrefix(payload, "DELETE: "):
		change.Op = "DELETE"
		body := strings.TrimPrefix(payload, "DELETE: ")
		body = strings.TrimSpace(body)
		if body == "(no-tuple-data)" {
			return change, true // 无主键且非 FULL：旧行完全不可知
		}
		body = strings.TrimPrefix(body, "old-key:")
		body = strings.TrimSpace(body)
		change.OldKey = parseColumnTokens(body)
	default:
		return nil, false
	}
	return change, true
}

// indexOfWord 查找以词边界出现的标记（前面是空格或行首）。
func indexOfWord(s, marker string) int {
	for i := 0; i+len(marker) <= len(s); i++ {
		if s[i:i+len(marker)] == marker {
			if i == 0 || s[i-1] == ' ' || s[i-1] == ':' {
				return i
			}
		}
	}
	return -1
}

// parseColumnTokens 解析 "name[type]:value name2[type2]:value2 ..." 形态的列区。
// 带引号的值内部可以包含空格、冒号、方括号，必须由引号状态机保护；
// 裸值（数值/布尔）以空格结束。
func parseColumnTokens(s string) []decodedColumn {
	var cols []decodedColumn
	i := 0
	n := len(s)
	skipSpace := func() {
		for i < n && s[i] == ' ' {
			i++
		}
	}
	for {
		skipSpace()
		if i >= n {
			return cols
		}
		// 列名 → '['
		nameStart := i
		for i < n && s[i] != '[' {
			i++
		}
		if i >= n {
			return cols // 尾部残片（异常输入），丢弃而不是报错——生成阶段会校验完整性
		}
		name := s[nameStart:i]
		i++ // consume '['
		typeStart := i
		for i < n && s[i] != ']' {
			i++
		}
		if i >= n {
			return cols
		}
		colType := s[typeStart:i]
		i++ // consume ']'
		if i >= n || s[i] != ':' {
			return cols
		}
		i++ // consume ':'
		var value string
		isNull := false
		if i < n && s[i] == '\'' {
			// 引号值：内部 '' 是转义，扫描到未双写的收尾引号
			j := i + 1
			var b strings.Builder
			for j < n {
				if s[j] == '\'' {
					if j+1 < n && s[j+1] == '\'' {
						b.WriteByte('\'')
						b.WriteByte('\'')
						j += 2
						continue
					}
					break
				}
				b.WriteByte(s[j])
				j++
			}
			value = "'" + b.String() + "'"
			i = j + 1
		} else {
			j := i
			for j < n && s[j] != ' ' {
				j++
			}
			value = s[i:j]
			i = j
			if value == "null" {
				isNull = true
			}
		}
		cols = append(cols, decodedColumn{Name: name, Type: colType, Value: value, IsNull: isNull})
	}
}

// ---------------------------------------------------------------------------
// 逆向 SQL 生成
// ---------------------------------------------------------------------------

// tableMetaFunc 查询表的主键列与 replica identity。
// 抽象成函数类型是为了让逆向生成逻辑可以脱离真实数据库做单测。
// relreplident: 'd'=DEFAULT(仅主键) / 'f'=FULL(整行) / 'i'=INDEX / 'n'=NOTHING。
type tableMetaFunc func(ctx context.Context, schema, table string) (tableMeta, error)

type tableMeta struct {
	RelReplIdent string
	PKColumns    []string
}

// tableMetaProvider 是 tableMetaFunc 的连库实现（带缓存，同一表只查一次）。
type tableMetaProvider struct {
	conn  *pgx.Conn
	cache map[string]tableMeta
}

func (p *tableMetaProvider) metaFor(ctx context.Context, schema, table string) (tableMeta, error) {
	key := schema + "." + table
	if m, ok := p.cache[key]; ok {
		return m, nil
	}
	var relOID string
	var ident string
	err := p.conn.QueryRow(ctx, `
SELECT c.oid::text, c.relreplident::text
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2`, schema, table).Scan(&relOID, &ident)
	if err != nil {
		return tableMeta{}, fmt.Errorf("查询表 %s.%s 的复制标识失败: %w", schema, table, err)
	}
	m := tableMeta{RelReplIdent: ident}
	// 主键列按索引列序返回（array_position 保证顺序，回滚 WHERE 的列序无关紧要，但保持稳定利于测试）
	rows, err := p.conn.Query(ctx, `
SELECT a.attname
FROM pg_index i
JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
WHERE i.indrelid = $1::oid AND i.indisprimary
ORDER BY array_position(i.indkey, a.attnum)`, relOID)
	if err != nil {
		return tableMeta{}, fmt.Errorf("查询表 %s.%s 的主键失败: %w", schema, table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			return tableMeta{}, err
		}
		m.PKColumns = append(m.PKColumns, col)
	}
	if err := rows.Err(); err != nil {
		return tableMeta{}, err
	}
	p.cache[key] = m
	return m, nil
}

// buildRollbackStatements 把行变更生成逆向 SQL。
// 关键正确性规则：
//   - 顺序整体倒序：同行多次变更必须逆序回放，否则中间态覆盖终态；
//   - UPDATE 只 SET 真实变化的列（未变更列一律不碰）；
//   - WHERE 使用 IS NOT DISTINCT FROM 把「当时的新值」作为乐观锁守卫，
//     目标行被后续业务改写时命中 0 行，避免盲目覆盖；
//   - DELETE 的重建 INSERT 带 ON CONFLICT DO NOTHING 保证幂等。
//
// 返回 (语句列表, 跳过原因列表)。跳过不视为错误——混合工单里部分表
// 缺 REPLICA IDENTITY FULL 时，可回滚的部分照常生成。
func buildRollbackStatements(ctx context.Context, metaFor tableMetaFunc, changes []decodedChange) ([]string, []string) {
	var stmts, skips []string
	// 倒序回放：与变更发生顺序相反
	for i := len(changes) - 1; i >= 0; i-- {
		ch := changes[i]
		qualified := quotePGIdent(ch.Schema) + "." + quotePGIdent(ch.Table)
		meta, err := metaFor(ctx, ch.Schema, ch.Table)
		if err != nil {
			skips = append(skips, fmt.Sprintf("表 %s.%s：%v", ch.Schema, ch.Table, err))
			continue
		}

		switch ch.Op {
		case "INSERT":
			// 正向插入 → 逆向删除（用新元组定位）
			where, ok := locateColumns(ch.Columns, meta)
			if !ok {
				skips = append(skips, fmt.Sprintf(
					"表 %s.%s 的 INSERT：新元组缺少主键列，无法唯一定位逆向 DELETE", ch.Schema, ch.Table))
				continue
			}
			stmts = append(stmts, fmt.Sprintf("DELETE FROM %s WHERE %s;", qualified, where))

		case "UPDATE":
			// 只有 REPLICA IDENTITY FULL 时 old-key 才是整行，才能恢复旧值
			if meta.RelReplIdent != "f" {
				skips = append(skips, fmt.Sprintf(
					"表 %s.%s 的 UPDATE：REPLICA IDENTITY 非 FULL（当前 %q），WAL 中只有主键旧值、无法恢复变更前列，已跳过；如需回滚请执行 ALTER TABLE %s.%s REPLICA IDENTITY FULL",
					ch.Schema, ch.Table, meta.RelReplIdent, ch.Schema, ch.Table))
				continue
			}
			oldCols := columnMap(ch.OldKey)
			newCols := columnMap(ch.Columns)
			// 变化列 = 旧值 ≠ 新值的列；只回滚这些列
			var sets, guards []string
			for _, col := range ch.Columns {
				old, hasOld := oldCols[col.Name]
				if !hasOld || col.IsNull || (old.IsNull && !col.IsNull) {
					// 旧值缺失或其中一方为 NULL：逐列判定，避免整条丢弃
					if hasOld && (old.IsNull != col.IsNull || old.Value != col.Value) {
						sets = append(sets, fmt.Sprintf("%s = %s", quotePGIdent(col.Name), renderValue(old)))
						guards = append(guards, fmt.Sprintf("%s IS NOT DISTINCT FROM %s", quotePGIdent(col.Name), renderValue(col)))
					}
					continue
				}
				if old.Value != col.Value || old.IsNull != col.IsNull {
					sets = append(sets, fmt.Sprintf("%s = %s", quotePGIdent(col.Name), renderValue(old)))
					guards = append(guards, fmt.Sprintf("%s IS NOT DISTINCT FROM %s", quotePGIdent(col.Name), renderValue(col)))
				}
			}
			// old-key 中存在而 new-tuple 中没有的列（概率极低，防御性处理）
			for _, col := range ch.OldKey {
				if _, ok := newCols[col.Name]; !ok {
					sets = append(sets, fmt.Sprintf("%s = %s", quotePGIdent(col.Name), renderValue(col)))
				}
			}
			if len(sets) == 0 {
				continue // 无列级变化（如 UPDATE SET id=id）
			}
			pkWhere, ok := locateColumns(ch.Columns, meta)
			if !ok {
				skips = append(skips, fmt.Sprintf(
					"表 %s.%s 的 UPDATE：新元组缺少主键列，无法定位逆向 UPDATE", ch.Schema, ch.Table))
				continue
			}
			where := pkWhere
			for _, g := range guards {
				where += " AND " + g
			}
			stmts = append(stmts, fmt.Sprintf("UPDATE %s SET %s WHERE %s;", qualified, strings.Join(sets, ", "), where))

		case "DELETE":
			// 只有旧行完整（FULL，或旧值恰好含全列）才能重建 INSERT
			if len(ch.OldKey) == 0 {
				skips = append(skips, fmt.Sprintf(
					"表 %s.%s 的 DELETE：旧行不可知（无主键且 REPLICA IDENTITY 非 FULL），无法重建，已跳过", ch.Schema, ch.Table))
				continue
			}
			if meta.RelReplIdent != "f" {
				skips = append(skips, fmt.Sprintf(
					"表 %s.%s 的 DELETE：REPLICA IDENTITY 非 FULL（当前 %q），WAL 中只有主键、无法重建整行，已跳过；如需回滚请执行 ALTER TABLE %s.%s REPLICA IDENTITY FULL",
					ch.Schema, ch.Table, meta.RelReplIdent, ch.Schema, ch.Table))
				continue
			}
			var names, values []string
			for _, col := range ch.OldKey {
				names = append(names, quotePGIdent(col.Name))
				values = append(values, renderValue(col))
			}
			stmt := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", qualified, strings.Join(names, ", "), strings.Join(values, ", "))
			// 有主键时用 ON CONFLICT DO NOTHING 保证幂等（重复回滚不报错）
			if len(meta.PKColumns) > 0 {
				var pkQuoted []string
				for _, c := range meta.PKColumns {
					pkQuoted = append(pkQuoted, quotePGIdent(c))
				}
				stmt += " ON CONFLICT (" + strings.Join(pkQuoted, ", ") + ") DO NOTHING"
			}
			stmts = append(stmts, stmt+";")
		}
	}
	return stmts, skips
}

// locateColumns 生成定位条件：有主键用主键；无主键退化成全列匹配（调用方应已提示风险）。
func locateColumns(cols []decodedColumn, meta tableMeta) (string, bool) {
	m := columnMap(cols)
	if len(meta.PKColumns) > 0 {
		var conds []string
		for _, pk := range meta.PKColumns {
			col, ok := m[pk]
			if !ok {
				return "", false
			}
			conds = append(conds, fmt.Sprintf("%s = %s", quotePGIdent(pk), renderValue(col)))
		}
		return strings.Join(conds, " AND "), true
	}
	if len(cols) == 0 {
		return "", false
	}
	var conds []string
	for _, col := range cols {
		conds = append(conds, fmt.Sprintf("%s IS NOT DISTINCT FROM %s", quotePGIdent(col.Name), renderValue(col)))
	}
	return strings.Join(conds, " AND "), true
}

func columnMap(cols []decodedColumn) map[string]decodedColumn {
	m := make(map[string]decodedColumn, len(cols))
	for _, c := range cols {
		m[c.Name] = c
	}
	return m
}

// renderValue 渲染列值为 SQL 字面量。
// test_decoding 的引号值本身已是合法 PG 字面量（quote_literal_cstr 产出，
// standard_conforming_strings=on 语义），原样回填；null → NULL。
func renderValue(col decodedColumn) string {
	if col.IsNull || col.Value == "null" {
		return "NULL"
	}
	return col.Value
}

// quotePGIdent 双引号转义标识符（PG 的标识符大小写折叠规则下最稳妥的引用方式）。
func quotePGIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
