package v874

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractOfferOfTheDayPrice(t *testing.T) {
	pageHTMLBytes, _ := os.ReadFile("../../../samples/v8.7.4/en/traderImportExport.html")
	price, token, _, _, _ := NewExtractor().ExtractOfferOfTheDay(pageHTMLBytes)
	assert.Equal(t, int64(178224), price)
	assert.Equal(t, "2a38193e2fa6047e1d92d2f2c71c00fd", token)
}

func TestExtractOfferOfTheDayWithoutInlineToken(t *testing.T) {
	pageHTMLBytes, err := os.ReadFile("../../../samples/v8.7.4/en/traderImportExport.html")
	assert.NoError(t, err)

	// OGame 13 returns the request token as newAjaxToken in the JSON envelope, so
	// content.trader no longer contains the legacy `var token = ...` declaration.
	pageHTML := string(pageHTMLBytes)
	pageHTML = strings.Replace(pageHTML, `var token = "2a38193e2fa6047e1d92d2f2c71c00fd";`, "", 1)
	price, token, planetResources, multiplier, err := NewExtractor().ExtractOfferOfTheDay([]byte(pageHTML))

	assert.NoError(t, err)
	assert.Equal(t, int64(178224), price)
	assert.Empty(t, token)
	assert.NotEmpty(t, planetResources)
	assert.NotZero(t, multiplier.Metal)
}

func TestExtractAuction(t *testing.T) {
	pageHTMLBytes, _ := os.ReadFile("../../../samples/v8.7.4/en/traderAuctioneer.html")
	res, _ := NewExtractor().ExtractAuction(pageHTMLBytes)
	assert.Equal(t, "43576386810cdf91a833a6239f323f66", res.Token)
}
