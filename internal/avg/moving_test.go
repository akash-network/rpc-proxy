package avg

import (
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMovingDurationAverageTest(t *testing.T) {
	t.Run("window-1", func(t *testing.T) {
		a := Moving(1)
		_ = a.Next(5 * time.Second)
		_ = a.Next(10 * time.Second)
		require.Equal(t, time.Second, a.Next(time.Second))
		require.Equal(t, time.Second, a.Last())
	})

	t.Run("window-2", func(t *testing.T) {
		a := Moving(2)
		_ = a.Next(5 * time.Second)
		_ = a.Next(10 * time.Second)
		_ = a.Next(2 * time.Second)
		expected := 6 * time.Second
		require.Equal(t, expected, a.Next(10*time.Second))
		require.Equal(t, expected, a.Last())
	})

	t.Run("reset", func(t *testing.T) {
		a := Moving(2)
		for i := 0; i < 10; i++ {
			_ = a.Next(time.Duration(rand.Intn(100)) * time.Second)
		}
		require.NotZero(t, a.Last())
		a.Reset()
		require.Zero(t, a.Last())
	})
}
