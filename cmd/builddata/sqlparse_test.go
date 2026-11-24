package main

import (
	"strings"
	"testing"
)

func TestParseInserts(t *testing.T) {
	src := "\xef\xbb\xbf/* head 'quote */\n-- c\nSET NAMES utf8;\nCREATE TABLE t (a int COMMENT 'it''s;');\n" +
		"INSERT INTO wilayah (kode, nama)\nVALUES\n('11','Pasi Kuala Ba''u'),\n('11.01', 'x,(y);z');\n" +
		"-- [12]\nINSERT INTO `t`(`a`,`b`,`c`) VALUES\n(-1.5e3, NULL, ''), (2, null, 'a''');\n"
	got, err := ParseInserts(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Table != "wilayah" || got[1].Table != "t" {
		t.Fatalf("got %+v", got)
	}
	r := got[0].Rows
	if len(r) != 2 || r[0][1].S != "Pasi Kuala Ba'u" || r[1][1].S != "x,(y);z" {
		t.Fatalf("rows %+v", r)
	}
	if c := got[1].Cols; len(c) != 3 || c[1] != "b" {
		t.Fatalf("cols %v", c)
	}
	r = got[1].Rows
	if r[0][0] != (Value{KNumber, "-1.5e3"}) || r[0][1].Kind != KNull || r[0][2] != (Value{KString, ""}) || r[1][2].S != "a'" {
		t.Fatalf("rows %+v", r)
	}
}

func TestParseLongString(t *testing.T) {
	long := strings.Repeat("[1.5,2.5],", 3_000_000)
	got, err := ParseInserts("INSERT INTO t(a,b) VALUES (1,'" + long + "');")
	if err != nil || len(got[0].Rows[0][1].S) != len(long) {
		t.Fatalf("err=%v", err)
	}
}

func TestParseErrors(t *testing.T) {
	for src, want := range map[string]string{
		"INSERT INTO t(a) VALUES ('x":         "unterminated",
		"INSERT INTO t(a) VALUES (abc);":      "invalid literal",
		"INSERT INTO t(a,b) VALUES (1);":      "",
		"INSERT INTO t(a) VALUES (1) (2);":    "",
		"/* open":                             "",
		"INSERT INTO t(a) VALUES ('a\\'b');":  "backslash", // backslash escape
		"SELECT 'x\\y';":                      "backslash", // backslash in a skipped statement
		"REPLACE INTO t(a) VALUES (1);":       "",
		"replace into t(a) VALUES (1);":       "",
		"INSERT IGNORE INTO t(a) VALUES (1);": "",
	} {
		if _, err := ParseInserts(src); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err=%v, want substring %q", src, err, want)
		}
	}
}
