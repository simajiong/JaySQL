package checker

// SQL 文本空白归一（各文本解析型引擎共用）
//
// 展示用的原文不动，只在「解析入口」用折叠空白的副本：SQL 的词法里空白与单个空格等价，
// 而引擎里的前缀判断、按空格切词、关键字定位都默认「恰好一个空格」。

import "strings"

// collapseSQLWhitespace 把「字面量 / 引用标识符 / 注释」之外的连续空白折叠为单个空格。
//
// 为什么需要：文本解析型引擎（PG/SQLServer）不依赖 AST，规则全部建立在文本匹配上（`strings.Index(lower, "create table")`、
// `HasPrefix(item, "CONSTRAINT ")`、按空格切词等）。真实工单里 `create  table  t_01(id int)`
// 这类多空格写法（手输/复制粘贴）会让所有解析器返回 ok=false，整条语句零结论——
// 规范里开着也永远不报，且没有任何报错提示。
//
// SQL 的空白在词法上与单个空格等价，因此归一不改变语义；字符串字面量、双引号标识符、
// 注释、$$ 美元引用块内部一律原样保留，避免改动业务字面量。
func collapseSQLWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	writeSpace := func() {
		if !prevSpace {
			b.WriteByte(' ')
			prevSpace = true
		}
	}
	i := 0
	for i < len(s) {
		c := s[i]
		// 行注释 / 块注释：原样复制到结束
		if c == '-' && i+1 < len(s) && s[i+1] == '-' {
			j := i
			for j < len(s) && s[j] != '\n' {
				j++
			}
			b.WriteString(s[i:j])
			prevSpace = false
			i = j
			continue
		}
		if c == '/' && i+1 < len(s) && s[i+1] == '*' {
			j := len(s)
			if off := strings.Index(s[i+2:], "*/"); off >= 0 {
				j = i + 2 + off + 2
			}
			b.WriteString(s[i:j])
			prevSpace = false
			i = j
			continue
		}
		// 单引号字符串 / 双引号标识符：内部原样保留（'' 与 "" 为转义）
		if c == '\'' || c == '"' {
			j := i + 1
			for j < len(s) {
				if s[j] == c {
					if j+1 < len(s) && s[j+1] == c {
						j += 2
						continue
					}
					j++
					break
				}
				j++
			}
			b.WriteString(s[i:j])
			prevSpace = false
			i = j
			continue
		}
		// $$tag$$ 美元引用块：内部原样保留（函数体等）
		if c == '$' {
			if j, ok := pgDollarQuotedEnd(s, i); ok {
				b.WriteString(s[i:j])
				prevSpace = false
				i = j
				continue
			}
		}
		switch c {
		case ' ', '\t', '\n', '\r', '\v', '\f':
			writeSpace()
			i++
			continue
		}
		b.WriteByte(c)
		prevSpace = false
		i++
	}
	return strings.TrimSpace(b.String())
}

// pgDollarQuotedEnd 若 s[i] 起是 $$tag$$ 美元引用块，返回块结束后的下标
func pgDollarQuotedEnd(s string, i int) (int, bool) {
	end := strings.IndexByte(s[i+1:], '$')
	if end < 0 {
		return 0, false
	}
	tag := s[i : i+1+end+1] // $tag$
	for k := 1; k < len(tag)-1; k++ {
		ch := tag[k]
		if !(ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9') {
			return 0, false
		}
	}
	off := strings.Index(s[i+len(tag):], tag)
	if off < 0 {
		return len(s), true
	}
	return i + len(tag) + off + len(tag), true
}
