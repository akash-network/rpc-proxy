package block

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// ErrBlockTooLow when the block number is lower than the latest block.
var ErrBlockTooLow = errors.New("new block number is lower than the latest block")

// BlockManager is a singleton structure to hold the latest block application-wide.
type BlockManager struct {
	latestBlock   atomic.Int64
	lastAdvanced  atomic.Int64 // unix nano timestamp of last block height advance
	lastChecked   atomic.Int64 // unix nano timestamp of last SetLatestBlock call
}

var instance *BlockManager
var once sync.Once

// GetInstance ensures a single instance of BlockManager.
func GetInstance() *BlockManager {
	once.Do(func() {
		instance = &BlockManager{}
	})
	return instance
}

// NewBlockManager creates a new standalone BlockManager.
// Use this in tests to avoid shared singleton state.
func NewBlockManager() *BlockManager {
	return &BlockManager{}
}

// SetLatestBlock updates the latest block if it's greater than the current one.
func (b *BlockManager) SetLatestBlock(newBlock int64) error {
	now := time.Now().UnixNano()
	b.lastChecked.Store(now)

	current := b.latestBlock.Load()
	if newBlock < current {
		return ErrBlockTooLow
	}
	if newBlock > current {
		b.lastAdvanced.Store(now)
	}
	b.latestBlock.Store(newBlock)
	return nil
}

// GetLatestBlock returns the current latest block number.
func (b *BlockManager) GetLatestBlock() int64 {
	return b.latestBlock.Load()
}

// LastAdvancedAt returns the time when the block height last advanced.
// Returns zero time if block height has never advanced.
func (b *BlockManager) LastAdvancedAt() time.Time {
	ns := b.lastAdvanced.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// LastCheckedAt returns the time of the last SetLatestBlock call.
// Returns zero time if SetLatestBlock has never been called.
func (b *BlockManager) LastCheckedAt() time.Time {
	ns := b.lastChecked.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}
