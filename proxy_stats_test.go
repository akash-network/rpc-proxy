package main

import (
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCompareServerStats(t *testing.T) {
	names := func(st []ServerStat) []string {
		r := make([]string, 0, len(st))
		for _, s := range st {
			r = append(r, s.Name)
		}
		return r
	}
	v := []ServerStat{
		{
			Name:        "1",
			Avg:         time.Second,
			Degraded:    false,
			Initialized: true,
		},
		{
			Name:        "2",
			Avg:         time.Second,
			Degraded:    true,
			Initialized: true,
		},
		{
			Name:        "3",
			Avg:         0,
			Degraded:    false,
			Initialized: false,
		},
		{
			Name:        "4",
			Avg:         time.Millisecond * 10,
			Degraded:    false,
			Initialized: true,
		},
		{
			Name:        "5",
			Avg:         0,
			Degraded:    true,
			Initialized: true,
		},
	}
	t.Log(names(v))
	sort.Sort(serverStats(v))
	require.Equal(t, []string{"4", "1", "5", "2", "3"}, names(v))
}
