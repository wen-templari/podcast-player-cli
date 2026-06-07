package main

import (
	"testing"

	"charm.land/lipgloss/v2"
)

func TestTruncateRespectsDisplayWidthForMixedCJKText(t *testing.T) {
	input := "2024 年森泽奖欧文组有一个令人难忘的名字"

	got := truncate(input, 20)

	if width := lipgloss.Width(got); width > 20 {
		t.Fatalf("expected truncated width <= 20, got %d for %q", width, got)
	}
	if got[len(got)-len("…"):] != "…" {
		t.Fatalf("expected truncated text to end with ellipsis, got %q", got)
	}
}

func TestTruncateReturnsEllipsisWhenWidthIsOne(t *testing.T) {
	if got := truncate("中文内容", 1); got != "…" {
		t.Fatalf("expected ellipsis for width 1, got %q", got)
	}
}
