//go:build darwin && (amd64 || arm64)

package main

import "testing"

func TestSupportedMacOS(t *testing.T) {
	for _, tc := range []struct {
		version string
		wantErr bool
	}{
		{version: "26.7.1", wantErr: true},
		{version: "27.0.1"},
		{version: "28.0"},
		{version: "invalid", wantErr: true},
	} {
		t.Run(tc.version, func(t *testing.T) {
			if got := checkSupportedMacOS(tc.version); (got != nil) != tc.wantErr {
				t.Fatalf("checkSupportedMacOS(%q) = %v, want error %t", tc.version, got, tc.wantErr)
			}
		})
	}
}
