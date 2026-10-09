package agent

import (
	"testing"
	"time"
)

func TestElapsedMs(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want int64
	}{
		{0, 1},
		{300 * time.Microsecond, 1},
		{999 * time.Microsecond, 1},
		{time.Millisecond, 1},
		{1500 * time.Microsecond, 1}, // 1ms 以上保持原有的截断口径，与历史归档一致
		{2 * time.Millisecond, 2},
		{1234567 * time.Microsecond, 1234},
	} {
		if got := elapsedMs(tc.d); got != tc.want {
			t.Errorf("elapsedMs(%v) = %d，应为 %d", tc.d, got, tc.want)
		}
	}
}
