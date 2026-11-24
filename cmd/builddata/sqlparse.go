package main

import (
	"errors"
	"fmt"
	"strings"
)

// Kind of a SQL literal inside a VALUES tuple.
type Kind int

const (
	KString Kind = iota
	KNumber
	KNull
)

// Value is one literal. Numbers keep their raw text.
type Value struct {
	Kind Kind
	S    string
}

// Insert is one parsed INSERT statement.
type Insert struct {
	Table string
	Cols  []string
	Rows  [][]Value
}

type lexer struct {
	s   string
	pos int
}

func (l *lexer) errf(format string, a ...any) error {
	line := 1 + strings.Count(l.s[:min(l.pos, len(l.s))], "\n")
	return fmt.Errorf("sql line %d: %s", line, fmt.Sprintf(format, a...))
}

// skipSpace skips whitespace and comments (/* */, -- and #).
func (l *lexer) skipSpace() error {
	for l.pos < len(l.s) {
		c := l.s[l.pos]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			l.pos++
		case strings.HasPrefix(l.s[l.pos:], "/*"):
			end := strings.Index(l.s[l.pos+2:], "*/")
			if end < 0 {
				return l.errf("unterminated comment")
			}
			l.pos += end + 4
		case strings.HasPrefix(l.s[l.pos:], "--") || c == '#':
			end := strings.IndexByte(l.s[l.pos:], '\n')
			if end < 0 {
				l.pos = len(l.s)
			} else {
				l.pos += end + 1
			}
		default:
			return nil
		}
	}
	return nil
}

// quoted reads a '...' string (with ” escape) or `...` identifier starting at the quote.
func (l *lexer) quoted() (string, error) {
	q := l.s[l.pos]
	var sb strings.Builder
	i := l.pos + 1
	for i < len(l.s) {
		j := strings.IndexByte(l.s[i:], q)
		if j < 0 {
			break
		}
		if strings.IndexByte(l.s[i:i+j], '\\') >= 0 {
			l.pos = i
			return "", l.errf("backslash escapes are not supported")
		}
		sb.WriteString(l.s[i : i+j])
		i += j + 1
		if i < len(l.s) && l.s[i] == q {
			sb.WriteByte(q)
			i++
			continue
		}
		l.pos = i
		return sb.String(), nil
	}
	return "", l.errf("unterminated quoted string")
}

func (l *lexer) word() string {
	st := l.pos
	for l.pos < len(l.s) {
		c := l.s[l.pos]
		if c == ',' || c == ')' || c == '(' || c == ';' || c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			break
		}
		l.pos++
	}
	return l.s[st:l.pos]
}

func (l *lexer) expect(c byte) error {
	if err := l.skipSpace(); err != nil {
		return err
	}
	if l.pos >= len(l.s) || l.s[l.pos] != c {
		return l.errf("expected %q", c)
	}
	l.pos++
	return nil
}

func (l *lexer) ident() (string, error) {
	if err := l.skipSpace(); err != nil {
		return "", err
	}
	if l.pos < len(l.s) && l.s[l.pos] == '`' {
		return l.quoted()
	}
	w := l.word()
	if w == "" {
		return "", l.errf("expected identifier")
	}
	return w, nil
}

func (l *lexer) value() (Value, error) {
	if err := l.skipSpace(); err != nil {
		return Value{}, err
	}
	if l.pos >= len(l.s) {
		return Value{}, l.errf("unexpected end of input")
	}
	if l.s[l.pos] == '\'' {
		s, err := l.quoted()
		return Value{KString, s}, err
	}
	w := l.word()
	if strings.EqualFold(w, "NULL") {
		return Value{Kind: KNull}, nil
	}
	if !isNumber(w) {
		return Value{}, l.errf("invalid literal %q", w)
	}
	return Value{KNumber, w}, nil
}

