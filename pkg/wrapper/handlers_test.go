package wrapper

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReplaceAllButPortRewritesPlainAndEscapedURLs(t *testing.T) {
	server := []byte("https://s252-en.ogame.gameforge.com")
	proxy := []byte("http://srv30.mikr.us:40158")

	page := []byte(`<a href="https://s252-en.ogame.gameforge.com/game/index.php?page=ingame">x</a>`)
	assert.Equal(t,
		`<a href="http://srv30.mikr.us:40158/game/index.php?page=ingame">x</a>`,
		string(replaceAllButPort(page, server, proxy)))
}

func TestReplaceAllButPortKeepsChatNodeURL(t *testing.T) {
	// The chat node answers on its own port. Rewriting serverURL here would produce
	// "http://srv30.mikr.us:40158:24114/..." — socket.io never loads and the in-game
	// chat stays frozen on the history the page was rendered with.
	server := []byte(`https:\/\/s252-en.ogame.gameforge.com`)
	proxy := []byte(`http:\/\/srv30.mikr.us:40158`)

	page := []byte(`var nodeUrl = "https:\/\/s252-en.ogame.gameforge.com:24114\/socket.io\/socket.io.js"`)
	assert.Equal(t, string(page), string(replaceAllButPort(page, server, proxy)))
}

func TestReplaceAllButPortMixedOccurrences(t *testing.T) {
	server := []byte("https://s1-en.ogame.gameforge.com")
	proxy := []byte("http://127.0.0.1:8080")

	page := []byte("https://s1-en.ogame.gameforge.com/game/index.php|https://s1-en.ogame.gameforge.com:24114/socket.io/")
	assert.Equal(t,
		"http://127.0.0.1:8080/game/index.php|https://s1-en.ogame.gameforge.com:24114/socket.io/",
		string(replaceAllButPort(page, server, proxy)))
}

func TestLooksLikeJSON(t *testing.T) {
	assert.True(t, looksLikeJSON([]byte(`{"status":"ok"}`)))
	assert.True(t, looksLikeJSON([]byte("\n  [1,2,3]")))
	assert.False(t, looksLikeJSON([]byte("<!DOCTYPE html><html></html>")))
	assert.False(t, looksLikeJSON([]byte("{not json")))
	assert.False(t, looksLikeJSON(nil))
}
