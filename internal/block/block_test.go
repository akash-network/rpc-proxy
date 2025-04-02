package block

import (
	"sync"
	"testing"
)

func TestBlockManagerSingleton(t *testing.T) {
	bm1 := GetInstance()
	bm2 := GetInstance()

	if bm1 != bm2 {
		t.Errorf("GetInstance() returned different instances")
	}
}

func TestSetLatestBlock(t *testing.T) {
	bm := GetInstance()
	bm.SetLatestBlock(100)

	if bm.GetLatestBlock() != 100 {
		t.Errorf("expected latest block to be 100, got %d", bm.GetLatestBlock())
	}
}

func TestSetLatestBlockLowerValue(t *testing.T) {
	bm := GetInstance()
	bm.SetLatestBlock(200)        // Set an initial higher block
	err := bm.SetLatestBlock(150) // Try setting a lower block
	if err != ErrBlockTooLow {
		t.Errorf("expected ErrBlockTooLow, got %v", err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	bm := GetInstance()
	var wg sync.WaitGroup
	expectedBlock := int64(500)

	for i := int64(0); i < expectedBlock; i++ {
		wg.Add(1)
		go func(blockNumber int64) {
			defer wg.Done()
			bm.SetLatestBlock(blockNumber)
		}(i)
	}

	wg.Wait()

	if bm.GetLatestBlock() != expectedBlock-1 {
		t.Errorf("expected latest block to be %d, got %d", expectedBlock-1, bm.GetLatestBlock())
	}
}
