package sqladapter

import "testing"

func TestParseCreateTableShape(t *testing.T) {
	cases := []struct {
		name          string
		ddl           string
		wantPartition string
		wantKey       string
	}{
		{
			name: "doris range + duplicate key",
			ddl: "CREATE TABLE `orders` (\n" +
				"  `id` bigint NULL,\n" +
				"  `event_date` date NULL\n" +
				") ENGINE=OLAP\n" +
				"DUPLICATE KEY(`id`, `event_date`)\n" +
				"PARTITION BY RANGE(`event_date`)\n" +
				"(PARTITION p20260101 VALUES LESS THAN ('2026-01-02'))",
			wantPartition: "RANGE(event_date)",
			wantKey:       "DUPLICATE KEY(id, event_date)",
		},
		{
			name: "doris list + unique key",
			ddl: "CREATE TABLE `u` (`id` int, `region` varchar(20)) ENGINE=OLAP\n" +
				"UNIQUE KEY(`id`)\n" +
				"PARTITION BY LIST(`region`) ()",
			wantPartition: "LIST(region)",
			wantKey:       "UNIQUE KEY(id)",
		},
		{
			name: "mysql range expression + primary key",
			ddl: "CREATE TABLE `logs` (\n  `id` int,\n  `d` date,\n  PRIMARY KEY (`id`)\n)\n" +
				"/*!50100 PARTITION BY RANGE (to_days(`d`))\n(PARTITION p0 VALUES LESS THAN (1)) */",
			wantPartition: "RANGE(d)",
			wantKey:       "PRIMARY KEY(id)",
		},
		{
			name: "range columns multi",
			ddl: "CREATE TABLE t (a int, b int) ENGINE=OLAP\n" +
				"DUPLICATE KEY(`a`)\n" +
				"PARTITION BY RANGE COLUMNS(`a`, `b`) ()",
			wantPartition: "RANGE(a, b)",
			wantKey:       "DUPLICATE KEY(a)",
		},
		{
			name:          "no partition, no key",
			ddl:           "CREATE TABLE `t` (`id` int, `name` varchar(10)) ENGINE=InnoDB",
			wantPartition: "",
			wantKey:       "",
		},
		{
			name:          "key only",
			ddl:           "CREATE TABLE `t` (`id` int, PRIMARY KEY (`id`)) ENGINE=InnoDB",
			wantPartition: "",
			wantKey:       "PRIMARY KEY(id)",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotP, gotK := parseCreateTableShape(c.ddl)
			if gotP != c.wantPartition {
				t.Errorf("partition = %q, want %q", gotP, c.wantPartition)
			}
			if gotK != c.wantKey {
				t.Errorf("key = %q, want %q", gotK, c.wantKey)
			}
		})
	}
}
