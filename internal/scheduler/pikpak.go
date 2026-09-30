package scheduler

import (
	"errors"
	"sort"
	"time"

	"github.com/nukewarrior/pikpak-bridge/internal/pikpak"
)

var ErrNoPikPakAccount = errors.New("no eligible pikpak account")

func SelectPikPakAccount(accounts []pikpak.AccountSnapshot, requiredBytes int64, now time.Time) (pikpak.AccountSnapshot, error) {
	candidates := make([]pikpak.AccountSnapshot, 0, len(accounts))
	for _, account := range accounts {
		if !account.Enabled || !account.Healthy || account.QuotaRemaining <= 0 {
			continue
		}
		if !account.CooldownUntil.IsZero() && now.Before(account.CooldownUntil) {
			continue
		}
		if account.MaxJobs <= 0 || account.ActiveJobs >= account.MaxJobs {
			continue
		}
		if requiredBytes > 0 && account.StorageFree < requiredBytes {
			continue
		}
		candidates = append(candidates, account)
	}
	if len(candidates) == 0 {
		return pikpak.AccountSnapshot{}, ErrNoPikPakAccount
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]

		left := int64(a.ActiveJobs) * int64(b.MaxJobs)
		right := int64(b.ActiveJobs) * int64(a.MaxJobs)
		if left != right {
			return left < right
		}
		if a.QuotaRemaining != b.QuotaRemaining {
			return a.QuotaRemaining > b.QuotaRemaining
		}
		if !a.LastUsedAt.Equal(b.LastUsedAt) {
			return a.LastUsedAt.Before(b.LastUsedAt)
		}
		if a.StorageFree != b.StorageFree {
			return a.StorageFree > b.StorageFree
		}
		return a.ID < b.ID
	})
	return candidates[0], nil
}
