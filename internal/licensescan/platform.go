// Package licensescan collects the license and notice files of every
// third-party module linked into Steiner release binaries and renders them as a
// single notices document.
package licensescan

// Platform is a GOOS/GOARCH pair.
type Platform struct {
	GOOS   string
	GOARCH string
}

// String returns the platform as "goos/goarch".
func (p Platform) String() string {
	return p.GOOS + "/" + p.GOARCH
}

// ReleasePlatforms is the release build matrix. It mirrors the matrix in
// .github/workflows/build-binaries.yml; update both together.
var ReleasePlatforms = []Platform{
	{GOOS: "linux", GOARCH: "amd64"},
	{GOOS: "linux", GOARCH: "arm64"},
	{GOOS: "darwin", GOARCH: "amd64"},
	{GOOS: "darwin", GOARCH: "arm64"},
	{GOOS: "windows", GOARCH: "amd64"},
}
