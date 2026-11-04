package driver

import "testing"

func TestMySQLEngineFromVersion(t *testing.T) {
	for _, tc := range []struct{ version, want string }{
		{"8.4.3", "mysql"},
		{"8.0.40-Percona", "mysql"},
		{"5.5.5-10.11.6-MariaDB-0+deb12u1", "mariadb"},
		{"11.6.2-MariaDB", "mariadb"},
		{"10.6.0-mariadb", "mariadb"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			if got := mysqlEngineFromVersion(tc.version); got != tc.want {
				t.Fatalf("engine = %q, want %q", got, tc.want)
			}
		})
	}
}
