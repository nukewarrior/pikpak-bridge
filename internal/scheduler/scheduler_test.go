package scheduler

import (
	"testing"
	"time"

	"github.com/nukewarrior/pikpak-bridge/internal/pikpak"
)

func TestSelectPikPakAccountPrefersLowerLoadRatio(t *testing.T) {
	now := time.Now()
	accounts := []pikpak.AccountSnapshot{
		{ID: "quota-empty", Enabled: true, Healthy: true, QuotaRemaining: 0, StorageFree: 100, MaxJobs: 2},
		{ID: "busy", Enabled: true, Healthy: true, QuotaRemaining: 3, ActiveJobs: 2, MaxJobs: 2, StorageFree: 100},
		{ID: "half", Enabled: true, Healthy: true, QuotaRemaining: 3, ActiveJobs: 1, MaxJobs: 2, StorageFree: 100},
		{ID: "idle", Enabled: true, Healthy: true, QuotaRemaining: 2, ActiveJobs: 0, MaxJobs: 2, StorageFree: 100},
	}

	got, err := SelectPikPakAccount(accounts, 10, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "idle" {
		t.Fatalf("want idle, got %s", got.ID)
	}
}

func TestSelectPikPakAccountUsesQuotaThenLRU(t *testing.T) {
	now := time.Now()
	accounts := []pikpak.AccountSnapshot{
		{ID: "older", Enabled: true, Healthy: true, QuotaRemaining: 3, MaxJobs: 2, StorageFree: 100, LastUsedAt: now.Add(-time.Hour)},
		{ID: "newer", Enabled: true, Healthy: true, QuotaRemaining: 3, MaxJobs: 2, StorageFree: 100, LastUsedAt: now},
		{ID: "less-quota", Enabled: true, Healthy: true, QuotaRemaining: 2, MaxJobs: 2, StorageFree: 100, LastUsedAt: time.Time{}},
	}

	got, err := SelectPikPakAccount(accounts, 10, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "older" {
		t.Fatalf("want older, got %s", got.ID)
	}
}

func TestSelectPikPakAccountRejectsInsufficientSpace(t *testing.T) {
	now := time.Now()
	accounts := []pikpak.AccountSnapshot{
		{ID: "full", Enabled: true, Healthy: true, QuotaRemaining: 3, StorageFree: 0, MaxJobs: 2},
		{ID: "small", Enabled: true, Healthy: true, QuotaRemaining: 3, StorageFree: 1024, MaxJobs: 2},
	}
	if _, err := SelectPikPakAccount(accounts, 2048, now); err != ErrNoPikPakAccount {
		t.Fatalf("want ErrNoPikPakAccount, got %v", err)
	}
}
