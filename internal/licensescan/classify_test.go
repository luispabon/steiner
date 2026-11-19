package licensescan

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		want []string
	}{
		{name: "mit.txt", want: []string{"MIT"}},
		{name: "apache.txt", want: []string{"Apache-2.0"}},
		{name: "bsd2.txt", want: []string{"BSD-2-Clause"}},
		{name: "bsd3.txt", want: []string{"BSD-3-Clause"}},
		{name: "isc.txt", want: []string{"ISC"}},
		{name: "isc-and-or.txt", want: []string{"ISC"}},
		{name: "dual-mit-apache.txt", want: []string{"Apache-2.0", "MIT"}},
		{name: "mit0.txt", want: []string{"MIT-0"}},
		{name: "unknown.txt", want: []string{"Other"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "license", tt.name))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			if got := classify(string(data)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("classify(%s) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestDetectLicensesConcatenatesFiles(t *testing.T) {
	files := []LicenseFile{
		{Path: "LICENSE", Text: "MIT License\n"},
		{Path: "NOTICE", Text: "Redistribution and use in source and binary forms, with or without\nmodification, are permitted provided that Neither the name of the copyright\nholder is used.\n"},
	}
	want := []string{"BSD-3-Clause", "MIT"}
	if got := detectLicenses(files); !reflect.DeepEqual(got, want) {
		t.Errorf("detectLicenses() = %v, want %v", got, want)
	}
}
