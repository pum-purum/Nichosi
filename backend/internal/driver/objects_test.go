package driver

import "testing"

func TestQuoteIdent(t *testing.T) {
	for _, tc := range []struct{ in, want string }{{"lab", "`lab`"}, {"a`b", "`a``b`"}} {
		if got := QuoteIdent(tc.in); got != tc.want {
			t.Errorf("QuoteIdent(%q) = %q", tc.in, got)
		}
	}
}

func TestMySQLAccountCategory(t *testing.T) {
	for _, tc := range []struct{ name, host, want string }{
		{"mysql.infoschema", "localhost", "internal"},
		{"mysql.session", "localhost", "internal"},
		{"mysql.sys", "localhost", "internal"},
		{"root", "localhost", "default-admin"},
		{"root", "%", "other"},
		{"mysql.sys", "%", "other"},
		{"mysql.custom", "localhost", "other"},
		{"mysql.session.backup", "localhost", "other"},
		{"MYSQL.SYS", "localhost", "other"},
		{"mysql.sys", "127.0.0.1", "other"},
		{"app_reader", "localhost", "other"},
		{"", "localhost", "other"},
	} {
		t.Run(tc.name+"@"+tc.host, func(t *testing.T) {
			if got := mysqlAccountCategory(tc.name, tc.host); got != tc.want {
				t.Errorf("category = %q, want %q", got, tc.want)
			}
		})
	}
}
