//go:build !prod

package netshape

// SetEnumerator replaces the interface enumeration and returns a function that
// puts back the one it replaced.
//
// It exists for tests: this package's own, and internal/gateway's over the
// endpoint list. That second set is why it is exported rather than hidden in
// an export_test.go, which reaches only the package's own test binary — and
// the assertions that matter most (an endpoint marked, a specific bind listing
// one address, a list that reflects a tunnel appearing between two calls) are
// not in this package.
//
// Exported test-only API is still API: shipped, it is a supported way for
// anything linked into the binary to make the classifier say whatever it
// likes. So it is compiled out of the release build instead. `make app` — the
// only target that produces the bundle Gropius ships — builds with `-tags
// prod`, and this file is absent from it; the dev binary, `go build ./...` and
// every `go test` invocation carry no tags and have it.
// internal/archtest holds both halves of that.
//
// Production never calls it. The mutex is here rather than around a bare
// variable because Addrs is read from the control panel's snapshot on every
// event and from the menu bar's ticker, on their own goroutines, while the
// tests that fix the list live in other packages.
func SetEnumerator(fn func() ([]Interface, error)) func() {
	enumerateMu.Lock()
	defer enumerateMu.Unlock()
	prev := enumerate
	enumerate = fn
	return func() {
		enumerateMu.Lock()
		defer enumerateMu.Unlock()
		enumerate = prev
	}
}
