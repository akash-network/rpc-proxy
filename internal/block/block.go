package block

import (
	"errors"
	"sync"
	"sync/atomic"
)

// ErrBlockTooLow when the block number is lower than the latest block.
var ErrBlockTooLow = errors.New("new block number is lower than the latest block")

// BlockManager is a singleton structure to hold the latest block application-wide.
type BlockManager struct {
	latestBlock atomic.Int64
}

var instance *BlockManager
var once sync.Once

// GetInstance ensures a single instance of BlockManager
func GetInstance() *BlockManager {
	once.Do(func() {
		instance = &BlockManager{}
	})
	return instance
}

// SetLatestBlock updates the latest block if it's greater than the current one.
func (b *BlockManager) SetLatestBlock(newBlock int64) error {
	current := b.latestBlock.Load()
	if newBlock < current {
		return ErrBlockTooLow
	}
	b.latestBlock.Store(newBlock)
	return nil
}

// GetLatestBlock returns the current latest block number.
func (b *BlockManager) GetLatestBlock() int64 {
	return b.latestBlock.Load()
}
