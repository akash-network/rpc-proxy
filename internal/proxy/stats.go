package proxy

import "time"

type ServerStat struct {
	Name        string
	URL         string
	Avg         time.Duration
	Degraded    bool
	Initialized bool
	Requests    int64
	ErrorRate   float64
}

type serverStats []ServerStat

func (st serverStats) Len() int      { return len(st) }
func (st serverStats) Swap(i, j int) { st[i], st[j] = st[j], st[i] }
func (st serverStats) Less(i, j int) bool {
	si := st[i]
	sj := st[j]
	if si.Initialized && !sj.Initialized {
		return true
	}
	if sj.Initialized && !si.Initialized {
		return false
	}
	if si.Degraded && !sj.Degraded {
		return false
	}
	if sj.Degraded && !si.Degraded {
		return true
	}
	if si.ErrorRate > sj.ErrorRate {
		return false
	}
	if sj.ErrorRate > si.ErrorRate {
		return true
	}
	return si.Avg < sj.Avg
}
