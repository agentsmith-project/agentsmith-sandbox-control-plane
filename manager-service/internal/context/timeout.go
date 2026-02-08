package context

import (
	 stdcontext "context"
	 "time"
)

// Timeout constants defining a hierarchy of timeouts for different operations.
// These timeouts are organized from shortest to longest, with each level
// encompassing the levels below it.
const (
 // DefaultTimeout is the standard timeout for most operations (30 seconds).
 // Use this for general API calls, database queries, and simple operations.
 DefaultTimeout = 30 * time.Second

 // ShortTimeout is for fast, lightweight operations (5 seconds).
 // Use this for health checks, cache lookups, and non-critical queries.
 ShortTimeout = 5 * time.Second

 // LongTimeout is for complex operations that may take time (5 minutes).
 // Use this for file uploads/downloads, pod creation, and resource-intensive tasks.
 LongTimeout = 5 * time.Minute

 // SnapshotTimeout is specifically for workspace snapshot operations (10 minutes).
 // This is the longest timeout as snapshots involve large file operations.
 SnapshotTimeout = 10 * time.Minute
)

// WithDefaultTimeout creates a context with the DefaultTimeout.
// This is the most common timeout for general operations.
func WithDefaultTimeout(parent stdcontext.Context) (stdcontext.Context, stdcontext.CancelFunc) {
 return stdcontext.WithTimeout(parent, DefaultTimeout)
}

// WithShortTimeout creates a context with the ShortTimeout.
// Use this for quick operations like health checks.
func WithShortTimeout(parent stdcontext.Context) (stdcontext.Context, stdcontext.CancelFunc) {
 return stdcontext.WithTimeout(parent, ShortTimeout)
}

// WithLongTimeout creates a context with the LongTimeout.
// Use this for operations that may take longer like pod creation.
func WithLongTimeout(parent stdcontext.Context) (stdcontext.Context, stdcontext.CancelFunc) {
 return stdcontext.WithTimeout(parent, LongTimeout)
}

// WithSnapshotTimeout creates a context with the SnapshotTimeout.
// Use this specifically for workspace snapshot operations.
func WithSnapshotTimeout(parent stdcontext.Context) (stdcontext.Context, stdcontext.CancelFunc) {
 return stdcontext.WithTimeout(parent, SnapshotTimeout)
}
