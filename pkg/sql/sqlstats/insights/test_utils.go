// Copyright 2024 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package insights

import "github.com/cockroachdb/cockroach/pkg/sql/clusterunique"

// TestingKnobs provides hooks and testingKnobs for unit tests.
type TestingKnobs struct {
	// OnSessionClear is a callback that is triggered when the locking
	// registry clears a session entry.
	OnSessionClear func(sessionID clusterunique.ID)

	// InsightsWriterTxnInterceptor is a callback that's triggered when a txn insight
	// is observed by the ingester. The callback is called instead of writing the
	// insight to the buffer.
	InsightsWriterTxnInterceptor func(sessionID clusterunique.ID, transaction *Transaction)

	// InsightsWriterStmtInterceptor is a callback that's triggered when a stmt insight
	// is observed. The callback is called instead of writing the insight to the buffer.
	InsightsWriterStmtInterceptor func(sessionID clusterunique.ID, statement *Statement)
}

// ModuleTestingKnobs implements base.ModuleTestingKnobs interface.
func (*TestingKnobs) ModuleTestingKnobs() {}
