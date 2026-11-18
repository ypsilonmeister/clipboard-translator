package platform

import "testing"

func TestCursorHintCoordinate(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		cursor, start, end, want int32
	}{
		{"positive offset", 300, 0, 1920, 316},
		{"exact fit", 1880, 0, 1920, 1896},
		{"flip near edge", 1919, 0, 1920, 1879},
		{"monitor left of primary", -1, -1920, 0, -41},
		{"clamp on narrow work area", 25, 10, 45, 10},
		{"pointer outside work area", -50, 0, 1920, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cursorHintCoordinate(tc.cursor, tc.start, tc.end); got != tc.want {
				t.Errorf("cursorHintCoordinate(%d, %d, %d) = %d, want %d", tc.cursor, tc.start, tc.end, got, tc.want)
			}
		})
	}
}
