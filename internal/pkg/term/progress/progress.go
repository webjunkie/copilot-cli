// Copyright 2019 Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

// Package progress provides data and functionality to display updates to the terminal.
package progress

// Text is a description of the progress update.
type Text string

// Status is the condition of the progress update.
type Status string

// Common progression life-cycle for an update.
const (
	StatusInProgress Status = "in progress"
	StatusFailed     Status = "failed"
	StatusComplete   Status = "complete"
	StatusSkipped    Status = "skipped"
)
