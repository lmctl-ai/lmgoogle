// SPDX-License-Identifier: MIT

package membudget

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/lmctl-ai/lmcode-go"
)

const (
	EnvVarName       = "LMGOOGLE_MAX_MEMORY_MB"
	DefaultBudgetMiB = int64(4096)
)

var (
	budgetMiB    atomic.Int64
	budgetBytes  atomic.Int64
	chargedBytes atomic.Int64
	baselineHeap atomic.Uint64
)

func init() {
	budgetMiB.Store(DefaultBudgetMiB)
	budgetBytes.Store(DefaultBudgetMiB * 1024 * 1024)
}

// InitFromEnv parses LMGOOGLE_MAX_MEMORY_MB (default 4096 MiB).
// Returns an error if the environment variable is set to an invalid value (exit code 2).
func InitFromEnv() (int64, error) {
	raw, exists := os.LookupEnv(EnvVarName)
	if !exists {
		SetBudgetMiB(DefaultBudgetMiB)
		return DefaultBudgetMiB, nil
	}
	if raw == "" {
		return 0, fmt.Errorf("invalid %s=\"\": must be a positive integer MiB", EnvVarName)
	}
	val, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || val <= 0 || val > 1048576 {
		return 0, fmt.Errorf("invalid %s=%q: must be a positive integer MiB", EnvVarName, raw)
	}
	SetBudgetMiB(val)
	return val, nil
}

// SetBudgetMiB configures the active memory budget in MiB.
func SetBudgetMiB(mib int64) {
	if mib <= 0 {
		mib = DefaultBudgetMiB
	}
	bytes := mib * 1024 * 1024
	budgetMiB.Store(mib)
	budgetBytes.Store(bytes)
	chargedBytes.Store(0)
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	baselineHeap.Store(ms.HeapAlloc)
	debug.SetMemoryLimit(bytes)
}

// BudgetMiB returns the configured memory budget in MiB.
func BudgetMiB() int64 {
	return budgetMiB.Load()
}

// ResetRun resets per-run charged memory counters and captures baseline heap.
func ResetRun() {
	chargedBytes.Store(0)
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	baselineHeap.Store(ms.HeapAlloc)
}

func exceededError(usedBytes, limitBytes int64) error {
	mib := budgetMiB.Load()
	return lmcode.NewHostError(fmt.Sprintf(
		"memory budget exceeded: %d bytes > %d bytes (%s=%d)",
		usedBytes, limitBytes, EnvVarName, mib,
	))
}

// Require checks whether allocating n additional bytes would exceed the budget.
func Require(n int64) error {
	if n < 0 {
		return nil
	}
	limit := budgetBytes.Load()
	cur := chargedBytes.Load()
	if cur+n > limit {
		return exceededError(cur+n, limit)
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	base := baselineHeap.Load()
	var heapDelta int64
	if ms.HeapAlloc > base {
		heapDelta = int64(ms.HeapAlloc - base)
	}
	total := cur + n
	if heapDelta+n > total {
		total = heapDelta + n
	}
	if total > limit {
		return exceededError(total, limit)
	}
	return nil
}

// Charge records n bytes against the memory budget and fails if exceeded.
func Charge(n int64) error {
	if n <= 0 {
		return nil
	}
	if err := Require(n); err != nil {
		return err
	}
	next := chargedBytes.Add(n)
	limit := budgetBytes.Load()
	if next > limit {
		return exceededError(next, limit)
	}
	return nil
}

// StartWatchdog periodically samples Go HeapAlloc while a program is running
// and invokes onExceeded if heap delta exceeds the configured budget.
func StartWatchdog(onExceeded func(error)) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if err := Require(0); err != nil {
					onExceeded(err)
					return
				}
			}
		}
	}()
	return func() {
		close(done)
	}
}
