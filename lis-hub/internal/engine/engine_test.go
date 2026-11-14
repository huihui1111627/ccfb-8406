package engine

import (
	"testing"
)

func hist(zs ...float64) func(int) []float64 {
	return func(n int) []float64 {
		if len(zs) > n {
			return zs[:n]
		}
		return zs
	}
}

func has(v []string, rule string) bool {
	for _, x := range v {
		if x == rule {
			return true
		}
	}
	return false
}

func TestWestgard(t *testing.T) {
	cases := []struct {
		name string
		z    float64
		hist []float64
		want []string
	}{
		{"正常", 0.5, nil, nil},
		{"1_2s警告", 2.3, nil, []string{"1_2s"}},
		{"1_3s失控", 3.2, nil, []string{"1_2s", "1_3s"}},
		{"2_2s同侧", 2.4, []float64{2.3}, []string{"1_2s", "2_2s"}},
		{"2_2s异侧不算", 2.4, []float64{-2.3}, []string{"1_2s", "R_4s"}}, // 差4.7触发R_4s
		{"R_4s", 2.2, []float64{-2.1}, []string{"1_2s", "R_4s"}},
		{"4_1s", 1.3, []float64{1.2, 1.4, 1.1}, []string{"4_1s"}},
		{"10x", 0.5, []float64{0.3, 0.4, 0.2, 0.6, 0.1, 0.5, 0.4, 0.3, 0.2}, []string{"10x"}},
	}
	for _, c := range cases {
		got := Westgard(c.z, hist(c.hist...))
		for _, w := range c.want {
			if !has(got, w) {
				t.Errorf("%s: z=%.1f 缺少规则 %s (got %v)", c.name, c.z, w, got)
			}
		}
		if len(c.want) == 0 && len(got) != 0 {
			t.Errorf("%s: 不应有违反, got %v", c.name, got)
		}
	}
}
