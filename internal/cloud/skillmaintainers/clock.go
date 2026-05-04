package skillmaintainers

import "time"

// nowFn is a tiny indirection so tests can stub the clock.
var nowFn = func() time.Time { return time.Now().UTC() }
