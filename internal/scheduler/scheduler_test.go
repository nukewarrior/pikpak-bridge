package scheduler

import (
	"testing"
	"time"

	"github.com/nukewarrior/pikpak-bridge/internal/pikpak"
)

func TestSelectPikPakAccount(t *testing.T) {
	now := time.Now()
	accounts := []pikpak.AccountSnapshot{
		{Name: "quota-empty", Enabled: true, Healthy: true, QuotaRemaining: 0, StorageFree: 100},
		{Name: "busy", Enabled: true, Healthy: true, QuotaRemaining: 3, ActiveJobs: 2, MaxJobs: 2, StorageFree: 100},
		{Name: "a", Enabled: true, Healthy: true, QuotaRemaining: 2, ActiveJobs: 1, MaxJobs: 2, StorageFree: 100, LastUsedAt: now.Add(-time.Minute)},
		{Name: "b", Enabled: true, Healthy: true, QuotaRemaining: 3, ActiveJobs: 1, MaxJobs: 2, StorageFree: 100, LastUsedAt: now},
	}
	got, err := SelectPikPakAccount(accounts, 10, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "b" {
		t.Fatalf("want b, got %s", got.Name)
	}
}

func TestSelectAria2Instance(t *testing.T) {
	instances := []Aria2Snapshot{
		{Name: "full", Enabled: true, Healthy: true, Active: 4, MaxActive: 4, Weight: 10},
		{Name: "home", Enabled: true, Healthy: true, Active: 2, MaxActive: 4, Weight: 10},
		{Name: "vps", Enabled: true, Healthy: true, Active: 0, MaxActive: 2, Weight: 1},
	}
	got, err := SelectAria2Instance(instances)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "home" {
		t.Fatalf("want home because capacity and weight outweigh idle status, got %s", got.Name)
	}
}

func TestSelectPikPakAccountRejectsInsufficientSpace(t *testing.T) {
	now := time.Now()
	accounts := []pikpak.AccountSnapshot{
		{Name: "full", Enabled: true, Healthy: true, QuotaRemaining: 3, StorageFree: 0},
		{Name: "small", Enabled: true, Healthy: true, QuotaRemaining: 3, StorageFree: 1024},
	}
	if _, err := SelectPikPakAccount(accounts, 2048, now); err != ErrNoPikPakAccount {
		t.Fatalf("want ErrNoPikPakAccount, got %v", err)
	}
}