func isNumber(w string) bool {
	if w == "" {
		return false
	}
	digits := 0
	for i := 0; i < len(w); i++ {
		switch c := w[i]; {
		case c >= '0' && c <= '9':
			digits++
		case c == '-' || c == '+' || c == '.' || c == 'e' || c == 'E':
		default:
			return false
		}
	}
	return digits > 0
}

// skipStatement skips to the end of the current statement, honouring quotes and comments.
func (l *lexer) skipStatement() error {
	for l.pos < len(l.s) {
		if err := l.skipSpace(); err != nil {
			return err
		}
		if l.pos >= len(l.s) {
			return nil
		}
		switch l.s[l.pos] {
		case ';':
			l.pos++
			return nil
		case '\'', '`', '"':
			if _, err := l.quoted(); err != nil {
				return err
			}
		default:
			l.pos++
		}
	}
	return nil
}

func (l *lexer) insert() (Insert, error) {
	var ins Insert
	if err := l.skipSpace(); err != nil {
		return ins, err
	}
	if w := l.word(); !strings.EqualFold(w, "INTO") { // also rejects INSERT IGNORE
		return ins, l.errf("expected INTO, got %q (INSERT IGNORE is not supported)", w)
	}
	t, err := l.ident()
	if err != nil {
		return ins, err
	}
	ins.Table = t
	if err := l.skipSpace(); err != nil {
		return ins, err
	}
	if l.pos < len(l.s) && l.s[l.pos] == '(' {
		l.pos++
		for {
			c, err := l.ident()
			if err != nil {
				return ins, err
			}
			ins.Cols = append(ins.Cols, c)
			if err := l.skipSpace(); err != nil {
				return ins, err
			}
			if l.pos < len(l.s) && l.s[l.pos] == ',' {
				l.pos++
				continue
			}
			if err := l.expect(')'); err != nil {
				return ins, err
			}
			break
		}
	}
	if err := l.skipSpace(); err != nil {
		return ins, err
	}
	if w := l.word(); !strings.EqualFold(w, "VALUES") {
		return ins, l.errf("expected VALUES, got %q", w)
	}
	for {
		if err := l.expect('('); err != nil {
			return ins, err
		}
		var row []Value
		for {
			v, err := l.value()
			if err != nil {
				return ins, err
			}
			row = append(row, v)
			if err := l.skipSpace(); err != nil {
				return ins, err
			}
			if l.pos < len(l.s) && l.s[l.pos] == ',' {
				l.pos++
				continue
			}
			if err := l.expect(')'); err != nil {
				return ins, err
			}
			break
		}
		if ins.Cols != nil && len(row) != len(ins.Cols) {
			return ins, l.errf("row has %d values, want %d", len(row), len(ins.Cols))
		}
		ins.Rows = append(ins.Rows, row)
		if err := l.skipSpace(); err != nil {
			return ins, err
		}
		if l.pos >= len(l.s) {
			return ins, nil
		}
		switch l.s[l.pos] {
		case ',':
			l.pos++
		case ';':
			l.pos++
			return ins, nil
		default:
			return ins, l.errf("expected , or ; after tuple")
		}
	}
}

// ParseInserts extracts every INSERT ... VALUES statement; other statements are skipped.
// Only the doubled-quote escape is supported; backslashes, REPLACE and INSERT IGNORE are errors.
func ParseInserts(src string) ([]Insert, error) {
	l := &lexer{s: strings.TrimPrefix(src, "\xef\xbb\xbf")}
	var out []Insert
	for {
		if err := l.skipSpace(); err != nil {
			return nil, err
		}
		if l.pos >= len(l.s) {
			return out, nil
		}
		save := l.pos
		w := l.word()
		if strings.EqualFold(w, "REPLACE") {
			return nil, l.errf("REPLACE statements are not supported")
		}
		if strings.EqualFold(w, "INSERT") {
			ins, err := l.insert()
			if err != nil {
				return nil, err
			}
			out = append(out, ins)
			continue
		}
		l.pos = save
		if err := l.skipStatement(); err != nil {
			return nil, err
		}
		if l.pos == save {
			return nil, errors.New("parser made no progress")
		}
	}
}
