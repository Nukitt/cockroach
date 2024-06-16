package span

import "testing"

func FuzzBtreeFrontier(f *testing.F) {
	defer enableBtreeFrontier(true)()
	fuzzFrontier(f)
}
