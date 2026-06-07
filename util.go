package main

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

func clampIndex(v, length int) int {
	if length == 0 {
		return 0
	}
	if v < 0 {
		return 0
	}
	if v >= length {
		return length - 1
	}
	return v
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Round(time.Second).Seconds())
	hours := total / 3600
	minutes := (total % 3600) / 60
	seconds := total % 60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%02d:%02d", minutes, seconds)
}

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width <= 1 {
		return "…"
	}

	const ellipsis = "…"
	maxWidth := width - lipgloss.Width(ellipsis)
	if maxWidth <= 0 {
		return ellipsis
	}

	var builder strings.Builder
	currentWidth := 0
	for _, r := range s {
		runeWidth := lipgloss.Width(string(r))
		if currentWidth+runeWidth > maxWidth {
			break
		}
		builder.WriteRune(r)
		currentWidth += runeWidth
	}

	if builder.Len() > 0 {
		return builder.String() + ellipsis
	}
	return ellipsis
}

func hashBytes(data []byte) string {
	sum := sha1.Sum(data)
	return hex.EncodeToString(sum[:])
}

func cacheKey(value string) string {
	sum := sha1.Sum([]byte(value))
	return hex.EncodeToString(sum[:])
}
