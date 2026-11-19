package licensescan

import (
	"sort"
	"strings"
)

// License signatures, applied in this order. A module may match more than one
// signature; the result is deduplicated and sorted for determinism.
const (
	sigMITNoAttribution = "MIT No Attribution"
	sigApacheLicense    = "Apache License"
	sigApacheVersion    = "Version 2.0"
	sigMPL              = "Mozilla Public License"
	sigMITGrant         = "Permission is hereby granted, free of charge"
	sigMITHeader        = "MIT License"
	sigBSDGrant         = "Redistribution and use in source and binary forms"
	sigBSDNeither       = "Neither the name"
	sigISCGrant         = "Permission to use, copy, modify, and distribute this software for any purpose"
	sigISCGrantAndOr    = "Permission to use, copy, modify, and/or distribute this software for any purpose"
	sigUnlicense        = "free and unencumbered software released into the public domain"
)

// detectLicenses classifies the concatenated license and notice file texts of
// a module. The result is sorted and never empty: unrecognised text yields
// "Other".
func detectLicenses(files []LicenseFile) []string {
	var b strings.Builder
	for _, file := range files {
		b.WriteString(file.Text)
		b.WriteString("\n")
	}
	return classify(b.String())
}

// classify returns the license names detected in text. Signatures are matched
// against text with runs of whitespace collapsed, so wrapped license text still
// matches.
func classify(text string) []string {
	text = strings.Join(strings.Fields(text), " ")
	found := map[string]struct{}{}
	add := func(name string) { found[name] = struct{}{} }

	if strings.Contains(text, sigApacheLicense) && strings.Contains(text, sigApacheVersion) {
		add("Apache-2.0")
	}
	if strings.Contains(text, sigMPL) {
		add("MPL-2.0")
	}
	// MIT-0 text repeats the MIT grant sentence, so it must not also report MIT.
	if strings.Contains(text, sigMITNoAttribution) {
		add("MIT-0")
	} else if strings.Contains(text, sigMITGrant) || strings.Contains(text, sigMITHeader) {
		add("MIT")
	}
	if strings.Contains(text, sigBSDGrant) {
		if strings.Contains(text, sigBSDNeither) {
			add("BSD-3-Clause")
		} else {
			add("BSD-2-Clause")
		}
	}
	if strings.Contains(text, sigISCGrant) || strings.Contains(text, sigISCGrantAndOr) {
		add("ISC")
	}
	if strings.Contains(text, sigUnlicense) {
		add("Unlicense")
	}
	if len(found) == 0 {
		return []string{"Other"}
	}

	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
