package ogame

import (
	"math"
)

type interstellarAnomalyScanner struct {
	BaseBuilding
}

func newInterstellarAnomalyScanner() *interstellarAnomalyScanner {
	b := new(interstellarAnomalyScanner)
	b.Name = "interstellar anomaly scanner"
	b.ID = InterstellarAnomalyScannerID
	b.IncreaseFactor = 1.5
	b.BaseCost = Resources{Metal: 84, Crystal: 42, Deuterium: 14}
	b.Requirements = map[ID]int64{AstrophysicsID: 5, ShipyardID: 5, CombustionDriveID: 1}
	return b
}

// GetPrice returns the price to build the given level
func (b interstellarAnomalyScanner) GetPrice(level int64, lfBonuses LfBonuses) Resources {
	tmp := func(baseCost int64, increaseFactor float64, level int64) int64 {
		return int64(math.Ceil(float64(baseCost) * math.Pow(increaseFactor, float64(level-1))))
	}
	price := Resources{
		Metal:     tmp(b.BaseCost.Metal, b.IncreaseFactor, level),
		Crystal:   tmp(b.BaseCost.Crystal, b.IncreaseFactor, level),
		Deuterium: tmp(b.BaseCost.Deuterium, b.IncreaseFactor, level),
	}
	bonus := lfBonuses.CostTimeBonuses[b.ID].Cost
	return price.SubPercent(bonus)
}
