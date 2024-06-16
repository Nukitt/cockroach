package span

import (
	"testing"
)

func FuzzLLRBFrontier(f *testing.F) {
	defer enableBtreeFrontier(false)()
	fuzzFrontier(f)
}
