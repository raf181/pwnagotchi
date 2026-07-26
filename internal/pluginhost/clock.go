package pluginhost

import "time"

// Clock is the production wall-clock implementation exposed to plugins.
type Clock struct{}

func (Clock) Now() time.Time { return time.Now() }
