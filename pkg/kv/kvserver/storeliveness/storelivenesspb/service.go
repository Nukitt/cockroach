// Copyright 2024 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package storelivenesspb

// Epoch is an epoch in the Store Liveness fabric, referencing an uninterrupted
// period of support from one store to another. A store can unilaterally
// increment the epoch for which it requests support from another store (e.g.
// after a restart).
type Epoch int64

// SafeValue implements the redact.SafeValue interface.
func (e Epoch) SafeValue() {}
