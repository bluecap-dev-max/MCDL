package main

import "strings"

// runeWidth 返回字符在等宽终端里占用的列数（CJK 宽字符算 2 列）。
func runeWidth(r rune) int {
	if r == 0 || r == '\n' || r == '\r' {
		return 0
	}
	if r < 0x20 || (r >= 0x7f && r < 0xa0) {
		return 0
	}
	if isZeroWidth(r) {
		return 0
	}
	if isWide(r) {
		return 2
	}
	return 1
}

func isZeroWidth(r rune) bool {
	switch {
	case r >= 0x0300 && r <= 0x036f:
		return true
	case r >= 0x1ab0 && r <= 0x1aff:
		return true
	case r >= 0x1dc0 && r <= 0x1dff:
		return true
	case r >= 0x20d0 && r <= 0x20ff:
		return true
	case r >= 0xfe00 && r <= 0xfe0f:
		return true
	case r >= 0xfe20 && r <= 0xfe2f:
		return true
	}
	return false
}

func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115f:
		return true
	case r >= 0x2e80 && r <= 0x303e:
		return true
	case r >= 0x3041 && r <= 0x33ff:
		return true
	case r >= 0x3400 && r <= 0x4dbf:
		return true
	case r >= 0x4e00 && r <= 0x9fff:
		return true
	case r >= 0xa000 && r <= 0xa4cf:
		return true
	case r >= 0xa960 && r <= 0xa97f:
		return true
	case r >= 0xac00 && r <= 0xd7a3:
		return true
	case r >= 0xf900 && r <= 0xfaff:
		return true
	case r >= 0xfe10 && r <= 0xfe19:
		return true
	case r >= 0xfe30 && r <= 0xfe6f:
		return true
	case r >= 0xff00 && r <= 0xff60:
		return true
	case r >= 0xffe0 && r <= 0xffe6:
		return true
	case r >= 0x1f300 && r <= 0x1f64f:
		return true
	case r >= 0x1f900 && r <= 0x1f9ff:
		return true
	case r >= 0x20000 && r <= 0x3fffd:
		return true
	}
	return false
}

// stringWidth 返回字符串在终端里占用的列数。
func stringWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

// truncateWidth 按显示宽度截断字符串（不会把宽字符劈成两半）。
func truncateWidth(s string, max int) string {
	if max <= 0 {
		return ""
	}
	w := 0
	for i, r := range s {
		rw := runeWidth(r)
		if w+rw > max {
			return s[:i]
		}
		w += rw
	}
	return s
}

// padRight 截断并右侧补空格到指定显示宽度。
func padRight(s string, width int) string {
	s = truncateWidth(s, width)
	d := width - stringWidth(s)
	if d <= 0 {
		return s
	}
	return s + strings.Repeat(" ", d)
}

// padLeft 截断并左侧补空格到指定显示宽度。
func padLeft(s string, width int) string {
	s = truncateWidth(s, width)
	d := width - stringWidth(s)
	if d <= 0 {
		return s
	}
	return strings.Repeat(" ", d) + s
}

// center 把字符串居中到指定宽度。
func center(s string, width int) string {
	s = truncateWidth(s, width)
	d := width - stringWidth(s)
	if d <= 0 {
		return s
	}
	l := d / 2
	return strings.Repeat(" ", l) + s + strings.Repeat(" ", d-l)
}

// fillTo 把字符串截断后用指定字符填充到目标显示宽度。
func fillTo(s string, width int, fill string) string {
	s = truncateWidth(s, width)
	d := width - stringWidth(s)
	if d <= 0 {
		return s
	}
	if fill == "" {
		fill = " "
	}
	return s + strings.Repeat(fill, d)
}
