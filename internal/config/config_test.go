package config

import "testing"

func TestParseSize(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"50GB", 50 * 1024 * 1024 * 1024},
		{"500MB", 500 * 1024 * 1024},
		{"1024B", 1024},
		{"2KB", 2 * 1024},
		{"1G", 1024 * 1024 * 1024},
		{"10M", 10 * 1024 * 1024},
		{"0", 0},
		{"100", 100},
	}
	for _, c := range cases {
		got, err := ParseSize(c.in)
		if err != nil {
			t.Errorf("ParseSize(%q) 报错: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseSize(%q) = %d, 期望 %d", c.in, got, c.want)
		}
	}
}

func TestParseSizeInvalid(t *testing.T) {
	bad := []string{"", "abc", "10XB", "GB"}
	for _, s := range bad {
		if _, err := ParseSize(s); err == nil {
			t.Errorf("ParseSize(%q) 期望报错，但成功了", s)
		}
	}
}
