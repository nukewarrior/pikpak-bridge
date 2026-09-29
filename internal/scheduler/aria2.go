package scheduler

import (
	"errors"
	"math"
	"sort"
)

var ErrNoAria2Instance = errors.New("no eligible aria2 instance")

type Aria2Snapshot struct {
	Name      string
	Enabled   bool
	Healthy   bool
	Active    int
	Waiting   int
	MaxActive int
	Weight    float64
}

func SelectAria2Instance(instances []Aria2Snapshot) (Aria2Snapshot, error) {
	type candidate struct {
		instance Aria2Snapshot
		score    float64
	}
	var candidates []candidate
	for _, in := range instances {
		if !in.Enabled || !in.Healthy || in.MaxActive <= 0 || in.Active >= in.MaxActive {
			continue
		}
		weight := in.Weight
		if weight <= 0 {
			weight = 1
		}
		load := (float64(in.Active) + 0.5*float64(in.Waiting)) / float64(in.MaxActive)
		candidates = append(candidates, candidate{
			instance: in,
			score:    load / weight,
		})
	}
	if len(candidates) == 0 {
		return Aria2Snapshot{}, ErrNoAria2Instance
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if math.Abs(candidates[i].score-candidates[j].score) > 1e-9 {
			return candidates[i].score < candidates[j].score
		}
		if candidates[i].instance.Active != candidates[j].instance.Active {
			return candidates[i].instance.Active < candidates[j].instance.Active
		}
		return candidates[i].instance.Name < candidates[j].instance.Name
	})
	return candidates[0].instance, nil
}
