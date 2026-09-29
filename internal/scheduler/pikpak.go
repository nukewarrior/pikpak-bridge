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
	for _, a := range accounts {
		if !a.Enabled || !a.Healthy || a.QuotaRemaining <= 0 {
			continue
		}
		if !a.CooldownUntil.IsZero() && now.Before(a.CooldownUntil) {
			continue
		}
		if a.MaxJobs > 0 && a.ActiveJobs >= a.MaxJobs {
			continue
		}
		if requiredBytes > 0 && a.StorageFree > 0 && a.StorageFree < requiredBytes {
			continue
		}
		candidates = append(candidates, a)
	}
	if len(candidates) == 0 {
		return pikpak.AccountSnapshot{}, ErrNoPikPakAccount
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.QuotaRemaining != b.QuotaRemaining {
			return a.QuotaRemaining > b.QuotaRemaining
		}
		if a.ActiveJobs != b.ActiveJobs {
			return a.ActiveJobs < b.ActiveJobs
		}
		if !a.LastUsedAt.Equal(b.LastUsedAt) {
			return a.LastUsedAt.Before(b.LastUsedAt)
		}
		return a.Name < b.Name
	})
	return candidates[0], nil
}
