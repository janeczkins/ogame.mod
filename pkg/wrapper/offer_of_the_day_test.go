package wrapper

import (
	"encoding/json"
	"testing"

	"github.com/alaingilbert/ogame/pkg/ogame"
	"github.com/alaingilbert/ogame/pkg/utils"
	"github.com/stretchr/testify/assert"
)

// planetResource is unexported, so build the map the way the extractor does: from the game's
// own "var planetResources" JSON.
func parsePlanetResources(t *testing.T, raw string) ogame.PlanetResources {
	t.Helper()
	var pr ogame.PlanetResources
	assert.NoError(t, json.Unmarshal([]byte(raw), &pr))
	return pr
}

const sampleTraderPlanetResources = `{
	"33623197":{"input":{"metal":495801,"crystal":848179,"deuterium":110502},"isMoon":false},
	"33634862":{"input":{"metal":0,"crystal":0,"deuterium":2675750},"isMoon":true},
	"33631691":{"input":{"metal":6376482,"crystal":3002778,"deuterium":4867976},"isMoon":false},
	"33634865":{"input":{"metal":0,"crystal":0,"deuterium":660250},"isMoon":true},
	"33638959":{"input":{"metal":23304,"crystal":12227,"deuterium":400},"isMoon":false}
}`

// The offered price rarely divides evenly by the deuterium/crystal multiplier, and rounding the
// last chunk up used to push the running total below zero — every following celestial then bid a
// negative amount and the game refused the trade.
func TestCalcResourcesNeverBidsNegativeAmounts(t *testing.T) {
	planetResources := parsePlanetResources(t, sampleTraderPlanetResources)
	multiplier := ogame.Multiplier{Metal: 1, Crystal: 1.5, Deuterium: 3, Honor: 100}

	for price := int64(36109); price <= 36117; price++ {
		payload, missing := calcResources(price, planetResources, multiplier)
		assert.Zero(t, missing, "price %d should be affordable", price)
		for key, values := range payload {
			for _, v := range values {
				assert.GreaterOrEqual(t, utils.DoParseI64(v), int64(0), "price %d, %s = %s", price, key, v)
			}
		}
	}
}

// Ranging over the resource map directly made the bid depend on Go's randomized map iteration,
// so the same offer produced a different payload on every attempt.
func TestCalcResourcesIsDeterministic(t *testing.T) {
	planetResources := parsePlanetResources(t, sampleTraderPlanetResources)
	multiplier := ogame.Multiplier{Metal: 1, Crystal: 1.5, Deuterium: 3, Honor: 100}

	first, _ := calcResources(36112, planetResources, multiplier)
	for i := 0; i < 20; i++ {
		again, _ := calcResources(36112, planetResources, multiplier)
		assert.Equal(t, first.Encode(), again.Encode())
	}
}

// The bid must be worth exactly the asking price: the totals are what the game charges.
func TestCalcResourcesCoversExactlyThePrice(t *testing.T) {
	planetResources := parsePlanetResources(t, sampleTraderPlanetResources)
	multiplier := ogame.Multiplier{Metal: 1, Crystal: 1.5, Deuterium: 3, Honor: 100}

	for price := int64(36109); price <= 36117; price++ {
		payload, _ := calcResources(price, planetResources, multiplier)
		var total float64
		for key, values := range payload {
			mult := multiplier.Metal
			switch {
			case key[len(key)-8:] == "crystal]":
				mult = multiplier.Crystal
			case key[len(key)-10:] == "deuterium]":
				mult = multiplier.Deuterium
			}
			for _, v := range values {
				total += float64(utils.DoParseI64(v)) * mult
			}
		}
		// Rounding up the final chunk can overpay by less than one unit's worth.
		assert.GreaterOrEqual(t, total, float64(price), "price %d", price)
		assert.Less(t, total, float64(price)+multiplier.Deuterium, "price %d", price)
	}
}

// Markup as the trader page renders it in both states (OGame 12.10.0): the price and the trade
// token stay on the page after the offer has been taken, only the panels swap visibility.
const traderPagePayable = `<div class="right_content">
	<div class="bargain_overlay" style="display: none"><p class="bargain_text"></p></div>
	<div class="payment" style="display: block"><div class="price js_import_price ">36.111</div></div>
</div>`

const traderPageAlreadyBought = `<div class="right_content">
	<div class="bargain_overlay" style="display: block"><p class="bargain_text">Dzisiaj nie ma już więcej ofert. Wróć jutro.</p></div>
	<div class="payment" style="display: none"><div class="price js_import_price green_text">36.111</div></div>
</div>`

func TestOfferOfTheDayAlreadyBought(t *testing.T) {
	assert.False(t, offerOfTheDayAlreadyBought([]byte(traderPagePayable)))
	assert.True(t, offerOfTheDayAlreadyBought([]byte(traderPageAlreadyBought)))
	// Unrecognised markup must not be mistaken for "already bought" — a buy attempt with a
	// real error message is more useful than a silent "come back tomorrow".
	assert.False(t, offerOfTheDayAlreadyBought([]byte(`<div class="right_content"></div>`)))
}

// A player who cannot cover the price must be told so, instead of sending an underfunded bid
// that the game rejects with an unhelpful error.
func TestCalcResourcesReportsMissingResources(t *testing.T) {
	planetResources := parsePlanetResources(t, `{"33638959":{"input":{"metal":100,"crystal":0,"deuterium":0}}}`)
	multiplier := ogame.Multiplier{Metal: 1, Crystal: 1.5, Deuterium: 3, Honor: 100}

	_, missing := calcResources(1000, planetResources, multiplier)
	assert.Equal(t, int64(900), missing)
}
