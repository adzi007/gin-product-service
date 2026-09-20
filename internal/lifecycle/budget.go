// Package lifecycle owns the process-level lifecycle constants shared by the
// HTTP server and infrastructure adapters. It has no dependencies so that no
// package below it can form an import cycle.
package lifecycle

import "time"

// ServiceShutdownBudget is the single authoritative graceful shutdown window for
// the whole process: HTTP intake stop, accepted-request drain, dependency close,
// and the bounded telemetry flush all share it. Other values that must fit
// strictly inside it (for example telemetry timeouts) import this constant.
const ServiceShutdownBudget = 15 * time.Second
