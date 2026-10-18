package domain

import "time"

// Snapshot contains one atomically published source scan.
type Snapshot struct {
	LastScan           time.Time
	LastSuccessfulScan time.Time
	Tables             []Table
}
