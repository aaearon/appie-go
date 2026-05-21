package main

import (
	"slices"
	"testing"

	appie "github.com/gwillem/appie-go"
)

func TestFormatBonus(t *testing.T) {
	tests := []struct {
		name      string
		mechanism string
		start     string
		end       string
		want      string
	}{
		{"empty mechanism", "", "", "", ""},
		{"empty mechanism with dates ignored", "", "2026-05-18", "2026-05-25", ""},
		{"mechanism only", "3 VOOR 6.99", "", "", "3 VOOR 6.99"},
		{"mechanism with only start", "3 VOOR 6.99", "2026-05-18", "", "3 VOOR 6.99"},
		{"mechanism with only end", "3 VOOR 6.99", "", "2026-05-25", "3 VOOR 6.99"},
		{"mechanism with both dates", "3 VOOR 6.99", "2026-05-18", "2026-05-25", "3 VOOR 6.99 (2026-05-18 → 2026-05-25)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatBonus(tt.mechanism, tt.start, tt.end)
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMissingIDs(t *testing.T) {
	tests := []struct {
		name     string
		ids      []int
		products []appie.Product
		want     []int
	}{
		{
			name:     "all present",
			ids:      []int{1, 2, 3},
			products: []appie.Product{{ID: 1}, {ID: 2}, {ID: 3}},
			want:     nil,
		},
		{
			name:     "some missing",
			ids:      []int{1, 2, 3, 4},
			products: []appie.Product{{ID: 1}, {ID: 3}},
			want:     []int{2, 4},
		},
		{
			name:     "all missing",
			ids:      []int{1, 2},
			products: nil,
			want:     []int{1, 2},
		},
		{
			name:     "preserves input order",
			ids:      []int{9, 5, 7},
			products: []appie.Product{{ID: 5}},
			want:     []int{9, 7},
		},
		{
			name:     "duplicate input id reported each time",
			ids:      []int{1, 1, 2},
			products: []appie.Product{{ID: 2}},
			want:     []int{1, 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := missingIDs(tt.ids, tt.products)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
