package span

import (
	"testing"
)

func FuzzBtreeFrontier(f *testing.F) {
	defer enableBtreeFrontier(true)()
	fuzzFrontier(f)
}

func FuzzLLRBFrontier(f *testing.F) {
	defer enableBtreeFrontier(false)()
	fuzzFrontier(f)
}
