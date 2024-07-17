package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMovingDurationAverageTest(t *testing.T) {
	t.Run("window-1", func(t *testing.T) {
		a := NewPingAverage(1)
		n := a.Next(5 * time.Second)
		n = a.Next(10 * time.Second)
		n = a.Next(time.Second)
		l := a.Last()
		require.Equal(t, time.Second, n)
		require.Equal(t, time.Second, l)
	})

	t.Run("window-2", func(t *testing.T) {
		a := NewPingAverage(2)
		n := a.Next(5 * time.Second)
		n = a.Next(10 * time.Second)
		n = a.Next(2 * time.Second)
		n = a.Next(10 * time.Second)
		l := a.Last()
		require.Equal(t, 6*time.Second, n)
		require.Equal(t, 6*time.Second, l)
	})
}
